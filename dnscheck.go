package main

// Проверка DNS: какие DNS-серверы настроены в Keenetic (DoH/DoT/обычные), отвечает
// ли каждый, и отвечает ли DNS самого роутера — тот, через который выходят все
// устройства. Если провайдер заблокировал DoH-серверы, «не работает VPN» на самом
// деле может быть «не работает DNS».

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// ---- DNS-пакет ----

func dnsQuery(name string) ([]byte, uint16) {
	id := uint16(rand.Intn(65535))
	b := make([]byte, 12)
	binary.BigEndian.PutUint16(b[0:], id)
	b[2] = 0x01 // RD
	binary.BigEndian.PutUint16(b[4:], 1)
	for _, l := range strings.Split(strings.TrimSuffix(name, "."), ".") {
		b = append(b, byte(len(l)))
		b = append(b, l...)
	}
	b = append(b, 0, 0, 1, 0, 1) // A, IN
	return b, id
}

func dnsSkipName(b []byte, i int) int {
	for i < len(b) {
		l := int(b[i])
		if l == 0 {
			return i + 1
		}
		if l&0xC0 == 0xC0 {
			return i + 2
		}
		i += l + 1
	}
	return i
}

// dnsParse — rcode и A-записи.
func dnsParse(b []byte, id uint16) (int, []string, error) {
	if len(b) < 12 {
		return 0, nil, errors.New("короткий ответ")
	}
	if binary.BigEndian.Uint16(b) != id {
		return 0, nil, errors.New("чужой ответ")
	}
	rcode := int(b[3] & 0x0F)
	qd, an := int(binary.BigEndian.Uint16(b[4:])), int(binary.BigEndian.Uint16(b[6:]))
	i := 12
	for k := 0; k < qd; k++ {
		i = dnsSkipName(b, i) + 4
	}
	var ips []string
	for k := 0; k < an && i < len(b); k++ {
		i = dnsSkipName(b, i)
		if i+10 > len(b) {
			break
		}
		typ := binary.BigEndian.Uint16(b[i:])
		rdl := int(binary.BigEndian.Uint16(b[i+8:]))
		i += 10
		if typ == 1 && rdl == 4 && i+4 <= len(b) {
			ips = append(ips, net.IP(b[i:i+4]).String())
		}
		i += rdl
	}
	return rcode, ips, nil
}

var rcodeName = map[int]string{1: "ошибка формата", 2: "сбой сервера (SERVFAIL)", 3: "имя не найдено (NXDOMAIN)", 5: "отказ (REFUSED)"}

// ---- способы запроса ----

func queryUDP(addr, name string, d *net.Dialer) ([]string, int64, error) {
	q, id := dnsQuery(name)
	t0 := time.Now()
	c, err := d.Dial("udp", addr)
	if err != nil {
		return nil, 0, err
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(4 * time.Second))
	c.Write(q)
	buf := make([]byte, 1500)
	n, err := c.Read(buf)
	if err != nil {
		return nil, 0, err
	}
	rc, ips, err := dnsParse(buf[:n], id)
	ms := time.Since(t0).Milliseconds()
	if err == nil && rc != 0 {
		err = errors.New(rcodeName[rc])
	}
	return ips, ms, err
}

func queryDoT(host, sni, name string, d *net.Dialer) ([]string, int64, error) {
	q, id := dnsQuery(name)
	t0 := time.Now()
	if !strings.Contains(host, ":") {
		host += ":853"
	}
	raw, err := bootstrapDial(d)(context.Background(), "tcp", host)
	if err != nil {
		return nil, 0, err
	}
	c := tls.Client(raw, &tls.Config{ServerName: sni})
	defer c.Close()
	c.SetDeadline(time.Now().Add(6 * time.Second))
	msg := binary.BigEndian.AppendUint16(nil, uint16(len(q)))
	if _, err := c.Write(append(msg, q...)); err != nil {
		return nil, 0, err
	}
	var l [2]byte
	if _, err := io.ReadFull(c, l[:]); err != nil {
		return nil, 0, err
	}
	buf := make([]byte, binary.BigEndian.Uint16(l[:]))
	if _, err := io.ReadFull(c, buf); err != nil {
		return nil, 0, err
	}
	rc, ips, err := dnsParse(buf, id)
	if err == nil && rc != 0 {
		err = errors.New(rcodeName[rc])
	}
	return ips, time.Since(t0).Milliseconds(), err
}

