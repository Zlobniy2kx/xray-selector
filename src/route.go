package main

// «Куда пойдёт этот сайт»: проходим правила Xray по порядку так же, как Xray,
// включая категории geosite/geoip из .dat (разбор protobuf вручную).

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// ---- мини-разбор protobuf ----

type gField struct {
	num  int
	wt   int
	v    uint64
	data []byte
}

func gNext(b []byte) (gField, []byte, error) {
	key, n := binary.Uvarint(b)
	if n <= 0 {
		return gField{}, nil, errors.New("pb")
	}
	b = b[n:]
	f := gField{num: int(key >> 3), wt: int(key & 7)}
	switch f.wt {
	case 0:
		v, n := binary.Uvarint(b)
		if n <= 0 {
			return f, nil, errors.New("pb")
		}
		f.v, b = v, b[n:]
	case 2:
		l, n := binary.Uvarint(b)
		if n <= 0 || int(l) > len(b)-n {
			return f, nil, errors.New("pb")
		}
		f.data, b = b[n:n+int(l)], b[n+int(l):]
	case 1:
		if len(b) < 8 {
			return f, nil, errors.New("pb")
		}
		b = b[8:]
	case 5:
		if len(b) < 4 {
			return f, nil, errors.New("pb")
		}
		b = b[4:]
	default:
		return f, nil, errors.New("pb wt")
	}
	return f, b, nil
}

type geoDomain struct {
	typ int // 0 plain(keyword) 1 regex 2 domain 3 full
	val string
}

var geoCache = struct {
	sync.Mutex
	m map[string]any
}{m: map[string]any{}}

func assetDir() string {
	for _, kv := range xrayAssetEnv() {
		if strings.HasPrefix(kv, "XRAY_LOCATION_ASSET=") {
			return strings.TrimPrefix(kv, "XRAY_LOCATION_ASSET=")
		}
	}
	return filepath.Dir(*flagXray)
}

// geositeList — домены категории code из файла (geosite.dat по умолчанию).
func geositeList(file, code string) ([]geoDomain, error) {
	p := filepath.Join(assetDir(), file)
	st, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	key := "s|" + p + "|" + strings.ToLower(code) + "|" + st.ModTime().String()
	geoCache.Lock()
	if v, ok := geoCache.m[key]; ok {
		geoCache.Unlock()
		return v.([]geoDomain), nil
	}
	geoCache.Unlock()
	if !guard.memFor(st.Size(), file) {
		return nil, fmt.Errorf("мало свободной памяти — %s сейчас не читаю", file)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var out []geoDomain
	found := false
	for len(b) > 0 {
		f, rest, err := gNext(b)
		if err != nil {
			break
		}
		b = rest
		if f.num != 1 || f.wt != 2 {
			continue
		}
		// GeoSite: 1 country_code, 2 domain
		e := f.data
		var cc string
		var doms [][]byte
		for len(e) > 0 {
			g, r2, err := gNext(e)
			if err != nil {
				break
			}
			e = r2
			if g.num == 1 && g.wt == 2 {
				cc = string(g.data)
			} else if g.num == 2 && g.wt == 2 {
				doms = append(doms, g.data)
			}
		}
		if !strings.EqualFold(cc, code) {
			continue
		}
		found = true
		for _, d := range doms {
			var gd geoDomain
			for len(d) > 0 {
				h, r3, err := gNext(d)
				if err != nil {
					break
				}
				d = r3
				if h.num == 1 && h.wt == 0 {
					gd.typ = int(h.v)
				} else if h.num == 2 && h.wt == 2 {
					gd.val = strings.ToLower(string(h.data))
				}
			}
			out = append(out, gd)
		}
		break
	}
	if !found {
		return nil, errors.New("категории " + code + " нет в " + file)
	}
	geoCache.Lock()
	if len(geoCache.m) > 40 {
		geoCache.m = map[string]any{}
	}
	geoCache.m[key] = out
	geoCache.Unlock()
	return out, nil
}

// geoipList — подсети категории code.
func geoipList(file, code string) ([]*net.IPNet, bool, error) {
	p := filepath.Join(assetDir(), file)
	st, err := os.Stat(p)
	if err != nil {
		return nil, false, err
	}
	key := "i|" + p + "|" + strings.ToLower(code) + "|" + st.ModTime().String()
	geoCache.Lock()
	if v, ok := geoCache.m[key]; ok {
		geoCache.Unlock()
		x := v.([2]any)
		return x[0].([]*net.IPNet), x[1].(bool), nil
	}
	geoCache.Unlock()
	if !guard.memFor(st.Size(), file) {
		return nil, false, fmt.Errorf("мало свободной памяти — %s сейчас не читаю", file)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, false, err
	}
	var out []*net.IPNet
	rev, found := false, false
	for len(b) > 0 {
		f, rest, err := gNext(b)
		if err != nil {
			break
		}
		b = rest
		if f.num != 1 || f.wt != 2 {
			continue
		}
		e := f.data
		var cc string
		var cidrs [][]byte
		r := false
		for len(e) > 0 {
			g, r2, err := gNext(e)
			if err != nil {
				break
			}
			e = r2
			switch {
			case g.num == 1 && g.wt == 2:
				cc = string(g.data)
			case g.num == 2 && g.wt == 2:
				cidrs = append(cidrs, g.data)
			case g.num == 3 && g.wt == 0:
				r = g.v != 0
			}
		}
		if !strings.EqualFold(cc, code) {
			continue
		}
		found, rev = true, r
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
			if len(ip) == 4 || len(ip) == 16 {
				out = append(out, &net.IPNet{IP: net.IP(ip), Mask: net.CIDRMask(int(pre), len(ip)*8)})
			}
		}
		break
	}
	if !found {
		return nil, false, errors.New("категории " + code + " нет в " + file)
	}
	geoCache.Lock()
	geoCache.m[key] = [2]any{out, rev}
	geoCache.Unlock()
	return out, rev, nil
}

