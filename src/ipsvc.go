package main

// Сервис по IP-адресу: многие приложения (Telegram, звонки, игры) соединяются сразу
// по IP, без имени сайта. Распознаём их по известным диапазонам и по категориям
// geoip из правил маршрутизации (раздел «Сайты») — у каждого роутера свои правила,
// поэтому у знакомых подтянутся их собственные списки.

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var builtinIPs = []struct{ name, cidrs string }{
	{"Telegram", "91.108.4.0/22,91.108.8.0/22,91.108.12.0/22,91.108.16.0/22,91.108.20.0/22,91.108.56.0/22,91.105.192.0/23,95.161.64.0/20,149.154.160.0/20,185.76.151.0/24"},
	{"WhatsApp", "157.240.0.0/16,31.13.0.0/16,179.60.192.0/22,185.60.216.0/22,66.220.144.0/20,69.63.176.0/20,102.132.96.0/20,129.134.0.0/16,163.70.128.0/17,57.144.0.0/14"},
	{"Google", "142.250.0.0/15,172.217.0.0/16,216.58.192.0/19,172.253.0.0/16,74.125.0.0/16,173.194.0.0/16,108.177.0.0/17,209.85.128.0/17,64.233.160.0/19,35.190.0.0/17,34.64.0.0/10"},
	{"Cloudflare", "104.16.0.0/13,104.24.0.0/14,172.64.0.0/13,162.158.0.0/15,188.114.96.0/20,141.101.64.0/18,190.93.240.0/20,198.41.128.0/17,1.1.1.0/24,1.0.0.0/24"},
	{"Apple", "17.0.0.0/8"},
	{"Steam", "155.133.224.0/19,162.254.192.0/21,185.25.180.0/22,205.196.6.0/24,146.66.152.0/21"},
	{"Microsoft", "13.64.0.0/11,20.33.0.0/16,20.34.0.0/15,20.36.0.0/14,20.40.0.0/13,20.48.0.0/12,20.64.0.0/10,20.128.0.0/16,40.64.0.0/10,52.96.0.0/12,52.112.0.0/14"},
	{"Яндекс", "5.255.255.0/24,77.88.0.0/18,87.250.224.0/19,93.158.128.0/18,213.180.192.0/19,5.45.192.0/18"},
	{"VK", "87.240.128.0/18,93.186.224.0/20,95.142.192.0/20,95.213.0.0/17,185.32.248.0/22"},
}

type ipNet struct {
	n    *net.IPNet
	name string
}

var ipSvc struct {
	sync.Mutex
	list     []ipNet
	built    int64
	cache    map[string]string
	building bool
}

func catTitle(c string) string {
	c = strings.ToLower(c)
	switch c {
	case "ru":
		return "Российские сайты"
	case "telegram":
		return "Telegram"
	case "meta", "facebook", "instagram":
		return "Instagram"
	case "youtube":
		return "YouTube"
	case "google":
		return "Google"
	case "discord":
		return "Discord"
	case "cloudflare":
		return "Cloudflare"
	}
	if c == "" {
		return ""
	}
	return strings.ToUpper(c[:1]) + c[1:]
}

func buildIPSvc() []ipNet {
	out := geoipFileCats() // сначала категории из баз (YouTube раньше общего Google)
	for _, b := range builtinIPs {
		for _, c := range strings.Split(b.cidrs, ",") {
			if _, n, err := net.ParseCIDR(c); err == nil {
				out = append(out, ipNet{n, b.name})
			}
		}
	}
	// категории geoip из правил «Сайтов»
	if _, _, rules, _, err := readRules(); err == nil {
		seen := map[string]bool{}
		for _, r := range rules {
			for _, e := range r.IP {
				e = strings.TrimPrefix(e, "!")
				low := strings.ToLower(e)
				var file, cat string
				switch {
				case strings.HasPrefix(low, "geoip:"):
					file, cat = "geoip.dat", e[6:]
				case strings.HasPrefix(low, "ext:"):
					p := strings.SplitN(e[4:], ":", 2)
					if len(p) == 2 {
						file, cat = p[0], p[1]
					}
				}
				if cat == "" || strings.EqualFold(cat, "private") || seen[file+cat] {
					continue
				}
				seen[file+cat] = true
				if st, err := os.Stat(filepath.Join(assetDir(), file)); err != nil || st.Size() > 12<<20 {
					continue
				}
				nets, rev, err := geoipList(file, cat)
				if err != nil || rev {
					continue
				}
				for _, n := range nets {
					out = append(out, ipNet{n, catTitle(cat)})
				}
			}
		}
	}
	return out
}