func queryDoH(u, name string, d *net.Dialer) ([]string, int64, error) {
	q, id := dnsQuery(name)
	binary.BigEndian.PutUint16(q, 0) // по RFC 8484 id = 0
	id = 0
	tr := &http.Transport{DialContext: bootstrapDial(d), TLSHandshakeTimeout: 6 * time.Second, DisableKeepAlives: true}
	defer tr.CloseIdleConnections()
	cl := &http.Client{Transport: tr, Timeout: 8 * time.Second}
	sep := "?"
	if strings.Contains(u, "?") {
		sep = "&"
	}
	t0 := time.Now()
	req, _ := http.NewRequest("GET", u+sep+"dns="+base64.RawURLEncoding.EncodeToString(q), nil)
	req.Header.Set("Accept", "application/dns-message")
	resp, err := cl.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != 200 {
		return nil, time.Since(t0).Milliseconds(), fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	rc, ips, err := dnsParse(body, id)
	if err == nil && rc != 0 {
		err = errors.New(rcodeName[rc])
	}
	return ips, time.Since(t0).Milliseconds(), err
}

// ---- что настроено в Keenetic ----

type dnsUpstream struct {
	Kind  string `json:"kind"`  // DoH | DoT | DNS
	Addr  string `json:"addr"`  // https://…, host, ip
	Local string `json:"local"` // локальный порт-прокси Keenetic (127.0.0.1:405xx), если есть
	Name  string `json:"name"`  // как показать
}

var reDoHLocal = regexp.MustCompile(`(127\.0\.0\.1:\d+)[^#\n]*#\s*(https://\S+?)(@\S+)?\s*$`)
var reDoTLocal = regexp.MustCompile(`(127\.0\.0\.1:\d+)[^#\n]*#\s*([\w.\-]+(?::853)?)(@\S+)?\s*$`)
var rePlain = regexp.MustCompile(`^\s*dns_server\s*=\s*([\d.]+(?::\d+)?)\b`)

func rciGet(path string) ([]byte, error) {
	cl := &http.Client{Timeout: 4 * time.Second}
	resp, err := cl.Get("http://127.0.0.1:79/rci/" + path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, 4<<20))
}

// keeneticDNS — DNS-серверы из «show dns-proxy» (proxy-config) и running-config.
func keeneticDNS() ([]dnsUpstream, string) {
	var out []dnsUpstream
	seen := map[string]bool{}
	add := func(u dnsUpstream) {
		k := u.Kind + "|" + u.Addr
		if !seen[k] {
			seen[k] = true
			out = append(out, u)
		}
	}
	src := ""
	if b, err := rciGet("show/dns-proxy"); err == nil {
		src = "dns-proxy"
		var root map[string]any
		json.Unmarshal(b, &root)
		var texts []string
		var walk func(v any)
		walk = func(v any) {
			switch x := v.(type) {
			case map[string]any:
				for k, y := range x {
					if s, ok := y.(string); ok && strings.Contains(k, "config") {
						texts = append(texts, s)
					}
					walk(y)
				}
			case []any:
				for _, y := range x {
					walk(y)
				}
			}
		}
		walk(root)
		for _, t := range texts {
			for _, l := range strings.Split(t, "\n") {
				if !strings.Contains(l, "dns_server") {
					continue
				}
				if m := reDoHLocal.FindStringSubmatch(l); m != nil {
					add(dnsUpstream{"DoH", m[2], m[1], hostOf(m[2])})
				} else if m := reDoTLocal.FindStringSubmatch(l); m != nil {
					add(dnsUpstream{"DoT", m[2], m[1], m[2]})
				} else if m := rePlain.FindStringSubmatch(l); m != nil && !strings.HasPrefix(m[1], "127.") {
					add(dnsUpstream{"DNS", m[1], "", m[1]})
				}
			}
		}
	}
	if len(out) == 0 { // запасной путь — running-config
		if b, err := rciGet("show/running-config"); err == nil {
			src = "running-config"
			var rc struct {
				Message []string `json:"message"`
			}
			json.Unmarshal(b, &rc)
			for _, l := range rc.Message {
				f := strings.Fields(l)
				switch {
				case len(f) >= 4 && f[0] == "dns-proxy" && f[1] == "https" && f[2] == "upstream":
					add(dnsUpstream{"DoH", f[3], "", hostOf(f[3])})
				case len(f) >= 4 && f[0] == "dns-proxy" && f[1] == "tls" && f[2] == "upstream":
					sni := f[3]
					if len(f) >= 6 && f[4] == "sni" {
						sni = f[5]
					}
					add(dnsUpstream{"DoT", f[3] + "@" + sni, "", sni})
				case len(f) >= 3 && f[0] == "ip" && f[1] == "name-server":
					add(dnsUpstream{"DNS", f[2], "", f[2]})
				}
			}
		}
	}
	return out, src
}