func matchGeoDomain(host string, list []geoDomain) string {
	for _, d := range list {
		switch d.typ {
		case 0:
			if strings.Contains(host, d.val) {
				return d.val
			}
		case 1:
			if re, err := regexp.Compile(d.val); err == nil && re.MatchString(host) {
				return d.val
			}
		case 2:
			if host == d.val || strings.HasSuffix(host, "."+d.val) {
				return d.val
			}
		case 3:
			if host == d.val {
				return d.val
			}
		}
	}
	return ""
}

// matchDomainEntry — совпадает ли домен с записью правила; why — пояснение.
func matchDomainEntry(host, e string) (bool, string) {
	low := strings.ToLower(e)
	switch {
	case strings.HasPrefix(low, "domain:"):
		d := low[7:]
		return host == d || strings.HasSuffix(host, "."+d), e
	case strings.HasPrefix(low, "full:"):
		return host == low[5:], e
	case strings.HasPrefix(low, "keyword:"):
		return strings.Contains(host, low[8:]), e
	case strings.HasPrefix(low, "regexp:"):
		re, err := regexp.Compile(e[7:])
		return err == nil && re.MatchString(host), e
	case strings.HasPrefix(low, "geosite:"):
		l, err := geositeList("geosite.dat", strings.Split(e[8:], "@")[0])
		if err == nil {
			if m := matchGeoDomain(host, l); m != "" {
				return true, e + " (" + m + ")"
			}
		}
		return false, ""
	case strings.HasPrefix(low, "ext:"):
		parts := strings.SplitN(e[4:], ":", 2)
		if len(parts) == 2 {
			l, err := geositeList(parts[0], strings.Split(parts[1], "@")[0])
			if err == nil {
				if m := matchGeoDomain(host, l); m != "" {
					return true, e + " (" + m + ")"
				}
			}
		}
		return false, ""
	}
	return strings.Contains(host, low), e // без префикса — подстрока
}

