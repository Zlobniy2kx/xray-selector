package main

// 2.3: раздел «Правила» — сервисы, общие режимы, режимы устройств.
//   п. 6  — каталог: «Через VPN» / «Без VPN» / по умолчанию; авто-разнос старых правил по сервисам;
//   п. 15 — блок «Без VPN — через вашего провайдера»;
//   п. 16 — общие режимы: 1 Выборочно, 2 Почти всё через VPN (кроме «Без VPN»), 3 Полностью через VPN;
//   п. 17 — режим на устройство (по IP-источнику), проверка закреплённого IP;
//   п. 23 — временный режим устройства с автовозвратом;
//   QUIC — «Отключить QUIC (рекомендуется)».
//
// Панель ведёт свои правила прямо в файле правил (05_routing.json) — помеченные полем "_xsel".
// Правила пользователя не трогаются (кроме явного «разнести по сервисам»).
// Порядок: [служебные правила пользователя: api, локальные адреса] → [правила панели] →
// [остальные правила пользователя] → [режим 2: «всё остальное — через VPN»] → [«всё остальное» пользователя].

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type devRule struct {
	Mode  string `json:"mode"`            // "" — как у всей сети; "1" | "2" | "3" | "novpn"
	Name  string `json:"name,omitempty"`  // для подписи в правиле
	Temp  string `json:"temp,omitempty"`  // временный режим
	Until int64  `json:"until,omitempty"` // до какого времени временный
	// п. 24 — детские устройства
	KidsBlock []string `json:"kidsBlock,omitempty"` // сервисы, которые блокировать по расписанию
	KidsFrom  int      `json:"kidsFrom,omitempty"`  // час начала (22)
	KidsTo    int      `json:"kidsTo,omitempty"`    // час конца (7)
	Adult     bool     `json:"adult,omitempty"`     // блокировать сайты для взрослых
	Ads       *bool    `json:"ads,omitempty"`       // п. 25: реклама для устройства (nil — как у сети)
}

type rulesState struct {
	Mode     int                 `json:"mode"` // 1 | 2 | 3
	VPN      []string            `json:"vpn"`
	NoVPN    []string            `json:"novpn"`
	QUICOff  bool                `json:"quicOff"`
	Ads      bool                `json:"ads,omitempty"` // п. 25
	Devices  map[string]*devRule `json:"devices"`       // ключ — IP устройства
	Applied  int64               `json:"applied"`
	Absorbed bool                `json:"absorbed,omitempty"`
	// п. 20 — подсказки: адреса, которые не открылись через провайдера
	Ignored []string `json:"ignored,omitempty"`
}

var rulesMu sync.Mutex

func rulesPath() string { return filepath.Join(filepath.Dir(*flagState), "xray-selector-rules.json") }

func loadRulesState() *rulesState {
	st := &rulesState{Mode: 1, Devices: map[string]*devRule{}}
	if b, err := os.ReadFile(rulesPath()); err == nil {
		json.Unmarshal(b, st)
	}
	if st.Mode < 1 || st.Mode > 3 {
		st.Mode = 1
	}
	if st.Devices == nil {
		st.Devices = map[string]*devRule{}
	}
	return st
}

func saveRulesState(st *rulesState) error {
	b, _ := json.MarshalIndent(st, "", "  ")
	return os.WriteFile(rulesPath(), b, 0644)
}

// ---- текстовые операции над routing.rules ----

// removeSpan — вырезать правило i вместе с запятой.
func removeSpan(s string, spans [][2]int, i int) string {
	st, en := spans[i][0], spans[i][1]+1
	j := skipWS(s, en)
	if j < len(s) && s[j] == ',' {
		k := skipWS(s, j+1)
		return s[:st] + s[k:]
	}
	// последний элемент — убрать запятую перед ним
	k := st - 1
	for k >= 0 && (s[k] == ' ' || s[k] == '\t' || s[k] == '\n' || s[k] == '\r') {
		k--
	}
	if k >= 0 && s[k] == ',' {
		return s[:k] + s[en:]
	}
	return s[:st] + s[en:]
}

type ruleX struct {
	ruleRaw
}