func hostOf(u string) string {
	if p, err := url.Parse(u); err == nil && p.Host != "" {
		return p.Host + strings.TrimSuffix(strings.TrimPrefix(p.Path, "/dns-query"), "/")
	}
	return u
}

// ---- проверка ----

type dnsResult struct {
	dnsUpstream
	OK    bool   `json:"ok"`
	Ms    int64  `json:"ms"`
	Err   string `json:"err,omitempty"`
	Block bool   `json:"block,omitempty"` // напрямую не отвечает, через VPN — да: режет провайдер
	VpnOK bool   `json:"vpnOK,omitempty"`
}

type dnsReport struct {
	T        int64              `json:"t"`
	Router   dnsResult          `json:"router"` // DNS роутера (то, что видят устройства)
	List     []dnsResult        `json:"list"`
	Source   string             `json:"source"`
	Verdict  string             `json:"verdict"`
	Level    string             `json:"level"` // ok | warn | bad
	TestName string             `json:"testName"`
	Hist     map[string][]dnsPt `json:"hist,omitempty"`  // 2.3: история за 24 ч по каждому серверу (ключ — addr, "router")
	Stats    map[string]dnsStat `json:"stats,omitempty"` // 0.9: доступность, сбои, отклик, итог
}

type dnsPt struct {
	T  int64 `json:"t"`
	OK bool  `json:"ok"`
	Ms int64 `json:"ms"`
}

var dnsHist = map[string][]dnsPt{} // под dnsLast

func dnsRecordLocked(r *dnsReport) {
	add := func(k string, x dnsResult) {
		l := append(dnsHist[k], dnsPt{r.T, x.OK, x.Ms})
		cut := r.T - 24*3600
		i := 0
		for i < len(l) && l[i].T < cut {
			i++
		}
		dnsHist[k] = l[i:]
	}
	add("router", r.Router)
	for _, x := range r.List {
		add(x.Addr, x)
	}
}

func dnsWithHistLocked(r *dnsReport) *dnsReport {
	cp := *r
	cp.Hist = map[string][]dnsPt{}
	cp.Stats = map[string]dnsStat{}
	for k, v := range dnsHist {
		cp.Hist[k] = append([]dnsPt(nil), v...)
		cp.Stats[k] = dnsStatLocked(k, tzOffCache)
	}
	return &cp
}

var dnsLast struct {
	sync.Mutex
	r       *dnsReport
	badFrom int64
	alerted bool
}

func (a *app) checkUpstream(u dnsUpstream, name string, d *net.Dialer) dnsResult {
	r := dnsResult{dnsUpstream: u}
	var err error
	switch u.Kind {
	case "DoH":
		_, r.Ms, err = queryDoH(u.Addr, name, d)
	case "DoT":
		host, sni, _ := strings.Cut(u.Addr, "@")
		if sni == "" {
			sni = strings.Split(host, ":")[0]
		}
		_, r.Ms, err = queryDoT(host, sni, name, d)
	default:
		addr := u.Addr
		if !strings.Contains(addr, ":") {
			addr += ":53"
		}
		_, r.Ms, err = queryUDP(addr, name, d)
	}
	if err != nil {
		r.Err = explainNetErr(err)
	} else {
		r.OK = true
	}
	return r
}