// ipService — название сервиса по IP или "".
func ipService(ip string) string {
	p := net.ParseIP(strings.Trim(ip, "[]"))
	if p == nil {
		return ""
	}
	ipSvc.Lock()
	defer ipSvc.Unlock()
	now := time.Now().Unix()
	if !ipSvc.building && (ipSvc.list == nil || now-ipSvc.built > 3600) {
		ipSvc.building, ipSvc.built = true, now
		go func() {
			l := buildIPSvc()
			ipSvc.Lock()
			ipSvc.list, ipSvc.cache, ipSvc.building = l, map[string]string{}, false
			ipSvc.Unlock()
		}()
	}
	if ipSvc.cache == nil {
		ipSvc.cache = map[string]string{}
	}
	if v, ok := ipSvc.cache[ip]; ok {
		return v
	}
	name := ""
	for _, x := range ipSvc.list {
		if x.n.Contains(p) {
			name = x.name
			break
		}
	}
	if len(ipSvc.cache) > 5000 {
		ipSvc.cache = map[string]string{}
	}
	ipSvc.cache[ip] = name
	return name
}

// serviceName — сервис по имени сайта, а для голого IP — по диапазонам.
func serviceName(host string) string {
	s := serviceOf(host)
	if s == "Другое" && net.ParseIP(strings.Trim(host, "[]")) != nil {
		if n := ipService(host); n != "" {
			return n
		}
	}
	return s
}

// приоритет: сначала конкретные сервисы, потом общие хостинги и CDN
var catOrder = []string{"telegram", "youtube", "meta", "facebook", "instagram", "whatsapp", "twitter", "discord", "netflix", "spotify", "tiktok", "openai",
	"google", "apple", "microsoft", "amazon", "cloudflare", "fastly", "akamai", "cloudfront", "gcore", "cdn77", "bunnycdn", "hetzner", "ovh", "digitalocean", "linode", "vultr", "oracle", "contabo", "leaseweb", "scaleway", "datacamp"}

func geoipFileCats() []ipNet {
	want := map[string]int{}
	for i, c := range catOrder {
		want[c] = i
	}
	byCat := make([][]ipNet, len(catOrder))
	fs, _ := filepathGlob(assetDir(), "*ip*.dat")
	for _, f := range fs {
		p := filepath.Join(assetDir(), f)
		if st, err := os.Stat(p); err != nil || st.Size() > 12<<20 || !guard.memFor(st.Size(), f) {
			continue // огромные базы не читаем — бережём память роутера
		}
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		// один проход по файлу: только нужные категории
		for len(b) > 0 {
			fl, rest, err := gNext(b)
			if err != nil {
				break
			}
			b = rest
			if fl.num != 1 || fl.wt != 2 {
				continue
			}
			e := fl.data
			var cc string
			var cidrs [][]byte
			rev := false
			for len(e) > 0 {
				g, r2, err := gNext(e)
				if err != nil {
					break
				}
				e = r2
				switch {
				case g.num == 1 && g.wt == 2:
					cc = strings.ToLower(string(g.data))
				case g.num == 2 && g.wt == 2:
					cidrs = append(cidrs, g.data)
				case g.num == 3 && g.wt == 0:
					rev = g.v != 0
				}
			}
			idx, ok := want[cc]
			if !ok || rev {
				continue
			}
			for _, c := range cidrs {
				var ip []byte
				var pre uint64
				for len(c) > 0 {
					h, r3, err := gNext(c)
					if err != nil {
						break
					}
					c = r3
					if h.num == 1 && h.wt == 2 {
						ip = h.data
					} else if h.num == 2 && h.wt == 0 {
						pre = h.v
					}
				}
				if len(ip) == 4 { // только IPv4 — меньше памяти
					byCat[idx] = append(byCat[idx], ipNet{&net.IPNet{IP: net.IP(append([]byte(nil), ip...)), Mask: net.CIDRMask(int(pre), 32)}, catTitle(cc)})
				}
			}
		}
		b = nil
	}
	var out []ipNet
	for _, l := range byCat {
		out = append(out, l...)
	}
	return out
}

func filepathGlob(dir, pat string) ([]string, error) {
	m, err := filepath.Glob(filepath.Join(dir, pat))
	for i := range m {
		m[i] = filepath.Base(m[i])
	}
	return m, err
}
