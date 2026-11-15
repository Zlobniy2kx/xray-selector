package main

// Резерв через подключения самого Keenetic — WireGuard, OpenVPN, SSTP, L2TP/PPTP,
// IPsec и «Прокси» (SOCKS5/HTTP, в системе это интерфейс tun2socks) — на случай,
// когда все серверы подписки перестали работать. Для каждого выбранного подключения
// в конфиг Xray добавляется outbound «freedom», привязанный к его интерфейсу
// (sockopt.interface): трафик уходит через подключение роутера, мимо серверов подписки.
//
// Список подключений берётся у самого Keenetic (RCI на 127.0.0.1:79). Если подключение
// пересоздать с теми же настройками интерфейса (например, загрузить новый .conf в тот же
// WireGuard0) — имя интерфейса не меняется и резерв продолжает работать сам. С режимом
// «подхватывать новые» программа сама добавит и новые подключения (в спокойное время).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

const reservePrefix = "🛟 "
const reserveFile = "09_xsel_reserve.json"

func isReserveTag(t string) bool { return strings.HasPrefix(t, reservePrefix) }

type ifaceInfo struct {
	Name  string `json:"name"`  // системное имя (nwg0, t2s0…)
	ID    string `json:"id"`    // имя в Keenetic (Wireguard0, Proxy0…)
	Type  string `json:"type"`  // WireGuard / Прокси / OpenVPN…
	Desc  string `json:"desc"`  // описание, заданное в Keenetic
	Up    bool   `json:"up"`    // подключено
	Label string `json:"label"` // для показа
	Tag   string `json:"tag"`   // тег outbound в Xray
}

var tunnelTypes = []struct{ key, name string }{
	{"wireguard", "WireGuard"}, {"proxy", "Прокси"}, {"openvpn", "OpenVPN"}, {"sstp", "SSTP"},
	{"l2tp", "L2TP"}, {"pptp", "PPTP"}, {"ipsec", "IPsec"}, {"ike", "IPsec"}, {"zerotier", "ZeroTier"},
}

func tunnelKind(s string) string {
	l := strings.ToLower(s)
	for _, t := range tunnelTypes {
		if strings.Contains(l, t.key) {
			return t.name
		}
	}
	return ""
}

// rciInterfaces — подключения из Keenetic (show interface).
func rciInterfaces() ([]ifaceInfo, error) {
	cl := &http.Client{Timeout: 4 * time.Second}
	resp, err := cl.Get("http://127.0.0.1:79/rci/show/interface")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	var m map[string]map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	var out []ifaceInfo
	for id, v := range m {
		str := func(k string) string { s, _ := v[k].(string); return s }
		kind := tunnelKind(str("type"))
		if kind == "" {
			kind = tunnelKind(id)
		}
		if kind == "" {
			continue
		}
		// п. 2: подключение провайдера (PPPoE/L2TP/PPTP «для интернета») — не резервный туннель.
		// Признак — Keenetic ставит ему приоритет или global. ПРОВЕРИТЬ на реальном RCI.
		if strings.Contains(strings.ToLower(str("type")), "pppoe") || strings.HasPrefix(strings.ToLower(id), "pppoe") { // 0.9: global не признак провайдера — у WireGuard/Proxy он тоже есть
			continue
		}
		sys := str("interface-name")
		if sys == "" || sys == id {
			sys = sysNameGuess(id)
		}
		if _, err := os.Stat(filepath.Join("/sys/class/net", sys)); err != nil {
			continue // интерфейса в системе нет (не создан/выключен)
		}
		up := str("connected") == "yes" || str("link") == "up" || str("state") == "up"
		out = append(out, ifaceInfo{Name: sys, ID: id, Type: kind, Desc: str("description"), Up: up})
	}
	return out, nil
}

// sysNameGuess — системное имя по имени Keenetic, если RCI его не дал.
func sysNameGuess(id string) string {
	l := strings.ToLower(id)
	num := strings.TrimLeft(l, "abcdefghijklmnopqrstuvwxyz")
	switch {
	case strings.HasPrefix(l, "wireguard"):
		return "nwg" + num
	case strings.HasPrefix(l, "proxy"):
		return "t2s" + num
	case strings.HasPrefix(l, "openvpn"):
		return "ovpn_br" + num
	}
	return l
}

// sysTunnels — запасной способ: по именам интерфейсов в системе.
func sysTunnels() []ifaceInfo {
	ents, _ := os.ReadDir("/sys/class/net")
	var out []ifaceInfo
	for _, e := range ents {
		n := e.Name()
		kind := ""
		switch {
		case strings.HasPrefix(n, "nwg"):
			kind = "WireGuard"
		case strings.HasPrefix(n, "t2s"):
			kind = "Прокси"
		case strings.HasPrefix(n, "ovpn"):
			kind = "OpenVPN"
		case strings.HasPrefix(n, "sstp"):
			kind = "SSTP"
		case strings.HasPrefix(n, "l2tp"):
			kind = "L2TP"
		default:
			continue
		}
		st, _ := os.ReadFile(filepath.Join("/sys/class/net", n, "operstate"))
		s := strings.TrimSpace(string(st))
		out = append(out, ifaceInfo{Name: n, Type: kind, Up: s == "up" || s == "unknown"})
	}
	return out
}

