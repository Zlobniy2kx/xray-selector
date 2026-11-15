package main

// Трафик и устройства.
//  • По серверам — точные байты из статистики Xray (StatsService, outbound>>>TAG>>>traffic).
//  • По устройствам — счётчики байтов соединений роутера (nf_conntrack с учётом байтов),
//    сопоставленные с журналом Xray по «IP:порт» источника → через какой сервер шло.
//  • Устройство = MAC из таблицы соседей (ARP), имена задаёт человек в панели.
//  • «Интернет провайдера» — раз в минуту прямой (не через VPN) запрос к ya.ru.

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type devInfo struct {
	Name      string `json:"name,omitempty"`
	IP        string `json:"ip"`
	Vendor    string `json:"vendor,omitempty"`
	FirstSeen int64  `json:"firstSeen"`
	LastSeen  int64  `json:"lastSeen"`
	MergeInto string `json:"mergeInto,omitempty"` // «это то же устройство, что…»
	Kind      string `json:"kind,omitempty"`      // тип (значок), задан вручную
	KName     string `json:"kname,omitempty"`     // имя из списка клиентов Keenetic
	Host      string `json:"host,omitempty"`      // hostname, который сообщило устройство
	Policy    string `json:"policy,omitempty"`    // политика доступа в Keenetic
	Conn      string `json:"conn,omitempty"`      // как подключено (кабель / Wi-Fi, сигнал)
}

type bytesPair struct {
	Up    int64 `json:"up"`
	Down  int64 `json:"down"`
	Conns int64 `json:"conns"`
}

func (b *bytesPair) add(o bytesPair) { b.Up += o.Up; b.Down += o.Down; b.Conns += o.Conns }

type dayStats struct {
	Servers  map[string]*bytesPair            `json:"servers"`  // по точной статистике Xray
	Devices  map[string]*bytesPair            `json:"devices"`  // mac → всё через Xray
	DevVPN   map[string]*bytesPair            `json:"devVPN"`   // mac → только через VPN
	DevSrv   map[string]map[string]*bytesPair `json:"devSrv"`   // mac → сервер → …
	DevSvc   map[string]map[string]*bytesPair `json:"devSvc"`   // mac → сервис (только VPN) → …
	Services map[string]*bytesPair            `json:"services"` // Instagram → …
	SrvConns map[string]int64                 `json:"srvConns"` // соединений через сервер
	Hourly   [24]bytesPair                    `json:"hourly"`   // VPN-трафик по часам
}

func newDay() *dayStats {
	return &dayStats{Servers: map[string]*bytesPair{}, Devices: map[string]*bytesPair{}, DevVPN: map[string]*bytesPair{},
		DevSrv: map[string]map[string]*bytesPair{}, DevSvc: map[string]map[string]*bytesPair{}, Services: map[string]*bytesPair{}, SrvConns: map[string]int64{}}
}

type connRef struct {
	out, host string
	t         int64
}

type ispPoint struct {
	T  int64 `json:"t"`
	OK bool  `json:"ok"`
	Ms int32 `json:"ms,omitempty"`
}

type traffic struct {
	mu        sync.Mutex
	days      map[string]*dayStats // "2026-11-08"
	devices   map[string]*devInfo  // mac → …
	arp       map[string]string    // ip → mac
	conns     map[string]connRef   // "ip:port" → через какой сервер
	ct        map[string][2]int64  // ключ conntrack → прошлые байты
	statsPrev map[string]int64     // имя счётчика Xray → прошлое значение
	isp       []ispPoint
	ispBy     map[string][]ispPoint
	wans      []wanInfo
	ctOK      bool   // счётчики соединений доступны
	ctErr     string // почему нет
	statsOK   bool
	lastSave  int64
	seeded    bool
	recent    []flowRec // соединения за последние 15 минут — для «карты»
}

type flowRec struct {
	t             int64
	dev, out, svc string
}

func trafficPath() string {
	return filepath.Join(filepath.Dir(*flagState), "xray-selector-traffic.json")
}

func newTraffic() *traffic {
	return &traffic{days: map[string]*dayStats{}, devices: map[string]*devInfo{}, arp: map[string]string{},
		conns: map[string]connRef{}, ct: map[string][2]int64{}, statsPrev: map[string]int64{}}
}

func (a *app) dayKey(t time.Time) string {
	a.watch.mu.Lock()
	off := a.watch.settings.TzOffsetMin
	a.watch.mu.Unlock()
	return t.In(time.FixedZone("", off*60)).Format("2006-01-02")
}

func (a *app) localHour(t time.Time) int {
	a.watch.mu.Lock()
	off := a.watch.settings.TzOffsetMin
	a.watch.mu.Unlock()
	return t.In(time.FixedZone("", off*60)).Hour()
}

func (tr *traffic) dayLocked(k string) *dayStats {
	d := tr.days[k]
	if d == nil {
		d = newDay()
		tr.days[k] = d
		// храним 31 день
		var ks []string
		for x := range tr.days {
			ks = append(ks, x)
		}
		sort.Strings(ks)
		for len(ks) > 31 {
			delete(tr.days, ks[0])
			ks = ks[1:]
		}
	}
	return d
}

