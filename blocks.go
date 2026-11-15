package main

// 2.3: блокировки и подсказки.
//   п. 24 — детские устройства: расписание блокировки сервисов, «сайты для взрослых»;
//   п. 25 — блокировка рекламы для сети и для устройства (встроенные списки + geosite, если есть);
//   п. 20 — подсказки: сайты, которые не открываются через провайдера → «Пустить через VPN?».

import (
	"bytes"
	"context"
	"crypto/tls"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var adsBuiltin = d("doubleclick.net", "googlesyndication.com", "googleadservices.com", "adservice.google.com", "pagead2.googlesyndication.com",
	"ads.youtube.com", "adnxs.com", "criteo.com", "criteo.net", "taboola.com", "outbrain.com", "scorecardresearch.com", "moatads.com",
	"adsrvr.org", "rubiconproject.com", "pubmatic.com", "openx.net", "advertising.com", "amazon-adsystem.com", "an.yandex.ru",
	"yandexadexchange.net", "adfox.ru", "adriver.ru", "mc.yandex.ru", "ads.vk.com", "ad.mail.ru", "top-fwz1.mail.ru", "adhigh.net",
	"admitad.com", "appsflyer.com", "app-measurement.com", "unityads.unity3d.com", "applovin.com", "mopub.com", "inmobi.com")

var adultBuiltin = d("pornhub.com", "xvideos.com", "xnxx.com", "xhamster.com", "redtube.com", "youporn.com", "onlyfans.com", "chaturbate.com",
	"stripchat.com", "spankbang.com", "brazzers.com", "fansly.com", "rule34.xxx", "e-hentai.org", "nhentai.net", "porn.com", "tube8.com")

var geoCatCache struct {
	sync.Mutex
	key string
	has map[string]bool
}

// geositeHas — есть ли категория в geosite.dat (читаем файл не чаще смены его даты; бережём память).
func geositeHas(cat string) bool {
	p := filepath.Join(assetDir(), "geosite.dat")
	st, err := os.Stat(p)
	if err != nil {
		return false
	}
	key := st.ModTime().String()
	geoCatCache.Lock()
	defer geoCatCache.Unlock()
	if geoCatCache.key != key {
		geoCatCache.key, geoCatCache.has = key, map[string]bool{}
		if guard.memFor(st.Size(), "geosite.dat") {
			if b, err := os.ReadFile(p); err == nil {
				for _, c := range []string{"CATEGORY-ADS-ALL", "CATEGORY-PORN"} {
					geoCatCache.has[c] = bytes.Contains(b, []byte(c))
				}
			}
		}
	}
	return geoCatCache.has[strings.ToUpper(cat)]
}

type blockRule struct {
	role string
	m    map[string]any
}

func (a *app) tzOff() int {
	a.watch.mu.Lock()
	defer a.watch.mu.Unlock()
	return a.watch.settings.TzOffsetMin
}

var tzOffCache int // заполняется в rulesLoop/handleRules через app — здесь только чтение

func inWindow(h, from, to int) bool {
	if from == to {
		return false
	}
	if from < to {
		return h >= from && h < to
	}
	return h >= from || h < to
}

func localHourAt(now time.Time) int {
	return now.In(time.FixedZone("", tzOffCache*60)).Hour()
}

func blockRules(st *rulesState, now time.Time) []blockRule {
	var out []blockRule
	ads := append([]string{}, adsBuiltin...)
	if geositeHas("category-ads-all") {
		ads = append(ads, "geosite:category-ads-all")
	}
	adult := append([]string{}, adultBuiltin...)
	if geositeHas("category-porn") {
		adult = append(adult, "geosite:category-porn")
	}
	h := localHourAt(now)
	ips := make([]string, 0, len(st.Devices))
	for ip := range st.Devices {
		ips = append(ips, ip)
	}
	sort.Strings(ips)
	var adsOff []string // устройства, где реклама разрешена, хотя в сети выключена
	for _, ip := range ips {
		dv := st.Devices[ip]
		src := []string{ip}
		if dv.Adult {
			out = append(out, blockRule{"kids", map[string]any{"source": src, "domain": adult, "outboundTag": "block"}})
		}
		if len(dv.KidsBlock) > 0 && inWindow(h, dv.KidsFrom, dv.KidsTo) {
			dom, ipl := collect(dv.KidsBlock)
			if len(dom) > 0 {
				out = append(out, blockRule{"kids", map[string]any{"source": src, "domain": dom, "outboundTag": "block"}})
			}
			if len(ipl) > 0 {
				out = append(out, blockRule{"kids", map[string]any{"source": src, "ip": ipl, "outboundTag": "block"}})
			}
		}
		if dv.Ads != nil && *dv.Ads && !st.Ads {
			out = append(out, blockRule{"ads", map[string]any{"source": src, "domain": ads, "outboundTag": "block"}})
		}
		if dv.Ads != nil && !*dv.Ads && st.Ads {
			adsOff = append(adsOff, ip)
		}
	}
	if st.Ads {
		m := map[string]any{"domain": ads, "outboundTag": "block"}
		if len(adsOff) > 0 { // для исключённых устройств — пропустить рекламу дальше по правилам
			out = append(out, blockRule{"ads", map[string]any{"source": adsOff, "domain": ads, "outboundTag": "direct"}})
		}
		out = append(out, blockRule{"ads", m})
	}
	return out
}

func scheduleSig(st *rulesState, now time.Time) string {
	h := localHourAt(now)
	var b strings.Builder
	for ip, dv := range st.Devices {
		if len(dv.KidsBlock) > 0 && inWindow(h, dv.KidsFrom, dv.KidsTo) {
			b.WriteString(ip + ";")
		}
	}
	return b.String()
}

// ---- п. 20: подсказки заблокированных сайтов ----

type blockedHint struct {
	Host  string `json:"host"`
	N     int    `json:"n"`
	T     int64  `json:"t"`
	Svc   string `json:"svc,omitempty"`
	ViaOK bool   `json:"viaOK"`
}

var hints struct {
	sync.Mutex
	checked map[string]int64
	found   map[string]*blockedHint
}

var reAccDirect = regexp.MustCompile(`accepted (?:tcp|udp):([a-zA-Z0-9.\-]+):(\d+) .*-> direct\]`)

func tlsProbe(dial func(ctx context.Context, network, addr string) (net.Conn, error), host string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	c, err := dial(ctx, "tcp", net.JoinHostPort(host, "443"))
	if err != nil {
		return false
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(6 * time.Second))
	tc := tls.Client(c, &tls.Config{ServerName: host, InsecureSkipVerify: true})
	return tc.HandshakeContext(ctx) == nil
}

func (a *app) hintsLoop() {
	time.Sleep(10 * time.Minute)
	hints.Lock()
	hints.checked, hints.found = map[string]int64{}, map[string]*blockedHint{}
	hints.Unlock()
	for {
		guard.pace(10 * time.Minute)
		if guard.level() == 0 && !demoMode {
			a.hintsRound()
		}
		time.Sleep(30 * time.Minute)
	}
}

func (a *app) hintsRound() {
	acc, _ := xrayLogPaths(*flagConfDir)
	lines, err := tailFile(acc, 2<<20)
	if err != nil {
		return
	}
	cnt := map[string]int{}
	for _, l := range lines {
		if m := reAccDirect.FindStringSubmatch(l); m != nil && m[2] == "443" && net.ParseIP(m[1]) == nil && strings.Contains(m[1], ".") {
			cnt[strings.ToLower(m[1])]++
		}
	}
	st := loadRulesState()
	ign := map[string]bool{}
	for _, x := range st.Ignored {
		ign[x] = true
	}
	type kv struct {
		h string
		n int
	}
	var top []kv
	now := time.Now().Unix()
	hints.Lock()
	for h, n := range cnt {
		if ign[h] || n < 3 || now-hints.checked[h] < 6*3600 {
			continue
		}
		if id := matchSvc(h); id != "" {
			if s := svcByID(id); s != nil && s.Group == "novpn" {
				continue
			}
		}
		top = append(top, kv{h, n})
	}
	hints.Unlock()
	sort.Slice(top, func(i, j int) bool { return top[i].n > top[j].n })
	if len(top) > 12 {
		top = top[:12]
	}
	active := a.activeServer()
	var vpnDial func(ctx context.Context, network, addr string) (net.Conn, error)
	if active != "" {
		if ports, err := a.wprobe.ensure(a.reload(), a.watchProbeTags()); err == nil && ports[active] > 0 {
			addr := "127.0.0.1:" + strconv.Itoa(ports[active])
			vpnDial = func(ctx context.Context, _, t string) (net.Conn, error) { return socks5Dial(ctx, addr, t) }
		}
	}
	direct := (&net.Dialer{Timeout: 5 * time.Second}).DialContext
	for _, x := range top {
		ok := tlsProbe(direct, x.h)
		hints.Lock()
		hints.checked[x.h] = time.Now().Unix()
		hints.Unlock()
		if ok {
			hints.Lock()
			delete(hints.found, x.h)
			hints.Unlock()
			continue
		}
		viaOK := vpnDial != nil && tlsProbe(vpnDial, x.h)
		if !viaOK {
			continue // не открывается и через VPN — сайт просто лежит, не предлагаем
		}
		hints.Lock()
		hints.found[x.h] = &blockedHint{Host: x.h, N: x.n, T: time.Now().Unix(), Svc: matchSvc(x.h), ViaOK: true}
		hints.Unlock()
		time.Sleep(2 * time.Second)
	}
}

func (a *app) blockedSuggestions(st *rulesState) []*blockedHint {
	ign := map[string]bool{}
	for _, x := range st.Ignored {
		ign[x] = true
	}
	hints.Lock()
	defer hints.Unlock()
	var out []*blockedHint
	for h, x := range hints.found {
		if !ign[h] {
			out = append(out, x)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].N > out[j].N })
	return out
}