func (a *app) dnsCheck() *dnsReport {
	const name = "www.instagram.com"
	rep := &dnsReport{T: time.Now().Unix(), TestName: name}
	direct := &net.Dialer{Timeout: 5 * time.Second}

	// 1) DNS роутера — тот, которым пользуются устройства
	routerAddr := "127.0.0.1:53"
	if h, _, err := net.SplitHostPort(*flagListen); err == nil && h != "" && h != "0.0.0.0" {
		routerAddr = net.JoinHostPort(h, "53")
	}
	ips, ms, err := queryUDP(routerAddr, name, direct)
	rep.Router = dnsResult{dnsUpstream: dnsUpstream{Kind: "DNS", Addr: routerAddr, Name: "DNS роутера (для всех устройств)"}, Ms: ms}
	if err != nil {
		rep.Router.Err = explainNetErr(err)
	} else if len(ips) == 0 {
		rep.Router.Err = "пустой ответ"
	} else {
		rep.Router.OK = true
	}

	// 2) каждый сервер из настроек Keenetic: напрямую (как ходит сам роутер)
	ups, src := keeneticDNS()
	rep.Source = src
	var wg sync.WaitGroup
	res := make([]dnsResult, len(ups))
	for i, u := range ups {
		wg.Add(1)
		go func(i int, u dnsUpstream) {
			defer wg.Done()
			res[i] = a.checkUpstream(u, name, direct)
			// локальный прокси Keenetic (127.0.0.1:405xx) — проверить и его
			if !res[i].OK && u.Local != "" {
				if _, ms, err := queryUDP(u.Local, name, direct); err == nil {
					res[i].OK, res[i].Ms, res[i].Err = true, ms, ""
				}
			}
		}(i, u)
	}
	wg.Wait()

	// 3) не ответившие напрямую — через VPN: если там отвечают, значит, режет провайдер
	var vpnPort int
	c := a.reload()
	a.mu.Lock()
	active := ""
	for _, g := range c.groups {
		if v := a.desired[g.Tag]; v != "" && !isReserveTag(v) {
			active = v
		}
	}
	a.mu.Unlock()
	needVPN := false
	for _, r := range res {
		if !r.OK && r.Kind != "DNS" {
			needVPN = true
		}
	}
	if needVPN && active != "" {
		if ports, err := a.wprobe.ensure(c, a.watchProbeTags()); err == nil {
			vpnPort = ports[active]
		}
	}
	if vpnPort > 0 {
		viaVPN := &net.Dialer{Timeout: 6 * time.Second}
		proxy := fmt.Sprintf("127.0.0.1:%d", vpnPort)
		for i := range res {
			if res[i].OK || res[i].Kind == "DNS" {
				continue
			}
			r := res[i]
			var err error
			switch r.Kind {
			case "DoH":
				tr := &http.Transport{DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) { return socks5Dial(ctx, proxy, addr) }, DisableKeepAlives: true}
				cl := &http.Client{Transport: tr, Timeout: 8 * time.Second}
				q, _ := dnsQuery(name)
				binary.BigEndian.PutUint16(q, 0)
				sep := "?"
				if strings.Contains(r.Addr, "?") {
					sep = "&"
				}
				req, _ := http.NewRequest("GET", r.Addr+sep+"dns="+base64.RawURLEncoding.EncodeToString(q), nil)
				req.Header.Set("Accept", "application/dns-message")
				var resp *http.Response
				resp, err = cl.Do(req)
				if err == nil {
					resp.Body.Close()
					if resp.StatusCode != 200 {
						err = fmt.Errorf("HTTP %d", resp.StatusCode)
					}
				}
				tr.CloseIdleConnections()
			case "DoT":
				host, _, _ := strings.Cut(r.Addr, "@")
				if !strings.Contains(host, ":") {
					host += ":853"
				}
				ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
				var cn net.Conn
				cn, err = socks5Dial(ctx, proxy, host)
				cancel()
				if err == nil {
					cn.Close()
				}
			}
			_ = viaVPN
			if err == nil {
				res[i].VpnOK, res[i].Block = true, true
			}
		}
	}
	rep.List = res

	// 4) вывод
	okN, blocked := 0, 0
	for _, r := range res {
		if r.OK {
			okN++
		}
		if r.Block {
			blocked++
		}
	}
	switch {
	case !rep.Router.OK && len(res) > 0 && okN == 0:
		rep.Level, rep.Verdict = "bad", "DNS роутера не отвечает: ни один из настроенных DNS-серверов недоступен. У устройств «не работает интернет», хотя VPN может быть в порядке."
		if blocked > 0 {
			rep.Verdict += fmt.Sprintf(" Провайдер, похоже, блокирует %d из них (через VPN они отвечают).", blocked)
		}
	case !rep.Router.OK:
		rep.Level, rep.Verdict = "bad", "DNS роутера не отвечает — у устройств не будут открываться сайты. Проверьте настройки DNS в Keenetic."
	case len(res) > 0 && okN < len(res):
		rep.Level = "warn"
		rep.Verdict = fmt.Sprintf("DNS работает, но %d из %d серверов не отвечают", len(res)-okN, len(res))
		if blocked > 0 {
			rep.Verdict += " (провайдер их режет — через VPN отвечают)"
		}
		rep.Verdict += ". Роутер пользуется оставшимися."
	default:
		rep.Level, rep.Verdict = "ok", "DNS в порядке"
	}
	sort.SliceStable(rep.List, func(i, j int) bool { return rep.List[i].OK && !rep.List[j].OK })
	return rep
}