func bp(m map[string]*bytesPair, k string) *bytesPair {
	p := m[k]
	if p == nil {
		p = &bytesPair{}
		m[k] = p
	}
	return p
}

// ---- сервисы по домену ----

var serviceRules = []struct{ name, subs string }{
	{"Instagram", "instagram,cdninstagram"},
	{"Facebook", "facebook,fbcdn,fbsbx,fb.com,fb.me"},
	{"WhatsApp", "whatsapp,wa.me"},
	{"Telegram", "telegram,t.me,telesco.pe,tdesktop,91.108.,149.154.,95.161.,91.105."},
	{"YouTube", "youtube,googlevideo,ytimg,youtu.be,yt3.ggpht"},
	{"ChatGPT", "openai,chatgpt,oaistatic,oaiusercontent"},
	{"Claude", "anthropic,claude"},
	{"Gemini", "gemini,aistudio,notebooklm,generativelanguage"},
	{"TikTok", "tiktok,byteoversea,ibyteimg,ttwstatic,muscdn"},
	{"X (Twitter)", "twitter,twimg,x.com"},
	{"Discord", "discord"},
	{"Spotify", "spotify,scdn.co"},
	{"Netflix", "netflix,nflx"},
	{"Steam", "steam"},
	{"Roblox", "roblox,rbxcdn"},
	{"Twitch", "twitch,jtvnw,ttvnw"},
	{"Reddit", "reddit,redd.it,redditmedia,redditstatic"},
	{"LinkedIn", "linkedin,licdn"},
	{"Pinterest", "pinterest,pinimg"},
	{"Viber", "viber"},
	{"Signal", "signal.org,whispersystems"},
	{"Snapchat", "snapchat,sc-cdn,snap-dev"},
	{"Google", "google,gstatic,ggpht,gvt1,gvt2,gvt3,doubleclick,googlesyndication,app-measurement,firebase,crashlytics,android"},
	{"Apple", "apple,icloud,mzstatic,aaplimg,cdn-apple,itunes"},
	{"Microsoft", "microsoft,msft,live.com,windows,office,xbox,azure,bing,msn,skype,onedrive,sharepoint,outlook"},
	{"Amazon", "amazon,aws,cloudfront,media-amazon"},
	{"NVIDIA", "nvidia,geforce"},
	{"Cloudflare", "cloudflare,cf-ns"},
	{"Speedtest", "speedtest,ookla,fast.com"},
}

// baseDomain — «главный» домен: cdn.foo.example.com → example.com, a.b.co.uk → b.co.uk.
func baseDomain(host string) string {
	h := strings.TrimSuffix(strings.ToLower(host), ".")
	if h == "" {
		return ""
	}
	if net.ParseIP(strings.Trim(h, "[]")) != nil {
		return "по IP-адресу"
	}
	parts := strings.Split(h, ".")
	if len(parts) <= 2 {
		return h
	}
	n := 2
	switch parts[len(parts)-2] {
	case "co", "com", "net", "org", "gov", "edu", "ac", "gob", "or", "ne":
		n = 3
	}
	if n > len(parts) {
		n = len(parts)
	}
	return strings.Join(parts[len(parts)-n:], ".")
}

// svcKey — ключ для статистики сервисов: известный сервис или домен сайта.
// Чтобы файл не разрастался, после limit разных ключей остальное идёт в «Другое».
func svcKey(m map[string]*bytesPair, host string, limit int) string {
	if s := serviceName(host); s != "Другое" {
		return s
	}
	d := baseDomain(host)
	if d == "" {
		return "Другое"
	}
	if _, ok := m[d]; ok || len(m) < limit {
		return d
	}
	return "Другое"
}

func serviceOf(host string) string {
	h := strings.ToLower(host)
	for _, r := range serviceRules {
		for _, s := range strings.Split(r.subs, ",") {
			if strings.Contains(h, s) {
				return r.name
			}
		}
	}
	return "Другое"
}

// ---- производитель по MAC ----