func parseRulesX(s string) ([]ruleX, [][2]int, error) {
	spans, err := ruleSpans(s)
	if err != nil {
		return nil, nil, err
	}
	out := make([]ruleX, len(spans))
	for i, sp := range spans {
		if e := json.Unmarshal(stripJSONComments([]byte(s[sp[0]:sp[1]+1])), &out[i]); e != nil {
			return nil, nil, fmt.Errorf("правило %d не разбирается: %v", i+1, e)
		}
	}
	return out, spans, nil
}

func isPrivateEntry(x string) bool {
	x = strings.ToLower(strings.TrimSpace(x))
	if x == "geoip:private" || strings.HasSuffix(x, ":private") {
		return true
	}
	ip := x
	if i := strings.Index(ip, "/"); i > 0 {
		ip = ip[:i]
	}
	p := net.ParseIP(ip)
	return p != nil && (p.IsPrivate() || p.IsLoopback() || p.IsLinkLocalUnicast() || p.IsMulticast() || p.IsUnspecified())
}

// служебное правило в начале: api, локальные адреса, DNS-сервер роутера и т. п.
func isSystemRule(r ruleX) bool {
	if len(r.InboundTag) > 0 || r.OutboundTag == "api" {
		return true
	}
	if len(r.IP) > 0 && len(r.Domain) == 0 {
		for _, x := range r.IP {
			if !isPrivateEntry(x) {
				return false
			}
		}
		return true
	}
	return false
}

func isCatchAll(r ruleX) bool {
	return len(r.Domain) == 0 && len(r.IP) == 0 && len(r.InboundTag) == 0 && len(r.Port) == 0 && len(r.Source) == 0 && len(r.Protocol) == 0
}

func ruleJSON(m map[string]any) string {
	b, _ := json.Marshal(m)
	return string(b)
}

type svcSets struct {
	vpnDom, vpnIP, noDom, noIP []string
}

func collect(ids []string) (dom, ip []string) {
	for _, id := range ids {
		if s := svcByID(id); s != nil {
			dom = append(dom, s.Domain...)
			ip = append(ip, s.IP...)
		}
	}
	return uniq(dom), uniq(ip)
}

// buildManaged — правила панели (голова) и «хвост» для режима 2.
func buildManaged(st *rulesState, bal string, now time.Time) (head []string, tail string) {
	var ss svcSets
	ss.vpnDom, ss.vpnIP = collect(st.VPN)
	ss.noDom, ss.noIP = collect(st.NoVPN)
	vpn := func(m map[string]any) map[string]any { m["balancerTag"] = bal; return m }
	dir := func(m map[string]any) map[string]any { m["outboundTag"] = "direct"; return m }
	add := func(role string, m map[string]any) { m["_xsel"] = role; head = append(head, ruleJSON(m)) }

	if st.QUICOff {
		add("quic", map[string]any{"network": "udp", "port": "443", "outboundTag": "block"})
	}
	// п. 24, 25 — блокировки (реклама, детские расписания) идут первыми
	for _, r := range blockRules(st, now) {
		add(r.role, r.m)
	}
	// устройства — по IP-источнику; сортировка для стабильного текста
	ips := make([]string, 0, len(st.Devices))
	for ip := range st.Devices {
		ips = append(ips, ip)
	}
	sort.Strings(ips)
	for _, ip := range ips {
		dv := st.Devices[ip]
		mode := dv.Mode
		if dv.Temp != "" && dv.Until > now.Unix() {
			mode = dv.Temp
		}
		src := []string{ip}
		switch mode {
		case "novpn":
			add("dev", dir(map[string]any{"source": src, "network": "tcp,udp"}))
		case "3":
			add("dev", vpn(map[string]any{"source": src, "network": "tcp,udp"}))
		case "2", "1":
			if len(ss.noDom) > 0 {
				add("dev", dir(map[string]any{"source": src, "domain": ss.noDom}))
			}
			if len(ss.noIP) > 0 {
				add("dev", dir(map[string]any{"source": src, "ip": ss.noIP}))
			}
			if mode == "2" {
				add("dev", vpn(map[string]any{"source": src, "network": "tcp,udp"}))
				continue
			}
			if len(ss.vpnDom) > 0 {
				add("dev", vpn(map[string]any{"source": src, "domain": ss.vpnDom}))
			}
			if len(ss.vpnIP) > 0 {
				add("dev", vpn(map[string]any{"source": src, "ip": ss.vpnIP}))
			}
			add("dev", dir(map[string]any{"source": src, "network": "tcp,udp"}))
		}
	}
	// вся сеть
	if st.Mode == 3 {
		add("all", vpn(map[string]any{"network": "tcp,udp"}))
		return
	}
	if len(ss.noDom) > 0 {
		add("novpn", dir(map[string]any{"domain": ss.noDom}))
	}
	if len(ss.noIP) > 0 {
		add("novpn", dir(map[string]any{"ip": ss.noIP}))
	}
	if st.Mode == 1 {
		if len(ss.vpnDom) > 0 {
			add("vpn", vpn(map[string]any{"domain": ss.vpnDom}))
		}
		if len(ss.vpnIP) > 0 {
			add("vpn", vpn(map[string]any{"ip": ss.vpnIP}))
		}
		return
	}
	// режим 2: свои VPN-сервисы — тоже сразу (быстрее, чем пройти все правила)
	if len(ss.vpnDom) > 0 {
		add("vpn", vpn(map[string]any{"domain": ss.vpnDom}))
	}
	tail = ruleJSON(map[string]any{"_xsel": "rest", "network": "tcp,udp", "balancerTag": bal})
	return
}