// dnsLoop — проверка раз в 5 минут; если DNS роутера не отвечает 10+ минут — событие.
func (a *app) dnsLoop() {
	loadDNSHourly()
	time.Sleep(90 * time.Second)
	for {
		r := a.dnsCheck()
		dnsLast.Lock()
		prev := dnsLast.r
		dnsLast.r = r
		dnsRecordLocked(r)
		now := time.Now().Unix()
		var ev, kind string
		if r.Level == "bad" {
			if dnsLast.badFrom == 0 {
				dnsLast.badFrom = now
			}
			if !dnsLast.alerted && now-dnsLast.badFrom >= 600 {
				dnsLast.alerted = true
				ev, kind = "DNS: "+r.Verdict, "warn"
			}
		} else {
			if dnsLast.alerted {
				ev, kind = "DNS снова работает", "recover"
			}
			dnsLast.badFrom, dnsLast.alerted = 0, false
		}
		_ = prev
		// 0.9: по каждому серверу — только после 3 неудач подряд, и «снова отвечает»
		evs := dnsAccountLocked(r)
		dnsLast.Unlock()
		if ev != "" || len(evs) > 0 {
			a.watch.mu.Lock()
			if ev != "" {
				a.watch.addEvent("", kind, ev)
			}
			for _, e := range evs {
				a.watch.addEventShort("", e[2], e[0], e[1])
			}
			a.watch.mu.Unlock()
		}
		time.Sleep(5 * time.Minute)
		guard.pace(5 * time.Minute)
	}
}

func (a *app) handleDNS(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" || r.URL.Query().Get("now") == "1" {
		rep := a.dnsCheck()
		dnsLast.Lock()
		dnsLast.r = rep
		dnsRecordLocked(rep)
		out := dnsWithHistLocked(rep)
		dnsLast.Unlock()
		writeJSON(w, out)
		return
	}
	dnsLast.Lock()
	rep := dnsLast.r
	if rep != nil {
		rep = dnsWithHistLocked(rep)
	}
	dnsLast.Unlock()
	if rep == nil {
		writeJSON(w, map[string]any{"pending": true})
		return
	}
	writeJSON(w, rep)
}

var _ = bytes.MinRead

// bootstrapDial — соединение по имени: сначала DNS роутера, а если он не отвечает —
// напрямую Яндекс/Cloudflare DNS (иначе неработающий DNS роутера выглядел бы как
// заблокированный DoH-сервер).
func bootstrapDial(d *net.Dialer) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil || net.ParseIP(host) != nil {
			return d.DialContext(ctx, network, addr)
		}
		c2, cancel := context.WithTimeout(ctx, 3*time.Second)
		ips, err := net.DefaultResolver.LookupHost(c2, host)
		cancel()
		if err != nil || len(ips) == 0 {
			for _, ns := range []string{"77.88.8.8:53", "1.1.1.1:53", "8.8.8.8:53"} {
				if l, _, e := queryUDP(ns, host, d); e == nil && len(l) > 0 {
					ips = l
					break
				}
			}
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("не удалось узнать адрес %s", host)
		}
		return d.DialContext(ctx, network, net.JoinHostPort(ips[0], port))
	}
}