var ouiVendors = map[string]string{
	"50:ff:20": "Keenetic", "a8:5e:45": "ASUS", "04:d9:f5": "ASUS", "2c:fd:a1": "ASUS", "f0:2f:74": "ASUS", "70:85:c2": "ASRock",
	"3c:22:fb": "Apple", "f0:18:98": "Apple", "a4:83:e7": "Apple", "dc:a9:04": "Apple", "88:66:a5": "Apple", "bc:d0:74": "Apple",
	"28:6c:07": "Xiaomi", "64:09:80": "Xiaomi", "50:ec:50": "Xiaomi", "54:ef:44": "Lumi/Aqara", "00:1e:06": "Wibrain",
	"ac:5f:ea": "OnePlus", "00:04:4b": "Nvidia", "48:b0:2d": "Nvidia", "78:b6:ec": "Sony", "00:d9:d1": "Sony", "70:9e:29": "Sony PlayStation",
	"a8:23:fe": "LG", "c8:08:e9": "LG", "00:e0:4c": "Realtek", "b0:be:76": "TP-Link", "00:1a:11": "Google", "f4:f5:d8": "Google",
	"cc:50:e3": "Espressif", "24:0a:c4": "Espressif", "08:3a:f2": "Espressif", "18:fe:34": "Espressif", "5c:e5:0c": "Beijing Xiaomi",
	"00:1b:63": "Apple", "e4:5f:01": "Raspberry Pi", "dc:a6:32": "Raspberry Pi", "00:26:ab": "Seiko Epson", "00:00:48": "Epson",
	"00:1e:8f": "Canon", "00:bb:3a": "Amazon", "44:65:0d": "Amazon", "8c:79:f5": "Samsung", "5c:49:7d": "Samsung", "00:e0:20": "TECNOMEN",
}

func vendorOf(mac string) string {
	if len(mac) < 8 {
		return ""
	}
	if v, ok := ouiVendors[strings.ToLower(mac[:8])]; ok {
		return v
	}
	// второй символ 2/6/a/e — «локально назначенный» адрес: частный адрес телефона (iPhone/Android)
	if c := strings.ToLower(mac[1:2]); c == "2" || c == "6" || c == "a" || c == "e" {
		return "частный адрес (телефон/планшет)"
	}
	return ""
}

func readARP() map[string]string {
	out := map[string]string{}
	f, err := os.Open("/proc/net/arp")
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Scan()
	for sc.Scan() {
		fs := strings.Fields(sc.Text())
		if len(fs) >= 4 && fs[3] != "00:00:00:00:00:00" {
			out[fs[0]] = strings.ToLower(fs[3])
		}
	}
	return out
}

// devKeyLocked — ключ устройства по IP: MAC (с учётом склейки) или сам IP.
func (tr *traffic) devKeyLocked(ip string, now int64) string {
	mac := tr.arp[ip]
	key := ip
	if mac != "" {
		key = mac
	}
	d := tr.devices[key]
	if d == nil {
		d = &devInfo{IP: ip, FirstSeen: now, Vendor: vendorOf(mac)}
		tr.devices[key] = d
	}
	d.IP, d.LastSeen = ip, now
	for i := 0; i < 5 && d.MergeInto != ""; i++ { // склеенные устройства
		if t := tr.devices[d.MergeInto]; t != nil {
			key, d = d.MergeInto, t
			d.LastSeen = now
		} else {
			break
		}
	}
	return key
}

// ---- журнал соединений Xray ----

func parseAccess(l string) (ip, port, host, out string, ok bool) {
	i := strings.Index(l, " from ")
	j := strings.Index(l, " accepted ")
	k := strings.LastIndex(l, " -> ")
	if i < 0 || j < i || k < j || !strings.HasSuffix(l, "]") {
		return
	}
	src := l[i+6 : j]
	if c := strings.LastIndex(src, ":"); c > 0 {
		ip, port = src[:c], src[c+1:]
	} else {
		return
	}
	dst := l[j+10:]
	if s := strings.Index(dst, " ["); s > 0 {
		dst = dst[:s]
	}
	if c := strings.Index(dst, ":"); c >= 0 { // tcp:host:port
		dst = dst[c+1:]
	}
	if c := strings.LastIndex(dst, ":"); c > 0 {
		dst = dst[:c]
	}
	return ip, port, dst, strings.TrimSpace(l[k+4 : len(l)-1]), true
}