// applyManagedText — убрать старые правила панели и вставить новые.
func applyManagedText(s string, st *rulesState, bal string, now time.Time) (string, error) {
	rules, spans, err := parseRulesX(s)
	if err != nil {
		return s, err
	}
	for i := len(rules) - 1; i >= 0; i-- {
		if rules[i].Xsel != "" {
			s = removeSpan(s, spans, i)
		}
	}
	head, tail := buildManaged(st, bal, now)
	if len(head) > 0 {
		rules, spans, err = parseRulesX(s)
		if err != nil {
			return s, err
		}
		at := 0
		for at < len(rules) && isSystemRule(rules[at]) {
			at++
		}
		ind := "      "
		if at < len(spans) {
			ind = lineIndent(s, spans[at][0])
		} else if len(spans) > 0 {
			ind = lineIndent(s, spans[len(spans)-1][0])
		}
		joined := strings.Join(head, ",\n"+ind)
		if s, err = insertRule(s, spans, func() int {
			if at < len(spans) {
				return at
			}
			return -1
		}(), joined); err != nil {
			return s, err
		}
	}
	if tail != "" {
		rules, spans, err = parseRulesX(s)
		if err != nil {
			return s, err
		}
		before := -1
		for i, r := range rules {
			if r.Xsel == "" && isCatchAll(r) {
				before = i
				break
			}
		}
		if s, err = insertRule(s, spans, before, tail); err != nil {
			return s, err
		}
	}
	return s, nil
}

func (a *app) mainBalancer() (string, error) {
	c := a.reload()
	if len(c.groups) == 0 {
		return "", errors.New("в конфиге Xray нет группы серверов (balancer)")
	}
	return c.groups[0].Tag, nil
}

// applyRules — пересобрать правила панели и применить (с проверкой и откатом).
func (a *app) applyRules(st *rulesState, desc string, dry bool) (map[string]any, error) {
	tzOffCache = a.tzOff()
	bal, err := a.mainBalancer()
	if err != nil {
		return nil, err
	}
	file, err := findRoutingFile(*flagConfDir)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	old := string(b)
	nt, err := applyManagedText(old, st, bal, time.Now())
	if err != nil {
		return nil, err
	}
	if nt == old {
		return map[string]any{"ok": true, "nochange": true}, nil
	}
	ch := []cfgChange{{Name: filepath.Base(file), Content: nt}}
	if dry {
		rem, add := diffLines(old, nt)
		terr := testChanges(ch)
		return map[string]any{"ok": terr == nil, "removed": rem, "added": add, "testError": fmtErr(terr)}, nil
	}
	if err := a.applyConfig(desc, ch); err != nil {
		return nil, err
	}
	st.Applied = time.Now().Unix()
	return map[string]any{"ok": true}, nil
}