// tunnelIfaces — все подключения-туннели и прокси роутера.
func tunnelIfaces() []ifaceInfo {
	list, err := rciInterfaces()
	if err != nil || len(list) == 0 {
		list = sysTunnels()
	} else { // добавить то, чего RCI не показал
		have := map[string]bool{}
		for _, x := range list {
			have[x.Name] = true
		}
		for _, x := range sysTunnels() {
			if !have[x.Name] {
				list = append(list, x)
			}
		}
	}
	for i := range list {
		x := &list[i]
		name := x.ID
		if name == "" {
			name = x.Name
		}
		x.Label = x.Type + " · " + name
		if x.Desc != "" {
			x.Label += " · " + x.Desc
		}
		x.Tag = reservePrefix + x.Type + " " + name
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Up != list[j].Up {
			return list[i].Up
		}
		return list[i].Name < list[j].Name
	})
	return list
}

type reserveEntry struct {
	Iface string `json:"iface"`
	Tag   string `json:"tag"`
}

// reserveConfigured — какие подключения сейчас в резерве (по файлу конфига).
func reserveConfigured() []reserveEntry {
	b, err := os.ReadFile(filepath.Join(*flagConfDir, reserveFile))
	if err != nil {
		return nil
	}
	var d struct {
		Outbounds []struct {
			Tag    string `json:"tag"`
			Stream struct {
				Sockopt struct {
					Interface string `json:"interface"`
				} `json:"sockopt"`
			} `json:"streamSettings"`
		} `json:"outbounds"`
	}
	json.Unmarshal(stripJSONComments(b), &d)
	var out []reserveEntry
	for _, o := range d.Outbounds {
		if o.Stream.Sockopt.Interface != "" {
			out = append(out, reserveEntry{o.Stream.Sockopt.Interface, o.Tag})
		}
	}
	return out
}

func ifaceDialer(iface string) *net.Dialer {
	return &net.Dialer{Timeout: 6 * time.Second, Control: func(network, address string, c syscall.RawConn) error {
		var serr error
		c.Control(func(fd uintptr) { serr = syscall.BindToDevice(int(fd), iface) })
		return serr
	}}
}