func (a *app) accessFollowLoop() {
	var off int64 = -1
	var path string
	var rest string
	for {
		time.Sleep(3 * time.Second)
		acc, _ := xrayLogPaths(*flagConfDir)
		if acc != path {
			path, off, rest = acc, -1, ""
		}
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		st, _ := f.Stat()
		if off < 0 || st.Size() < off { // старт или журнал очищен/перевёрнут
			off = st.Size()
			if off > 64*1024 {
				off -= 64 * 1024 // подхватить хвост, чтобы сразу было что показать
			} else {
				off = 0
			}
		}
		f.Seek(off, io.SeekStart)
		b, _ := io.ReadAll(io.LimitReader(f, 4<<20))
		f.Close()
		off += int64(len(b))
		chunk := rest + string(b)
		lines := strings.Split(chunk, "\n")
		rest = lines[len(lines)-1]
		lines = lines[:len(lines)-1]
		if len(lines) == 0 {
			continue
		}
		now := time.Now()
		dk, hr := a.dayKey(now), a.localHour(now)
		_ = hr
		tr := a.trf
		tr.mu.Lock()
		day := tr.dayLocked(dk)
		for _, l := range lines {
			ip, port, host, out, ok := parseAccess(l)
			if !ok || !strings.Contains(l, " accepted ") {
				continue
			}
			if !isLANAddr(ip) {
				continue // 0.9: внешний адрес (сам роутер через провайдера, входящие) — не устройство
			}
			key := tr.devKeyLocked(ip, now.Unix())
			tr.conns[ip+":"+port] = connRef{out, host, now.Unix()}
			svc := serviceName(host)
			if out == "direct" {
				svc = "напрямую"
			}
			if out != "block" {
				tr.recent = append(tr.recent, flowRec{now.Unix(), key, out, svc})
			}
			bp(day.Devices, key).Conns++
			if out != "direct" && out != "block" {
				bp(day.DevVPN, key).Conns++
				if day.DevSrv[key] == nil {
					day.DevSrv[key] = map[string]*bytesPair{}
				}
				bp(day.DevSrv[key], out).Conns++
				day.SrvConns[out]++
				bp(day.Services, svcKey(day.Services, host, 150)).Conns++
				if day.DevSvc == nil {
					day.DevSvc = map[string]map[string]*bytesPair{}
				}
				if day.DevSvc[key] == nil {
					day.DevSvc[key] = map[string]*bytesPair{}
				}
				bp(day.DevSvc[key], svcKey(day.DevSvc[key], host, 50)).Conns++
			}
		}
		cut := now.Unix() - 900
		i := 0
		for i < len(tr.recent) && tr.recent[i].t < cut {
			i++
		}
		tr.recent = tr.recent[i:]
		if len(tr.recent) > 50000 {
			tr.recent = tr.recent[len(tr.recent)-50000:]
		}
		if len(tr.conns) > 30000 { // подчистить старые
			for k, v := range tr.conns {
				if now.Unix()-v.t > 900 {
					delete(tr.conns, k)
				}
			}
		}
		tr.mu.Unlock()
	}
}

// ---- счётчики байтов соединений (conntrack) ----

func enableConntrackAcct() error {
	if demoMode {
		return nil
	}
	p := "/proc/sys/net/netfilter/nf_conntrack_acct"
	b, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(b)) == "1" {
		return nil
	}
	if err := os.WriteFile(p, []byte("1"), 0644); err != nil {
		return err
	}
	log.Printf("включён учёт байтов соединений (nf_conntrack_acct=1) — для трафика по устройствам")
	return nil
}

// порты UDP, которые не означают игру/звонок
var commonUDP = map[int]bool{53: true, 67: true, 68: true, 123: true, 137: true, 138: true, 443: true, 853: true, 1900: true, 5353: true}

// realtimeActive — идёт ли сейчас игра или звонок через VPN (за последние ~40 с).
// Без блокировок (atomic): вызывается из сторожа под w.mu, а conntrackLoop держит tr.mu и берёт w.mu.
var rtUntil atomic.Int64
var rtWho atomic.Value

func (a *app) realtimeActive() (bool, string) {
	if time.Now().Unix() < rtUntil.Load() {
		w, _ := rtWho.Load().(string)
		return true, w
	}
	return false, ""
}