// ---- авто-разнос существующих правил по сервисам ----

type absorbPlan struct {
	VPN, NoVPN []string
	Moved      []string
}

func (a *app) absorbText(s string) (string, absorbPlan, error) {
	return a.absorbTextWith(s, a.serverSet())
}

func (a *app) absorbTextWith(s string, servers map[string]bool) (string, absorbPlan, error) {
	var p absorbPlan
	rules, spans, err := parseRulesX(s)
	if err != nil {
		return s, p, err
	}
	vpnSet, noSet := map[string]bool{}, map[string]bool{}
	type edit struct {
		i         int
		dom, ip   []string
		dropWhole bool
	}
	var edits []edit
	for i, r := range rules {
		if r.Xsel != "" || len(r.Source) > 0 || len(r.InboundTag) > 0 || len(r.Port) > 0 || r.Off || strings.HasPrefix(r.BalancerTag, "xsel-") {
			continue
		}
		k := ruleKind(r.ruleRaw, servers)
		if k != "vpn" && k != "direct" {
			continue
		}
		keepD, keepI := []string{}, []string{}
		changed := false
		for _, x := range r.Domain {
			if id := matchSvc(x); id != "" && (k == "vpn" || svcByID(id).Group == "novpn") {
				if k == "vpn" {
					vpnSet[id] = true
				} else {
					noSet[id] = true
				}
				p.Moved = append(p.Moved, x)
				changed = true
				continue
			}
			keepD = append(keepD, x)
		}
		for _, x := range r.IP {
			if id := matchSvc(x); id != "" && (k == "vpn" || svcByID(id).Group == "novpn") {
				if k == "vpn" {
					vpnSet[id] = true
				} else {
					noSet[id] = true
				}
				p.Moved = append(p.Moved, x)
				changed = true
				continue
			}
			keepI = append(keepI, x)
		}
		if changed {
			// правило без доменов и IP стало бы «всё подряд» — такое удаляем целиком
			drop := len(keepD) == 0 && len(keepI) == 0 && r.Network == "" && len(r.Protocol) == 0
			edits = append(edits, edit{i, keepD, keepI, drop})
		}
	}
	for i := len(edits) - 1; i >= 0; i-- {
		e := edits[i]
		if e.dropWhole {
			s = removeSpan(s, spans, e.i)
			continue
		}
		s = setArrayInRule(s, spans[e.i][0], spans[e.i][1], "ip", e.ip)
		s = setArrayInRule(s, spans[e.i][0], spans[e.i][1], "domain", e.dom)
	}
	for id := range vpnSet {
		p.VPN = append(p.VPN, id)
	}
	for id := range noSet {
		p.NoVPN = append(p.NoVPN, id)
	}
	sort.Strings(p.VPN)
	sort.Strings(p.NoVPN)
	return s, p, nil
}

// ---- закреплённые IP (п. 17) ----

var reDHCPHost = regexp.MustCompile(`(?m)ip dhcp host\s+(\S+)\s+(\d+\.\d+\.\d+\.\d+)`)

// pinnedIPs — IP, закреплённые за устройствами в Keenetic (ПРОВЕРИТЬ на реальном RCI).
func pinnedIPs() (map[string]bool, error) {
	b, err := rciGet("show/running-config")
	if err != nil {
		return nil, err
	}
	text := string(b)
	var m map[string]any
	if json.Unmarshal(b, &m) == nil {
		if arr, ok := m["message"].([]any); ok {
			var sb strings.Builder
			for _, x := range arr {
				sb.WriteString(fmt.Sprint(x) + "\n")
			}
			text = sb.String()
		}
	}
	out := map[string]bool{}
	for _, mm := range reDHCPHost.FindAllStringSubmatch(text, -1) {
		out[mm[2]] = true
	}
	return out, nil
}

// ---- API ----