// checkReserve — открываются ли сервисы сторожа через подключение.
func (a *app) checkReserve(iface string) (bool, string) {
	d := ifaceDialer(iface)
	tr := &http.Transport{DialContext: d.DialContext, DisableKeepAlives: true, TLSHandshakeTimeout: 8 * time.Second}
	defer tr.CloseIdleConnections()
	cl := &http.Client{Transport: tr, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	a.watch.mu.Lock()
	targets := a.watch.settings.Targets
	a.watch.mu.Unlock()
	okN, bad := 0, ""
	for _, t := range targets {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		req, _ := http.NewRequestWithContext(ctx, "GET", t.URL, nil)
		resp, err := cl.Do(req)
		cancel()
		if err == nil && resp.StatusCode < 500 {
			resp.Body.Close()
			okN++
		} else if bad == "" {
			if err != nil {
				bad = t.Name + ": " + explainNetErr(err)
			} else {
				resp.Body.Close()
				bad = t.Name + ": HTTP " + resp.Status
			}
		}
	}
	return okN == len(targets) && okN > 0, bad
}

func reserveFileContent(list []ifaceInfo) string {
	var obs []any
	for _, x := range list {
		obs = append(obs, map[string]any{
			"tag": x.Tag, "protocol": "freedom",
			"settings":       map[string]any{"domainStrategy": "UseIPv4"},
			"streamSettings": map[string]any{"sockopt": map[string]any{"interface": x.Name}},
		})
	}
	b, _ := json.MarshalIndent(map[string]any{"outbounds": obs}, "", "  ")
	return string(b)
}

// setReserve — записать в конфиг выбранные подключения (пусто — выключить резерв).
func (a *app) setReserve(names []string, why string) error {
	if len(names) == 0 {
		if len(reserveConfigured()) == 0 {
			return nil
		}
		return a.applyConfig("резерв через подключения Keenetic выключен", []cfgChange{{Name: reserveFile, Delete: true}})
	}
	all := tunnelIfaces()
	var sel []ifaceInfo
	for _, n := range names {
		found := false
		for _, x := range all {
			if x.Name == n {
				sel, found = append(sel, x), true
			}
		}
		if !found {
			return errors.New("нет такого подключения: " + n)
		}
	}
	var labels []string
	for _, x := range sel {
		labels = append(labels, x.Label)
	}
	if why == "" {
		why = "резерв через подключения Keenetic: " + strings.Join(labels, ", ")
	}
	return a.applyConfig(why, []cfgChange{{Name: reserveFile, Content: reserveFileContent(sel)}})
}

// tryReserve — вызывается аварийным поиском, когда ни один сервер подписки не работает:
// по очереди проверяет подключения резерва и переключается на первое рабочее.
func (a *app) tryReserve(group string) bool {
	conf := reserveConfigured()
	if len(conf) == 0 {
		return false
	}
	w := a.watch
	var fails []string
	for _, e := range a.reserveOrder(conf) {
		ok, why := a.checkReserve(e.Iface)
		if !ok {
			fails = append(fails, strings.TrimPrefix(e.Tag, reservePrefix)+" — "+why)
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		err := a.api.OverrideBalancer(ctx, group, e.Tag)
		cancel()
		if err != nil {
			fails = append(fails, "не удалось переключить: "+err.Error())
			continue
		}
		w.mu.Lock()
		w.addEventShort(group, "switch", "ни один сервер подписки не работает — включаю резерв: "+strings.TrimPrefix(e.Tag, reservePrefix)+". Вернусь на сервер, как только какой-нибудь оживёт",
			"⚠️ Все серверы подписки не работают → "+shortSrv(e.Tag, ""))
		delete(w.allDown, group)
		w.mu.Unlock()
		a.mu.Lock()
		a.desired[group] = e.Tag
		a.saveStateLocked()
		a.mu.Unlock()
		return true
	}
	w.mu.Lock()
	w.addEvent(group, "warn", "резерв тоже не открывает сервисы: "+strings.Join(fails, "; "))
	w.mu.Unlock()
	return false
}

// reserveAutoLoop — режим «подхватывать новые подключения»: раз в 10 минут сверяет
// список подключений Keenetic с резервом и, если появилось новое или пропало старое,
// обновляет конфиг — только в спокойное время и не когда трафик идёт через резерв.
func (a *app) reserveAutoLoop() {
	time.Sleep(5 * time.Minute)
	for {
		time.Sleep(10 * time.Minute)
		a.watch.mu.Lock()
		auto := a.watch.settings.ReserveAuto
		a.watch.mu.Unlock()
		if !auto {
			continue
		}
		onReserve := false
		a.mu.Lock()
		for _, t := range a.desired {
			if isReserveTag(t) {
				onReserve = true
			}
		}
		a.mu.Unlock()
		if onReserve || !a.quietNow(1000) {
			continue
		}
		cur := map[string]bool{}
		for _, e := range reserveConfigured() {
			cur[e.Iface] = true
		}
		var names []string
		now := map[string]bool{}
		for _, x := range tunnelIfaces() {
			names = append(names, x.Name)
			now[x.Name] = true
		}
		same := len(cur) == len(now)
		for n := range now {
			same = same && cur[n]
		}
		if same {
			continue
		}
		var added, removed []string
		for n := range now {
			if !cur[n] {
				added = append(added, n)
			}
		}
		for n := range cur {
			if !now[n] {
				removed = append(removed, n)
			}
		}
		why := fmt.Sprintf("резерв: подключения Keenetic изменились (новые: %s; пропали: %s)", orDash(added), orDash(removed))
		if err := a.setReserve(names, why); err != nil {
			a.watch.mu.Lock()
			a.watch.addEvent("", "warn", "не удалось обновить резерв: "+err.Error())
			a.watch.mu.Unlock()
		}
	}
}

func orDash(l []string) string {
	if len(l) == 0 {
		return "—"
	}
	return strings.Join(l, ", ")
}

func (a *app) handleReserve(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		a.watch.mu.Lock()
		auto := a.watch.settings.ReserveAuto
		a.watch.mu.Unlock()
		writeJSON(w, map[string]any{"ifaces": tunnelIfaces(), "configured": reserveConfigured(), "auto": auto})
		return
	}
	var req struct {
		Ifaces []string `json:"ifaces"`
		Auto   *bool    `json:"auto"`
		Check  string   `json:"check"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if req.Check != "" {
		ok, why := a.checkReserve(req.Check)
		writeJSON(w, map[string]any{"ok": ok, "why": why})
		return
	}
	if req.Auto != nil {
		a.watch.mu.Lock()
		a.watch.settings.ReserveAuto = *req.Auto
		a.watch.mu.Unlock()
		a.mu.Lock()
		a.saveStateLocked()
		a.mu.Unlock()
	}
	if req.Ifaces != nil {
		if err := a.setReserve(req.Ifaces, ""); err != nil {
			w.WriteHeader(400)
			writeJSON(w, map[string]any{"error": err.Error()})
			return
		}
	}
	writeJSON(w, map[string]any{"ok": true})
}

func isGlobal(v map[string]any) bool {
	switch g := v["global"].(type) {
	case bool:
		return g
	case string:
		return g == "yes" || g == "true"
	}
	return false
}