func (a *app) conntrackLoop() {
	tr := a.trf
	if err := enableConntrackAcct(); err != nil {
		tr.mu.Lock()
		tr.ctErr = "не удалось включить учёт байтов: " + err.Error()
		tr.mu.Unlock()
	}
	for {
		time.Sleep(5 * time.Second)
		f, err := os.Open("/proc/net/nf_conntrack")
		if err != nil {
			f, err = os.Open("/proc/net/ip_conntrack")
		}
		if err != nil {
			tr.mu.Lock()
			tr.ctOK, tr.ctErr = false, "таблица соединений недоступна"
			tr.mu.Unlock()
			continue
		}
		type ent struct {
			src, sport string
			proto      string
			dport      int
			up, down   int64
			key        string
		}
		var ents []ent
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64*1024), 1<<20)
		for sc.Scan() {
			l := sc.Text()
			if !strings.Contains(l, "src=192.168.") || !strings.Contains(l, "bytes=") {
				continue
			}
			fs := strings.Fields(l)
			var e ent
			seen := map[string]int{}
			var proto string
			if len(fs) > 2 {
				proto = fs[2]
			}
			for _, x := range fs {
				kv := strings.SplitN(x, "=", 2)
				if len(kv) != 2 {
					continue
				}
				seen[kv[0]]++
				n := seen[kv[0]]
				switch {
				case kv[0] == "src" && n == 1:
					e.src = kv[1]
				case kv[0] == "sport" && n == 1:
					e.sport = kv[1]
				case kv[0] == "bytes" && n == 1:
					e.up, _ = strconv.ParseInt(kv[1], 10, 64)
				case kv[0] == "bytes" && n == 2:
					e.down, _ = strconv.ParseInt(kv[1], 10, 64)
				case kv[0] == "dst" && n == 1, kv[0] == "dport" && n == 1:
					e.key += kv[1] + "|"
					if kv[0] == "dport" {
						e.dport, _ = strconv.Atoi(kv[1])
					}
				}
			}
			if !strings.HasPrefix(e.src, "192.168.") {
				continue
			}
			e.proto = proto
			e.key = proto + "|" + e.src + "|" + e.sport + "|" + e.key
			ents = append(ents, e)
		}
		f.Close()
		now := time.Now()
		dk, hr := a.dayKey(now), a.localHour(now)
		tr.mu.Lock()
		tr.ctOK, tr.ctErr = true, ""
		day := tr.dayLocked(dk)
		alive := make(map[string][2]int64, len(ents))
		for _, e := range ents {
			ref, viaXray := tr.conns[e.src+":"+e.sport]
			if !viaXray {
				continue // это соединение шло мимо XKeen
			}
			prev := tr.ct[e.key]
			du, dd := e.up-prev[0], e.down-prev[1]
			if du < 0 || dd < 0 {
				du, dd = e.up, e.down
			}
			alive[e.key] = [2]int64{e.up, e.down}
			if du == 0 && dd == 0 {
				continue
			}
			// п. 21: игра или звонок через VPN — постоянный UDP-поток (не DNS/QUIC)
			if e.proto == "udp" && ref.out != "direct" && ref.out != "block" && du+dd >= 5000 && !commonUDP[e.dport] && prev != ([2]int64{}) {
				rtUntil.Store(now.Unix() + 40)
				rtWho.Store(e.src + ":" + strconv.Itoa(e.dport))
			}
			key := tr.devKeyLocked(e.src, now.Unix())
			d := bytesPair{Up: du, Down: dd}
			bp(day.Devices, key).add(d)
			if ref.out != "direct" && ref.out != "block" {
				bp(day.DevVPN, key).add(d)
				if day.DevSrv[key] == nil {
					day.DevSrv[key] = map[string]*bytesPair{}
				}
				bp(day.DevSrv[key], ref.out).add(d)
				bp(day.Services, svcKey(day.Services, ref.host, 150)).add(d)
				if day.DevSvc == nil {
					day.DevSvc = map[string]map[string]*bytesPair{}
				}
				if day.DevSvc[key] == nil {
					day.DevSvc[key] = map[string]*bytesPair{}
				}
				bp(day.DevSvc[key], svcKey(day.DevSvc[key], ref.host, 50)).add(d)
				day.Hourly[hr].add(d)
			}
		}
		tr.ct = alive
		tr.mu.Unlock()
	}
}

// ---- статистика Xray по серверам ----

func (x *xrayAPI) QueryStats(ctx context.Context, pattern string) (map[string]int64, error) {
	req := pbString(nil, 1, pattern)
	resp, err := x.call(ctx, "/xray.app.stats.command.StatsService/QueryStats", req)
	if err != nil {
		return nil, err
	}
	out := map[string]int64{}
	top, err := pbParse(resp)
	if err != nil {
		return nil, err
	}
	for _, f := range top {
		if f.num != 1 {
			continue
		}
		st, _ := pbParse(f.bytes)
		var name string
		var val int64
		for _, s := range st {
			if s.num == 1 {
				name = string(s.bytes)
			} else if s.num == 2 {
				val = int64(s.u)
			}
		}
		if name != "" {
			out[name] = val
		}
	}
	return out, nil
}

func (a *app) xrayStatsLoop() {
	tr := a.trf
	for {
		time.Sleep(10 * time.Second)
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		st, err := a.api.QueryStats(ctx, "outbound>>>")
		cancel()
		tr.mu.Lock()
		tr.statsOK = err == nil && len(st) > 0
		if !tr.statsOK {
			tr.mu.Unlock()
			continue
		}
		day := tr.dayLocked(a.dayKey(time.Now()))
		for name, v := range st {
			p := strings.Split(name, ">>>") // outbound>>>TAG>>>traffic>>>uplink
			if len(p) != 4 {
				continue
			}
			prev, had := tr.statsPrev[name]
			tr.statsPrev[name] = v
			if !had {
				continue
			}
			d := v - prev
			if d < 0 { // Xray перезапускался — счётчики обнулились
				d = v
			}
			if d == 0 {
				continue
			}
			b := bp(day.Servers, p[1])
			if p[3] == "uplink" {
				b.Up += d
			} else {
				b.Down += d
			}
		}
		tr.mu.Unlock()
	}
}

// ---- интернет провайдера (напрямую, не через VPN) ----

// ispLoop — «пинг» каждого провайдера: время TCP-соединения с Яндексом через
// интерфейс провайдера (SO_BINDTODEVICE), раз в минуту. Общая лента tr.isp — по
// провайдеру, через которого сейчас идёт интернет.
type wanInfo struct {
	Name   string `json:"name"`   // системное имя (eth3, ppp0)
	ID     string `json:"id"`     // имя в Keenetic
	Desc   string `json:"desc"`   // описание, заданное в Keenetic (имя провайдера)
	Prio   int    `json:"prio"`   // приоритет подключения
	Active bool   `json:"active"` // через него сейчас маршрут по умолчанию
	IP     string `json:"ip,omitempty"`
	Up     bool   `json:"up"`
	Role   string `json:"role"` // основной | резерв
}