func (a *app) handleRules(w http.ResponseWriter, r *http.Request) {
	rulesMu.Lock()
	defer rulesMu.Unlock()
	st := loadRulesState()
	if r.Method != "POST" {
		cat := svcCatalog()
		type svcOut struct {
			ID, Name, Group, Hint string
			N                     int
			Where                 string // vpn | novpn | ""
		}
		where := map[string]string{}
		for _, id := range st.VPN {
			where[id] = "vpn"
		}
		for _, id := range st.NoVPN {
			where[id] = "novpn"
		}
		var svcs []map[string]any
		for _, s := range cat {
			svcs = append(svcs, map[string]any{"id": s.ID, "name": s.Name, "group": s.Group, "hint": s.Hint, "n": len(s.Domain) + len(s.IP), "where": where[s.ID]})
		}
		// существующие правила пользователя: записи, сгруппированные по сервисам
		byService := map[string][]map[string]any{}
		custom := []map[string]any{}
		absorbable := 0
		if file, err := findRoutingFile(*flagConfDir); err == nil {
			if b, err := os.ReadFile(file); err == nil {
				if rules, _, err := parseRulesX(string(b)); err == nil {
					servers := a.serverSet()
					for i, rr := range rules {
						if rr.Xsel != "" || len(rr.InboundTag) > 0 {
							continue
						}
						k := ruleKind(rr.ruleRaw, servers)
						for _, x := range append(append([]string{}, rr.Domain...), rr.IP...) {
							e := map[string]any{"rule": i, "entry": x, "kind": k}
							if id := matchSvc(x); id != "" {
								byService[id] = append(byService[id], e)
								if (k == "vpn" || k == "direct") && len(rr.Source) == 0 && len(rr.Port) == 0 {
									absorbable++
								}
							} else if !isPrivateEntry(x) {
								custom = append(custom, e)
							}
						}
					}
				}
			}
		}
		pinned, perr := pinnedIPs()
		catalog.Lock()
		catInfo := map[string]any{"src": catalog.src, "updated": catalog.updated, "via": catalog.via, "err": catalog.err}
		catalog.Unlock()
		bal, _ := a.mainBalancer()
		writeJSON(w, map[string]any{"groups": svcGroups, "services": svcs, "state": st, "byService": byService, "custom": custom,
			"absorbable": absorbable, "pinned": pinned, "pinnedErr": fmtErr(perr), "catalog": catInfo, "balancer": bal,
			"suggest": a.blockedSuggestions(st)})
		return
	}
	var req struct {
		Action  string   `json:"action"` // save | absorb | device | ignore
		Mode    *int     `json:"mode"`
		VPN     []string `json:"vpn"`
		NoVPN   []string `json:"novpn"`
		QUICOff *bool    `json:"quicOff"`
		Ads     *bool    `json:"ads"`
		Dry     bool     `json:"dry"`
		IP      string   `json:"ip"`
		Dev     *devRule `json:"dev"`
		Minutes int      `json:"minutes"` // временный режим: на сколько минут (0 — снять)
		Temp    string   `json:"temp"`
		Host    string   `json:"host"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		http.Error(w, "bad request", 400)
		return
	}
	fail := func(err error) {
		w.WriteHeader(400)
		writeJSON(w, map[string]any{"error": err.Error()})
	}
	desc := "правила: "
	switch req.Action {
	case "save":
		if req.Mode != nil && *req.Mode >= 1 && *req.Mode <= 3 {
			st.Mode = *req.Mode
		}
		if req.VPN != nil {
			st.VPN = uniq(req.VPN)
		}
		if req.NoVPN != nil {
			st.NoVPN = uniq(req.NoVPN)
		}
		if req.QUICOff != nil {
			st.QUICOff = *req.QUICOff
		}
		if req.Ads != nil {
			st.Ads = *req.Ads
		}
		desc += fmt.Sprintf("«%s», через VPN %d сервисов, без VPN %d", modeName(st.Mode), len(st.VPN), len(st.NoVPN))
	case "device":
		ip := strings.TrimSpace(req.IP)
		if net.ParseIP(ip) == nil {
			fail(errors.New("неверный IP устройства"))
			return
		}
		dv := st.Devices[ip]
		if dv == nil {
			dv = &devRule{}
		}
		if req.Dev != nil {
			keepTemp, keepUntil := dv.Temp, dv.Until
			*dv = *req.Dev
			dv.Temp, dv.Until = keepTemp, keepUntil
		}
		if req.Temp != "" || req.Minutes != 0 {
			if req.Minutes > 0 {
				dv.Temp, dv.Until = req.Temp, time.Now().Unix()+int64(req.Minutes)*60
			} else {
				dv.Temp, dv.Until = "", 0
			}
		}
		if dv.Mode == "" && dv.Temp == "" && len(dv.KidsBlock) == 0 && !dv.Adult && dv.Ads == nil {
			delete(st.Devices, ip)
		} else {
			st.Devices[ip] = dv
		}
		desc += "устройство " + ip + " " + firstNonEmpty(dv.Name, "")
	case "absorb":
		file, err := findRoutingFile(*flagConfDir)
		if err != nil {
			fail(err)
			return
		}
		b, _ := os.ReadFile(file)
		nt, plan, err := a.absorbText(string(b))
		if err != nil {
			fail(err)
			return
		}
		if len(plan.Moved) == 0 {
			writeJSON(w, map[string]any{"ok": true, "nochange": true})
			return
		}
		st2 := *st
		st2.VPN = uniq(append(append([]string{}, st.VPN...), plan.VPN...))
		st2.NoVPN = uniq(append(append([]string{}, st.NoVPN...), plan.NoVPN...))
		bal, err := a.mainBalancer()
		if err != nil {
			fail(err)
			return
		}
		nt2, err := applyManagedText(nt, &st2, bal, time.Now())
		if err != nil {
			fail(err)
			return
		}
		ch := []cfgChange{{Name: filepath.Base(file), Content: nt2}}
		if req.Dry {
			rem, add := diffLines(string(b), nt2)
			terr := testChanges(ch)
			writeJSON(w, map[string]any{"ok": terr == nil, "plan": plan, "removed": rem, "added": add, "testError": fmtErr(terr)})
			return
		}
		if err := a.applyConfig(fmt.Sprintf("правила: %d записей разнесены по сервисам", len(plan.Moved)), ch); err != nil {
			fail(err)
			return
		}
		st2.Absorbed, st2.Applied = true, time.Now().Unix()
		saveRulesState(&st2)
		writeJSON(w, map[string]any{"ok": true, "plan": plan})
		return
	case "ignore":
		st.Ignored = uniq(append(st.Ignored, req.Host))
		saveRulesState(st)
		writeJSON(w, map[string]any{"ok": true})
		return
	default:
		http.Error(w, "bad action", 400)
		return
	}
	res, err := a.applyRules(st, desc, req.Dry)
	if err != nil {
		fail(err)
		return
	}
	if !req.Dry {
		saveRulesState(st)
	}
	writeJSON(w, res)
}

func firstNonEmpty(l ...string) string {
	for _, x := range l {
		if x != "" {
			return x
		}
	}
	return ""
}

// rulesLoop — снять истёкшие временные режимы (п. 23) и переключить детские расписания (п. 24).
func (a *app) rulesLoop() {
	time.Sleep(time.Minute)
	lastSig := ""
	for {
		rulesMu.Lock()
		tzOffCache = a.tzOff()
		st := loadRulesState()
		now := time.Now()
		changed := false
		for ip, dv := range st.Devices {
			if dv.Temp != "" && dv.Until <= now.Unix() {
				dv.Temp, dv.Until = "", 0
				changed = true
				a.watch.mu.Lock()
				a.watch.addEvent("", "info", "временный режим устройства "+firstNonEmpty(dv.Name, ip)+" закончился — вернул обычный")
				a.watch.mu.Unlock()
			}
		}
		sig := scheduleSig(st, now)
		if lastSig == "" {
			lastSig = sig
		}
		if changed || sig != lastSig {
			if _, err := a.applyRules(st, "правила: расписание/временный режим", false); err != nil {
				a.watch.mu.Lock()
				a.watch.addEvent("", "warn", "не удалось обновить правила по расписанию: "+err.Error())
				a.watch.mu.Unlock()
			} else {
				saveRulesState(st)
				lastSig = sig
			}
		}
		rulesMu.Unlock()
		time.Sleep(time.Minute)
	}
}

func modeName(m int) string {
	switch m {
	case 2:
		return "Всё, кроме российского"
	case 3:
		return "Абсолютно всё"
	}
	return "Только выбранное"
}