func matchIPEntry(ips []net.IP, e string) (bool, string) {
	neg := strings.HasPrefix(e, "!")
	x := strings.TrimPrefix(e, "!")
	var nets []*net.IPNet
	rev := false
	low := strings.ToLower(x)
	switch {
	case strings.HasPrefix(low, "geoip:"):
		nets, rev, _ = geoipList("geoip.dat", x[6:])
	case strings.HasPrefix(low, "ext:"):
		parts := strings.SplitN(x[4:], ":", 2)
		if len(parts) == 2 {
			nets, rev, _ = geoipList(parts[0], parts[1])
		}
	default:
		if _, n, err := net.ParseCIDR(x); err == nil {
			nets = []*net.IPNet{n}
		} else if ip := net.ParseIP(x); ip != nil {
			bits := 32
			if ip.To4() == nil {
				bits = 128
			}
			nets = []*net.IPNet{{IP: ip, Mask: net.CIDRMask(bits, bits)}}
		}
	}
	for _, ip := range ips {
		in := false
		for _, n := range nets {
			if n.Contains(ip) {
				in = true
				break
			}
		}
		if rev {
			in = !in
		}
		if neg {
			in = !in
		}
		if in {
			return true, e + " (" + ip.String() + ")"
		}
	}
	return false, ""
}

type routeResult struct {
	Host    string   `json:"host"`
	IPs     []string `json:"ips"`
	Rule    int      `json:"rule"`
	Kind    string   `json:"kind"`
	Target  string   `json:"target"`
	Why     string   `json:"why"`
	Active  string   `json:"active,omitempty"`
	Disable string   `json:"disabled,omitempty"` // совпало бы с отключённой записью
}

// routeFor — куда пойдёт TCP-соединение на host:443 от устройства в политике xkeen.
func (a *app) routeFor(host string) (*routeResult, error) {
	_, h := normalizeEntry(host)
	h = strings.TrimPrefix(h, "domain:")
	h = strings.ToLower(h)
	res := &routeResult{Host: h, Rule: -1}
	var ips []net.IP
	if ip := net.ParseIP(h); ip != nil {
		ips = []net.IP{ip}
	} else if addrs, err := net.LookupIP(h); err == nil {
		ips = addrs
	}
	for _, ip := range ips {
		res.IPs = append(res.IPs, ip.String())
	}
	_, _, rules, _, err := readRules()
	if err != nil {
		return nil, err
	}
	servers := a.serverSet()
	for i, r := range rules {
		if r.Off || len(r.InboundTag) > 0 || len(r.Source) > 0 || len(r.Protocol) > 0 {
			continue // правила для особых входов/источников — не про обычный трафик
		}
		if r.Network != "" && !strings.Contains(strings.ToLower(r.Network), "tcp") {
			continue
		}
		if len(r.Port) > 0 {
			ps := strings.Trim(string(r.Port), `"`)
			if !portIn(443, ps) {
				continue
			}
		}
		ok, why := len(r.Domain) == 0 && len(r.IP) == 0, "всё остальное"
		if len(r.Domain) > 0 && net.ParseIP(h) == nil {
			for _, e := range r.Domain {
				if m, w := matchDomainEntry(h, e); m {
					ok, why = true, w
					break
				}
			}
		}
		if !ok && len(r.IP) > 0 && len(ips) > 0 {
			for _, e := range r.IP {
				if m, w := matchIPEntry(ips, e); m {
					ok, why = true, w
					break
				}
			}
		}
		if !ok && res.Disable == "" {
			for _, e := range r.OffDomain {
				if m, _ := matchDomainEntry(h, e); m {
					res.Disable = e
				}
			}
		}
		if ok {
			res.Rule, res.Kind, res.Why = i, ruleKind(r, servers), why
			res.Target = r.OutboundTag
			if r.BalancerTag != "" {
				res.Target = r.BalancerTag
				a.mu.Lock()
				res.Active = a.desired[r.BalancerTag]
				a.mu.Unlock()
			}
			return res, nil
		}
	}
	res.Kind, res.Why = "direct", "ни одно правило не подошло — Xray отправит в первый outbound"
	return res, nil
}

func portIn(p int, spec string) bool {
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if lo, hi, ok := strings.Cut(part, "-"); ok {
			var l, h int
			if _, err := fmtSscan(lo, &l); err == nil {
				if _, err := fmtSscan(hi, &h); err == nil && p >= l && p <= h {
					return true
				}
			}
		} else {
			var v int
			if _, err := fmtSscan(part, &v); err == nil && v == p {
				return true
			}
		}
	}
	return false
}

func fmtSscan(s string, v *int) (int, error) { return fmt.Sscan(strings.TrimSpace(s), v) }