// ifaceByIP — IPv4 → системное имя интерфейса.
func ifaceByIP() map[string]string {
	out := map[string]string{}
	ifs, _ := net.Interfaces()
	for _, i := range ifs {
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil {
				out[n.IP.String()] = i.Name
			}
		}
	}
	return out
}

func wanList(defIface string) []wanInfo {
	var out []wanInfo
	if b, err := rciGet("show/interface"); err == nil {
		var m map[string]map[string]any
		if json.Unmarshal(b, &m) == nil {
			for id, v := range m {
				str := func(k string) string { x, _ := v[k].(string); return x }
				pr, hasPr := v["priority"].(float64)
				glob, _ := v["global"].(bool)
				if gs := str("global"); gs == "yes" || gs == "true" {
					glob = true
				}
				// провайдер: подключение «для интернета» (global). Туннель (WireGuard/OpenVPN…) без global —
				// это VPN, а не провайдер; L2TP/PPTP/PPPoE провайдера идут с global (п. 2).
				isTun := tunnelKind(str("type")) != "" || tunnelKind(id) != ""
				if (!hasPr && !glob) || isTun { // 0.9: WireGuard/прокси — это VPN, их показываем отдельно
					continue
				}
				// п. 1: системный интерфейс — по IP-адресу подключения (надёжнее имени)
				sys := ""
				if ip := str("address"); ip != "" {
					sys = ifaceByIP()[ip]
				}
				if sys == "" {
					sys = str("interface-name")
				}
				if sys == "" {
					continue
				}
				if _, err := os.Stat("/sys/class/net/" + sys); err != nil {
					continue
				}
				out = append(out, wanInfo{Name: sys, ID: id, Desc: str("description"), Prio: int(pr), IP: str("address"), Up: str("connected") == "yes" || str("link") == "up"})
			}
		}
	}
	if len(out) == 0 && defIface != "" {
		out = []wanInfo{{Name: defIface, ID: defIface}}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Prio > out[j].Prio })
	for i := range out {
		out[i].Active = out[i].Name == defIface
		out[i].Role = "резерв"
		if i == 0 {
			out[i].Role = "основной"
		}
	}
	return out
}

var ispTargets struct {
	sync.Mutex
	list []string
	t    int64
}

// ispHosts — адреса крупных сайтов на порту 443 (порт 53 роутер может перехватывать сам
// и отвечать мгновенно — пинг «1 мс» ничего не значит).
func ispHosts() []string {
	ispTargets.Lock()
	defer ispTargets.Unlock()
	if len(ispTargets.list) > 0 && time.Now().Unix()-ispTargets.t < 3600 {
		return ispTargets.list
	}
	var l []string
	for _, h := range []string{"ya.ru", "vk.com", "mail.ru"} {
		if ips, err := net.LookupHost(h); err == nil && len(ips) > 0 {
			l = append(l, net.JoinHostPort(ips[0], "443"))
		}
	}
	if len(l) == 0 {
		l = []string{"77.88.55.242:443", "87.240.132.72:443"}
	}
	ispTargets.list, ispTargets.t = l, time.Now().Unix()
	return l
}

func ispPing(d *net.Dialer) int64 {
	best := int64(-1)
	for _, h := range ispHosts() {
		start := time.Now()
		c, err := d.Dial("tcp", h)
		if err != nil {
			continue
		}
		ms := time.Since(start).Milliseconds()
		c.Close()
		if best < 0 || ms < best {
			best = ms
		}
	}
	return best
}

func (a *app) ispLoop() {
	for {
		a.met.mu.Lock()
		def := a.met.iface
		a.met.mu.Unlock()
		wans := wanList(def)
		now := time.Now().Unix()
		by := map[string]ispPoint{}
		for _, wn := range wans {
			d := ifaceDialer(wn.Name)
			d.Timeout = 3 * time.Second
			ms := ispPing(d)
			p := ispPoint{T: now, OK: ms >= 0}
			if ms >= 0 {
				p.Ms = int32(max(ms, 1))
			}
			by[wn.Name] = p
		}
		main := ispPoint{T: now}
		if p, ok := by[def]; ok {
			main = p
		} else {
			ms := ispPing(&net.Dialer{Timeout: 3 * time.Second})
			main.OK = ms >= 0
			if ms >= 0 {
				main.Ms = int32(max(ms, 1))
			}
		}
		tr := a.trf
		tr.mu.Lock()
		tr.isp = append(tr.isp, main)
		if len(tr.isp) > 1440 {
			tr.isp = tr.isp[len(tr.isp)-1440:]
		}
		if tr.ispBy == nil {
			tr.ispBy = map[string][]ispPoint{}
		}
		for n, p := range by {
			l := append(tr.ispBy[n], p)
			if len(l) > 1440 {
				l = l[len(l)-1440:]
			}
			tr.ispBy[n] = l
		}
		tr.wans = wans
		tr.mu.Unlock()
		time.Sleep(60 * time.Second)
	}
}

// ---- сохранение ----

type trafficFileT struct {
	Days    map[string]*dayStats `json:"days"`
	Devices map[string]*devInfo  `json:"devices"`
	ISP     []ispPoint           `json:"isp"`
	Seeded  bool                 `json:"seeded"`
}

func (a *app) loadTraffic() {
	tr := a.trf
	b, err := os.ReadFile(trafficPath())
	if err == nil {
		var f trafficFileT
		if json.Unmarshal(b, &f) == nil {
			if f.Days != nil {
				tr.days = f.Days
			}
			if f.Devices != nil {
				tr.devices = f.Devices
				for k, d := range tr.devices { // 0.9: убрать «устройства» с внешних адресов (например, сам роутер)
					if strings.Count(k, ":") != 5 && d.Name == "" && !isLANAddr(k) {
						delete(tr.devices, k)
					}
				}
			}
			tr.isp, tr.seeded = f.ISP, f.Seeded
		}
	}
	tr.arp = readARP() // сразу, чтобы первые соединения легли на MAC, а не на IP
	if !tr.seeded {
		tr.seeded = true
	}
}

func (a *app) trafficSaveLoop() {
	tr := a.trf
	for {
		time.Sleep(20 * time.Second)
		arp := readARP()
		tr.mu.Lock()
		tr.arp = arp
		for k, d := range tr.devices { // запись по IP, а MAC уже известен — склеить с записью по MAC
			if strings.Count(k, ":") != 5 && d.MergeInto == "" {
				if mac := arp[k]; mac != "" {
					if md := tr.devices[mac]; md != nil {
						if md.Name == "" && d.Name != "" {
							md.Name = d.Name
						}
						d.MergeInto = mac
					}
				}
			}
		}
		for _, d := range tr.devices {
			migrateEmojiName(d)
		}
		save := time.Now().Unix()-tr.lastSave >= 600
		var b []byte
		if save {
			tr.lastSave = time.Now().Unix()
			b, _ = json.Marshal(trafficFileT{Days: tr.days, Devices: tr.devices, ISP: tr.isp, Seeded: tr.seeded})
		}
		tr.mu.Unlock()
		if save && b != nil {
			tmp := trafficPath() + ".tmp"
			if os.WriteFile(tmp, b, 0644) == nil {
				os.Rename(tmp, trafficPath())
			}
		}
	}
}

// ---- API ----

func cloneDay(d *dayStats) *dayStats {
	if d == nil {
		return newDay()
	}
	b, _ := json.Marshal(d)
	var c dayStats
	json.Unmarshal(b, &c)
	return &c
}

func (a *app) handleTraffic(w http.ResponseWriter, r *http.Request) {
	tr := a.trf
	now := time.Now()
	today := a.dayKey(now)
	yest := a.dayKey(now.Add(-24 * time.Hour))
	tr.mu.Lock()
	td, yd := cloneDay(tr.days[today]), cloneDay(tr.days[yest])
	hist := map[string]bytesPair{}
	for k, d := range tr.days {
		var s bytesPair
		for _, v := range d.Servers {
			s.add(*v)
		}
		if s.Up+s.Down == 0 { // статистики Xray не было — берём по устройствам
			for _, v := range d.DevVPN {
				s.add(*v)
			}
		}
		hist[k] = s
	}
	isp := append([]ispPoint(nil), tr.isp...)
	ispBy := map[string][]ispPoint{}
	for k, v := range tr.ispBy {
		ispBy[k] = append([]ispPoint(nil), v...)
	}
	wans := append([]wanInfo(nil), tr.wans...)
	ctOK, ctErr, statsOK := tr.ctOK, tr.ctErr, tr.statsOK
	tr.mu.Unlock()
	writeJSON(w, map[string]any{"today": td, "yesterday": yd, "days": hist, "isp": isp, "ispBy": ispBy, "wans": wans,
		"ctOK": ctOK, "ctErr": ctErr, "statsOK": statsOK, "statsConfigured": xrayStatsConfigured(*flagConfDir), "tgRoute": tgRouteConfigured(*flagConfDir)})
}

func (a *app) devicesSnapshot() []map[string]any {
	tr := a.trf
	today := a.dayKey(time.Now())
	tr.mu.Lock()
	defer tr.mu.Unlock()
	day := tr.dayLocked(today)
	var out []map[string]any
	for k, d := range tr.devices {
		kind, auto := d.Kind, false
		if kind == "" {
			kind, auto = guessKind(d.Name+" "+d.KName+" "+d.Host, d.Vendor), true
		}
		row := map[string]any{"id": k, "name": d.Name, "kname": d.KName, "host": d.Host, "ip": d.IP, "vendor": d.Vendor, "firstSeen": d.FirstSeen,
			"lastSeen": d.LastSeen, "mergeInto": d.MergeInto, "isMac": strings.Count(k, ":") == 5, "kind": kind, "kindAuto": auto, "policy": d.Policy, "conn": d.Conn}
		if v := day.Devices[k]; v != nil {
			row["all"] = *v
		}
		if v := day.DevVPN[k]; v != nil {
			row["vpn"] = *v
		}
		if m := day.DevSrv[k]; m != nil {
			srv := map[string]bytesPair{}
			for s, v := range m {
				srv[s] = *v
			}
			row["servers"] = srv
		}
		if m := day.DevSvc[k]; m != nil {
			svc := map[string]bytesPair{}
			for s, v := range m {
				svc[s] = *v
			}
			row["services"] = svc
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i]["lastSeen"].(int64) > out[j]["lastSeen"].(int64) })
	return out
}

func (a *app) handleDevices(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		var req struct {
			ID        string  `json:"id"`
			Name      *string `json:"name"`
			Kind      *string `json:"kind"`
			MergeInto *string `json:"mergeInto"`
			Delete    bool    `json:"delete"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil || req.ID == "" {
			http.Error(w, "bad request", 400)
			return
		}
		tr := a.trf
		tr.mu.Lock()
		d := tr.devices[req.ID]
		switch {
		case d == nil:
		case req.Delete:
			delete(tr.devices, req.ID)
		default:
			if req.Name != nil {
				d.Name = strings.TrimSpace(*req.Name)
			}
			if req.Kind != nil {
				d.Kind = strings.TrimSpace(*req.Kind)
			}
			if req.MergeInto != nil && *req.MergeInto != req.ID {
				d.MergeInto = *req.MergeInto
			}
		}
		tr.lastSave = 0 // сохранить при следующем цикле
		tr.mu.Unlock()
		writeJSON(w, map[string]any{"ok": true})
		return
	}
	writeJSON(w, map[string]any{"devices": a.devicesSnapshot()})
}

// deviceName — имя для показа: имя, иначе IP.
func (a *app) deviceNameByIP(ip string) string {
	tr := a.trf
	tr.mu.Lock()
	defer tr.mu.Unlock()
	key := ip
	if m := tr.arp[ip]; m != "" {
		key = m
	}
	if d := tr.devices[key]; d != nil {
		for i := 0; i < 5 && d.MergeInto != ""; i++ {
			if t := tr.devices[d.MergeInto]; t != nil {
				d = t
			}
		}
		if n := devLabel(d); n != "" {
			return n
		}
	}
	return ip
}

func (a *app) deviceNames() map[string]string {
	tr := a.trf
	tr.mu.Lock()
	defer tr.mu.Unlock()
	out := map[string]string{}
	for ip, mac := range tr.arp {
		if d := tr.devices[mac]; d != nil {
			for i := 0; i < 5 && d.MergeInto != ""; i++ {
				if t := tr.devices[d.MergeInto]; t != nil {
					d = t
				}
			}
			if n := devLabel(d); n != "" {
				out[ip] = n
			}
		}
	}
	for k, d := range tr.devices {
		if n := devLabel(d); n != "" && strings.Count(k, ":") != 5 {
			out[k] = n
		}
	}
	return out
}

// handleFlow — «карта соединений» за 15 минут: устройство → сервер → сервис.
func (a *app) handleFlow(w http.ResponseWriter, r *http.Request) {
	tr := a.trf
	tr.mu.Lock()
	type link struct{ A, B string }
	ds, so := map[link]int{}, map[link]int{}
	devN, srvN, svcN := map[string]int{}, map[string]int{}, map[string]int{}
	var first int64
	for _, x := range tr.recent {
		if first == 0 {
			first = x.t
		}
		ds[link{x.dev, x.out}]++
		so[link{x.out, x.svc}]++
		devN[x.dev]++
		srvN[x.out]++
		svcN[x.svc]++
	}
	n := len(tr.recent)
	// подписанные устройства без соединений за 15 минут — показать на карте серыми
	var idle []string
	day := time.Now().Unix() - 86400
	for k, d := range tr.devices {
		if d.Name != "" && d.MergeInto == "" && d.LastSeen >= day && devN[k] == 0 {
			idle = append(idle, k)
		}
	}
	sort.Strings(idle)
	tr.mu.Unlock()
	labels := a.deviceLabels()
	conv := func(m map[link]int) []map[string]any {
		var out []map[string]any
		for k, v := range m {
			out = append(out, map[string]any{"a": k.A, "b": k.B, "n": v})
		}
		return out
	}
	span := int64(900)
	if first > 0 && time.Now().Unix()-first < span {
		span = time.Now().Unix() - first
	}
	if span < 60 {
		span = 60
	}
	writeJSON(w, map[string]any{"devSrv": conv(ds), "srvSvc": conv(so), "devices": devN, "servers": srvN,
		"services": svcN, "labels": labels, "total": n, "spanSec": span, "idle": idle})
}

// isLANAddr — адрес домашней сети (устройства), а не внешний.
func isLANAddr(ip string) bool {
	p := net.ParseIP(ip)
	return p != nil && (p.IsPrivate() || p.IsLinkLocalUnicast()) && !p.IsLoopback()
}
