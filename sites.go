package main

// Раздел «Сайты»: правила маршрутизации Xray (05_routing.json) — просмотр,
// добавление/удаление/временное отключение доменов и IP, поиск, «куда пойдёт сайт»,
// привязка сайтов к стране выхода. Файл правится точечно (комментарии сохраняются),
// применяется через applyConfig (проверка Xray, копия, откат).

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type ruleView struct {
	Index       int               `json:"index"`
	Domain      []string          `json:"domain,omitempty"`
	IP          []string          `json:"ip,omitempty"`
	OffDomain   []string          `json:"offDomain,omitempty"`
	OffIP       []string          `json:"offIP,omitempty"`
	Outbound    string            `json:"outbound,omitempty"`
	Balancer    string            `json:"balancer,omitempty"`
	Network     string            `json:"network,omitempty"`
	Port        string            `json:"port,omitempty"`
	Inbound     []string          `json:"inbound,omitempty"`
	Protocol    []string          `json:"protocol,omitempty"`
	Source      []string          `json:"source,omitempty"`
	Kind        string            `json:"kind"` // vpn | direct | block | other
	Disabled    bool              `json:"disabled,omitempty"`
	Description string            `json:"description"`
	Managed     string            `json:"managed,omitempty"` // 2.3: правило панели
	System      bool              `json:"system,omitempty"`  // 0.9: служебное (api, локальные адреса, QUIC, «всё остальное»)
	Svc         map[string]string `json:"svc,omitempty"`     // 0.9: запись → название сервиса из каталога
}

type ruleRaw struct {
	Domain      []string        `json:"domain"`
	IP          []string        `json:"ip"`
	OffDomain   []string        `json:"_xsel_off_domain"`
	OffIP       []string        `json:"_xsel_off_ip"`
	OutboundTag string          `json:"outboundTag"`
	BalancerTag string          `json:"balancerTag"`
	Network     string          `json:"network"`
	Port        json.RawMessage `json:"port"`
	InboundTag  []string        `json:"inboundTag"`
	Protocol    []string        `json:"protocol"`
	Source      []string        `json:"source"`
	Off         bool            `json:"_xsel_off"`
	Xsel        string          `json:"_xsel"` // 2.3: правило ведёт панель (раздел «Правила»)
}

// ruleSpans — границы каждого объекта в routing.rules.
func ruleSpans(s string) ([][2]int, error) {
	rk := findKey(s, "rules", 0, len(s))
	if rk < 0 {
		return nil, errors.New("нет routing.rules")
	}
	o := skipWS(s, rk)
	if o >= len(s) || s[o] != '[' {
		return nil, errors.New("routing.rules — не список")
	}
	end := matchClose(s, o)
	var out [][2]int
	for i := o + 1; i < end; {
		i = skipWS(s, i)
		if i >= end {
			break
		}
		if s[i] == '{' {
			e := matchClose(s, i)
			if e < 0 {
				return nil, errors.New("не разобрал правило")
			}
			out = append(out, [2]int{i, e})
			i = e + 1
			continue
		}
		i++
	}
	return out, nil
}

func (a *app) groupTags() map[string]bool {
	c := a.reload()
	m := map[string]bool{}
	for _, g := range c.groups {
		m[g.Tag] = true
	}
	return m
}

func ruleKind(r ruleRaw, servers map[string]bool) string {
	switch {
	case r.OutboundTag == "direct" || strings.HasPrefix(r.OutboundTag, "direct"):
		return "direct"
	case r.OutboundTag == "block" || strings.HasPrefix(r.OutboundTag, "block"):
		return "block"
	case r.BalancerTag != "" || servers[r.OutboundTag]:
		return "vpn"
	}
	return "other"
}

func readRules() (file, text string, rules []ruleRaw, spans [][2]int, err error) {
	file, err = findRoutingFile(*flagConfDir)
	if err != nil {
		return
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return
	}
	text = string(b)
	spans, err = ruleSpans(text)
	if err != nil {
		return
	}
	for _, sp := range spans {
		var r ruleRaw
		if e := json.Unmarshal(stripJSONComments([]byte(text[sp[0]:sp[1]+1])), &r); e != nil {
			err = fmt.Errorf("правило не разбирается: %v", e)
			return
		}
		rules = append(rules, r)
	}
	return
}

func (a *app) serverSet() map[string]bool {
	c := a.reload()
	m := map[string]bool{}
	for _, s := range c.servers {
		m[s.Tag] = true
	}
	return m
}

func describeRule(v ruleView) string {
	var p []string
	if len(v.Inbound) > 0 {
		p = append(p, "вход "+strings.Join(v.Inbound, ", "))
	}
	if v.Network != "" {
		p = append(p, v.Network)
	}
	if v.Port != "" {
		p = append(p, "порт "+v.Port)
	}
	if len(v.Source) > 0 {
		p = append(p, "от "+strings.Join(v.Source, ", "))
	}
	if len(v.Domain)+len(v.OffDomain) > 0 {
		p = append(p, fmt.Sprintf("%d сайтов", len(v.Domain)+len(v.OffDomain)))
	}
	if len(v.IP)+len(v.OffIP) > 0 {
		p = append(p, fmt.Sprintf("%d IP", len(v.IP)+len(v.OffIP)))
	}
	if len(p) == 0 {
		return "всё остальное"
	}
	return strings.Join(p, " · ")
}

func (a *app) listRules() ([]ruleView, string, error) {
	file, _, rules, _, err := readRules()
	if err != nil {
		return nil, "", err
	}
	servers := a.serverSet()
	var out []ruleView
	for i, r := range rules {
		v := ruleView{Index: i, Domain: r.Domain, IP: r.IP, OffDomain: r.OffDomain, OffIP: r.OffIP, Outbound: r.OutboundTag, Balancer: r.BalancerTag,
			Network: r.Network, Inbound: r.InboundTag, Protocol: r.Protocol, Source: r.Source, Kind: ruleKind(r, servers), Disabled: r.Off, Managed: r.Xsel}
		if len(r.Port) > 0 {
			var ps string
			if json.Unmarshal(r.Port, &ps) != nil {
				var pn int
				json.Unmarshal(r.Port, &pn)
				ps = fmt.Sprint(pn)
			}
			v.Port = ps
		}
		v.Description = describeRule(v)
		rx := ruleX{r}
		v.System = isSystemRule(rx) || isCatchAll(rx) || (r.Network == "udp" && v.Port == "443")
		for _, e := range append(append(append(append([]string{}, r.Domain...), r.IP...), r.OffDomain...), r.OffIP...) {
			if id := matchSvc(e); id != "" {
				if s := svcByID(id); s != nil {
					if v.Svc == nil {
						v.Svc = map[string]string{}
					}
					v.Svc[e] = s.Name
				}
			}
		}
		out = append(out, v)
	}
	return out, filepath.Base(file), nil
}

// normalizeEntry: ссылка/домен/IP → запись Xray и поле (domain|ip).
func normalizeEntry(x string) (field, val string) {
	x = strings.TrimSpace(x)
	if x == "" {
		return "", ""
	}
	low := strings.ToLower(x)
	for _, p := range []string{"domain:", "full:", "keyword:", "regexp:", "geosite:", "ext:"} {
		if strings.HasPrefix(low, p) {
			if p == "ext:" && strings.Contains(low, "ip") && !strings.Contains(low, "site") {
				return "ip", x
			}
			return "domain", x
		}
	}
	if strings.HasPrefix(low, "geoip:") {
		return "ip", x
	}
	if strings.Contains(x, "://") {
		if u, err := url.Parse(x); err == nil && u.Hostname() != "" {
			x = u.Hostname()
		}
	}
	if _, _, err := net.ParseCIDR(strings.Trim(x, ". ")); err == nil {
		return "ip", strings.Trim(x, ". ")
	}
	x = strings.Trim(strings.Split(x, "/")[0], ". ")
	if net.ParseIP(x) != nil {
		return "ip", x
	}
	if strings.Contains(x, "/") { // a.b.c.d/nn через split выше не прошёл
		return "ip", x
	}
	x = strings.TrimPrefix(strings.ToLower(x), "www.")
	if x == "" {
		return "", ""
	}
	return "domain", "domain:" + x
}

// setArrayInRule — заменить/добавить массив key внутри правила [st,en].
func setArrayInRule(s string, st, en int, key string, items []string) string {
	if k := findKey(s, key, st+1, en); k >= 0 && len(items) == 0 && strings.HasPrefix(key, "_xsel") {
		// служебный ключ без записей — убрать целиком вместе с запятой
		ks := strings.LastIndex(s[:k], `"`+key+`"`)
		v := skipWS(s, k)
		ve := matchClose(s, v) + 1
		j := skipWS(s, ve)
		if j < en && s[j] == ',' {
			return s[:ks] + strings.TrimLeft(s[j+1:], " \t\r\n")
		}
		i := ks - 1
		for i > st && strings.ContainsRune(" \t\r\n", rune(s[i])) {
			i--
		}
		if s[i] == ',' {
			return s[:i] + s[ve:]
		}
		return s[:ks] + s[ve:]
	}
	if findKey(s, key, st+1, en) >= 0 {
		if len(items) == 0 { // удалить ключ целиком: заменить на пустой массив
			ns, _ := replaceArray(s, key, st+1, en, nil)
			return ns
		}
		ns, _ := replaceArray(s, key, st+1, en, items)
		return ns
	}
	if len(items) == 0 {
		return s
	}
	b, _ := json.Marshal(items)
	inner := strings.TrimSpace(s[st+1 : en])
	sep := ","
	if inner == "" {
		sep = ""
	}
	ind := lineIndent(s, st) + "  "
	return s[:st+1] + "\n" + ind + jsonStr(key) + ": " + string(b) + sep + s[st+1:]
}

type siteOp struct {
	Op     string   `json:"op"`     // add | remove | disable | enable | addTo | bind | unbind | ruleOff | ruleOn
	Rule   int      `json:"rule"`   // индекс правила
	Values []string `json:"values"` // домены/IP/ссылки
	Target string   `json:"target"` // для addTo: vpn | direct | block; для bind: флаг страны
	Dry    bool     `json:"dry"`
	Ops    []siteOp `json:"ops,omitempty"` // 0.9: пачка правок — один перезапуск Xray
	base   string   // текст правил, на котором применять (для пачки)
}

func uniq(l []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range l {
		if x != "" && !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

func minus(l, rm []string) []string {
	m := map[string]bool{}
	for _, x := range rm {
		m[x] = true
	}
	var out []string
	for _, x := range l {
		if !m[x] {
			out = append(out, x)
		}
	}
	return out
}

// mainRuleFor — «основное» правило для vpn/direct/block: с доменами, без inbound/port/source.
func mainRuleFor(rules []ruleRaw, kind string, servers map[string]bool) int {
	for i, r := range rules {
		if r.Xsel == "" && ruleKind(r, servers) == kind && len(r.InboundTag) == 0 && len(r.Port) == 0 && len(r.Source) == 0 && len(r.Protocol) == 0 && !r.Off &&
			(len(r.Domain) > 0 || len(r.OffDomain) > 0) && !strings.HasPrefix(r.BalancerTag, "xsel-") {
			return i
		}
	}
	return -1
}

// lastCatchAll — индекс правила «всё остальное» (без условий), куда вставлять новые перед ним.
func lastCatchAll(rules []ruleRaw) int {
	for i, r := range rules {
		if r.Xsel == "" && len(r.Domain) == 0 && len(r.IP) == 0 && len(r.InboundTag) == 0 && len(r.Port) == 0 && len(r.Source) == 0 && len(r.Protocol) == 0 {
			return i
		}
	}
	return -1
}

// insertRule — вставить новое правило перед правилом с индексом before (или в конец, before<0).
func insertRule(s string, spans [][2]int, before int, obj string) (string, error) {
	if before >= 0 && before < len(spans) {
		st := spans[before][0]
		ind := lineIndent(s, st)
		return s[:st] + obj + ",\n" + ind + s[st:], nil
	}
	rk := findKey(s, "rules", 0, len(s))
	o := skipWS(s, rk)
	e := matchClose(s, o)
	inner := strings.TrimSpace(s[o+1 : e])
	ind := lineIndent(s, o) + "  "
	if inner == "" {
		return s[:o+1] + "\n" + ind + obj + "\n" + lineIndent(s, o) + s[e:], nil
	}
	// после последнего элемента
	last := spans[len(spans)-1][1]
	return s[:last+1] + ",\n" + ind + obj + s[last+1:], nil
}

// editSites — применить операцию к тексту файла правил, вернуть новый текст и описание.
func (a *app) editSites(op siteOp) (file, oldText, newText, desc string, err error) {
	var s string
	var rules []ruleRaw
	var spans [][2]int
	if op.base != "" {
		file, err = findRoutingFile(*flagConfDir)
		if err != nil {
			return
		}
		_, s, rules, spans, err = readRulesText(op.base)
	} else {
		file, s, rules, spans, err = readRules()
	}
	if err != nil {
		return
	}
	oldText = s
	servers := a.serverSet()
	split := func(vals []string) (dom, ip []string) {
		for _, v := range vals {
			for _, part := range strings.FieldsFunc(v, func(r rune) bool { return r == '\n' || r == ',' || r == ' ' || r == ';' }) {
				f, x := normalizeEntry(part)
				if f == "domain" {
					dom = append(dom, x)
				} else if f == "ip" {
					ip = append(ip, x)
				}
			}
		}
		return uniq(dom), uniq(ip)
	}
	dom, ip := split(op.Values)
	if op.Op == "remove" || op.Op == "disable" || op.Op == "enable" { // значения как есть, без нормализации
		dom, ip = nil, nil
		for _, v := range op.Values {
			v = strings.TrimSpace(v)
			if v == "" {
				continue
			}
			if contains(rules[clampIdx(op.Rule, len(rules))].IP, v) || contains(rules[clampIdx(op.Rule, len(rules))].OffIP, v) {
				ip = append(ip, v)
			} else {
				dom = append(dom, v)
			}
		}
	}
	idx := op.Rule
	switch op.Op {
	case "addTo":
		kind := op.Target
		idx = mainRuleFor(rules, kind, servers)
		if idx < 0 {
			// создать новое правило перед «всё остальное»
			var target string
			switch kind {
			case "vpn":
				c := a.reload()
				if len(c.groups) == 0 {
					err = errors.New("нет группы (балансировщика) для VPN")
					return
				}
				target = fmt.Sprintf(`"balancerTag": %s`, jsonStr(c.groups[0].Tag))
			case "direct":
				target = `"outboundTag": "direct"`
			case "block":
				target = `"outboundTag": "block"`
			default:
				err = errors.New("куда добавить?")
				return
			}
			var parts []string
			if len(dom) > 0 {
				b, _ := json.Marshal(dom)
				parts = append(parts, `"domain": `+string(b))
			}
			if len(ip) > 0 {
				b, _ := json.Marshal(ip)
				parts = append(parts, `"ip": `+string(b))
			}
			if len(parts) == 0 {
				err = errors.New("нечего добавлять")
				return
			}
			obj := "{ " + strings.Join(append(parts, target), ", ") + " }"
			newText, err = insertRule(s, spans, lastCatchAll(rules), obj)
			desc = fmt.Sprintf("сайты: новое правило «%s» (%d)", kindName(kind), len(dom)+len(ip))
			return
		}
		fallthrough
	case "add":
		if idx < 0 || idx >= len(rules) {
			err = errors.New("нет такого правила")
			return
		}
		// убрать эти же записи из других правил (чтобы не было противоречий)
		ns := s
		for j := len(rules) - 1; j >= 0; j-- {
			if j == idx {
				continue
			}
			r := rules[j]
			nd, ni := minus(r.Domain, dom), minus(r.IP, ip)
			if len(nd) != len(r.Domain) || len(ni) != len(r.IP) {
				if len(nd) == 0 && len(ni) == 0 {
					continue // правило опустело бы — пусть останется, Xray возьмёт первое совпадение
				}
				sp, _ := ruleSpans(ns)
				if len(nd) != len(r.Domain) {
					ns = setArrayInRule(ns, sp[j][0], sp[j][1], "domain", nd)
					sp, _ = ruleSpans(ns)
				}
				if len(ni) != len(r.IP) {
					ns = setArrayInRule(ns, sp[j][0], sp[j][1], "ip", ni)
				}
			}
		}
		sp, _ := ruleSpans(ns)
		r := rules[idx]
		if len(dom) > 0 {
			ns = setArrayInRule(ns, sp[idx][0], sp[idx][1], "domain", uniq(append(append([]string(nil), r.Domain...), dom...)))
			sp, _ = ruleSpans(ns)
		}
		if len(ip) > 0 {
			ns = setArrayInRule(ns, sp[idx][0], sp[idx][1], "ip", uniq(append(append([]string(nil), r.IP...), ip...)))
		}
		newText = ns
		desc = fmt.Sprintf("сайты: добавлено в «%s»: %s", kindName(ruleKind(r, servers)), short(append(dom, ip...)))
		// записи, оставшиеся в правиле выше, сработают раньше — предупредить
		for j := 0; j < idx; j++ {
			for _, x := range append(dom, ip...) {
				if contains(rules[j].Domain, x) || contains(rules[j].IP, x) {
					desc += fmt.Sprintf(" (внимание: %s остаётся в правиле №%d «%s» выше — оно сработает раньше)", x, j+1, kindName(ruleKind(rules[j], servers)))
				}
			}
		}
	case "remove", "disable", "enable":
		if idx < 0 || idx >= len(rules) {
			err = errors.New("нет такого правила")
			return
		}
		r := rules[idx]
		ns := s
		st, en := spans[idx][0], spans[idx][1]
		upd := func(key string, l []string) {
			ns = setArrayInRule(ns, st, en, key, l)
			sp, _ := ruleSpans(ns)
			st, en = sp[idx][0], sp[idx][1]
		}
		switch op.Op {
		case "remove":
			if len(dom) > 0 {
				upd("domain", minus(r.Domain, dom))
				if len(r.OffDomain) > 0 {
					upd("_xsel_off_domain", minus(r.OffDomain, dom))
				}
			}
			if len(ip) > 0 {
				upd("ip", minus(r.IP, ip))
				if len(r.OffIP) > 0 {
					upd("_xsel_off_ip", minus(r.OffIP, ip))
				}
			}
			desc = "сайты: удалено " + short(append(dom, ip...))
		case "disable":
			if len(dom) > 0 {
				upd("domain", minus(r.Domain, dom))
				upd("_xsel_off_domain", uniq(append(append([]string(nil), r.OffDomain...), dom...)))
			}
			if len(ip) > 0 {
				upd("ip", minus(r.IP, ip))
				upd("_xsel_off_ip", uniq(append(append([]string(nil), r.OffIP...), ip...)))
			}
			desc = "сайты: временно отключено " + short(append(dom, ip...))
		case "enable":
			if len(dom) > 0 {
				upd("_xsel_off_domain", minus(r.OffDomain, dom))
				upd("domain", uniq(append(append([]string(nil), r.Domain...), dom...)))
			}
			if len(ip) > 0 {
				upd("_xsel_off_ip", minus(r.OffIP, ip))
				upd("ip", uniq(append(append([]string(nil), r.IP...), ip...)))
			}
			desc = "сайты: снова включено " + short(append(dom, ip...))
		}
		// правило без доменов и IP стало бы «всё остальное» — так нельзя
		var chk ruleRaw
		sp, _ := ruleSpans(ns)
		json.Unmarshal(stripJSONComments([]byte(ns[sp[idx][0]:sp[idx][1]+1])), &chk)
		if len(chk.Domain) == 0 && len(chk.IP) == 0 && (len(r.Domain) > 0 || len(r.IP) > 0) && len(r.InboundTag) == 0 && len(r.Port) == 0 && r.Network == "" {
			err = errors.New("в правиле не осталось бы ни одного сайта — тогда оно сработает для всего трафика. Оставьте хотя бы одну запись")
			return
		}
		newText = ns
	case "bind":
		newText, desc, err = a.bindSites(s, rules, spans, dom, ip, op.Target)
	case "unbind":
		newText, desc, err = a.unbindCountry(s, op.Target)
	default:
		err = errors.New("неизвестное действие")
	}
	return
}

func clampIdx(i, n int) int {
	if i < 0 || i >= n {
		return 0
	}
	return i
}

func kindName(k string) string {
	switch k {
	case "vpn":
		return "через VPN"
	case "direct":
		return "напрямую"
	case "block":
		return "блокировать"
	}
	return k
}

func short(l []string) string {
	if len(l) <= 3 {
		return strings.Join(l, ", ")
	}
	return strings.Join(l[:3], ", ") + fmt.Sprintf(" и ещё %d", len(l)-3)
}

// ---- привязка сайтов к стране выхода ----

func countryTag(flag string) string {
	var b strings.Builder
	for _, r := range flag {
		if r >= 0x1F1E6 && r <= 0x1F1FF {
			b.WriteRune('a' + (r - 0x1F1E6))
		}
	}
	if b.Len() == 0 {
		return "xsel-" + strings.ToLower(strings.TrimSpace(flag))
	}
	return "xsel-" + b.String()
}

// bindSites — сайты всегда выходят через серверы одной страны (свой балансировщик leastPing).
func (a *app) bindSites(s string, rules []ruleRaw, spans [][2]int, dom, ip []string, flag string) (string, string, error) {
	if len(dom)+len(ip) == 0 || flag == "" {
		return "", "", errors.New("нужны сайты и страна")
	}
	c := a.reload()
	a.mu.Lock()
	var members []string
	for _, sv := range c.servers {
		if countryOf(sv.Tag) == flag && !a.dead[sv.Tag] {
			members = append(members, sv.Tag)
		}
	}
	a.mu.Unlock()
	if len(members) == 0 {
		return "", "", errors.New("нет рабочих серверов этой страны")
	}
	btag := countryTag(flag)
	ns := s
	// 1) балансировщик страны: обновить состав или создать
	if st, en, err := balancerObject(ns, btag); err == nil {
		ns, _ = replaceArray(ns, "selector", st+1, en, members)
	} else {
		bk := findKey(ns, "balancers", 0, len(ns))
		if bk < 0 {
			return "", "", errors.New("нет routing.balancers")
		}
		o := skipWS(ns, bk)
		sel, _ := json.Marshal(members)
		obj := fmt.Sprintf(`{ "tag": %s, "selector": %s, "strategy": { "type": "leastPing" }, "fallbackTag": %s }`, jsonStr(btag), string(sel), jsonStr(members[0]))
		ind := lineIndent(ns, o) + "  "
		inner := strings.TrimSpace(ns[o+1 : matchClose(ns, o)])
		sep := ","
		if inner == "" {
			sep = ""
		}
		ns = ns[:o+1] + "\n" + ind + obj + sep + ns[o+1:]
	}
	// 2) правило: если уже есть правило с этим балансировщиком — дополнить, иначе вставить первым из VPN-правил
	_, _, rules2, spans2, _ := readRulesText(ns)
	ri := -1
	for i, r := range rules2 {
		if r.BalancerTag == btag {
			ri = i
		}
	}
	if ri >= 0 {
		r := rules2[ri]
		if len(dom) > 0 {
			ns = setArrayInRule(ns, spans2[ri][0], spans2[ri][1], "domain", uniq(append(append([]string(nil), r.Domain...), dom...)))
			_, _, _, spans2, _ = readRulesText(ns)
		}
		if len(ip) > 0 {
			ns = setArrayInRule(ns, spans2[ri][0], spans2[ri][1], "ip", uniq(append(append([]string(nil), r.IP...), ip...)))
		}
	} else {
		var parts []string
		if len(dom) > 0 {
			b, _ := json.Marshal(dom)
			parts = append(parts, `"domain": `+string(b))
		}
		if len(ip) > 0 {
			b, _ := json.Marshal(ip)
			parts = append(parts, `"ip": `+string(b))
		}
		obj := "{ " + strings.Join(append(parts, `"balancerTag": `+jsonStr(btag)), ", ") + " }"
		servers := a.serverSet()
		before := -1
		for i, r := range rules2 {
			if ruleKind(r, servers) == "vpn" && len(r.InboundTag) == 0 {
				before = i
				break
			}
		}
		if before < 0 {
			before = lastCatchAll(rules2)
		}
		var err error
		ns, err = insertRule(ns, spans2, before, obj)
		if err != nil {
			return "", "", err
		}
	}
	ns = refreshObservatory(ns)
	return ns, fmt.Sprintf("сайты: %s всегда через %s (%d серверов)", short(append(dom, ip...)), flag, len(members)), nil
}

func (a *app) unbindCountry(s, flag string) (string, string, error) {
	btag := countryTag(flag)
	_, _, rules, spans, _ := readRulesText(s)
	ns := s
	for i := len(rules) - 1; i >= 0; i-- {
		if rules[i].BalancerTag == btag {
			st, en := spans[i][0], spans[i][1]
			// вместе с запятой после/до
			j := skipWS(ns, en+1)
			if j < len(ns) && ns[j] == ',' {
				ns = ns[:st] + ns[j+1:]
			} else {
				k := st - 1
				for k > 0 && strings.ContainsRune(" \t\r\n", rune(ns[k])) {
					k--
				}
				if ns[k] == ',' {
					ns = ns[:k] + ns[en+1:]
				} else {
					ns = ns[:st] + ns[en+1:]
				}
			}
		}
	}
	if st, en, err := balancerObject(ns, btag); err == nil {
		j := skipWS(ns, en+1)
		if j < len(ns) && ns[j] == ',' {
			ns = ns[:st] + ns[j+1:]
		} else {
			k := st - 1
			for k > 0 && strings.ContainsRune(" \t\r\n", rune(ns[k])) {
				k--
			}
			if ns[k] == ',' {
				ns = ns[:k] + ns[en+1:]
			} else {
				ns = ns[:st] + ns[en+1:]
			}
		}
	}
	return refreshObservatory(ns), "сайты: снята привязка к " + flag, nil
}

func readRulesText(s string) (string, string, []ruleRaw, [][2]int, error) {
	spans, err := ruleSpans(s)
	if err != nil {
		return "", s, nil, nil, err
	}
	var rules []ruleRaw
	for _, sp := range spans {
		var r ruleRaw
		json.Unmarshal(stripJSONComments([]byte(s[sp[0]:sp[1]+1])), &r)
		rules = append(rules, r)
	}
	return "", s, rules, spans, nil
}

// refreshObservatory — subjectSelector = объединение selector всех балансировщиков.
func refreshObservatory(s string) string {
	var doc struct {
		Routing struct {
			Balancers []struct {
				Selector []string `json:"selector"`
			} `json:"balancers"`
		} `json:"routing"`
	}
	if json.Unmarshal(stripJSONComments([]byte(s)), &doc) != nil {
		return s
	}
	seen := map[string]bool{}
	var union []string
	for _, b := range doc.Routing.Balancers {
		for _, x := range b.Selector {
			if !seen[x] {
				seen[x] = true
				union = append(union, x)
			}
		}
	}
	for _, k := range []string{"observatory", "burstObservatory"} {
		ok := findKey(s, k, 0, len(s))
		if ok < 0 {
			continue
		}
		o := skipWS(s, ok)
		if o < len(s) && s[o] == '{' {
			if ns, done := replaceArray(s, "subjectSelector", o+1, matchClose(s, o), union); done {
				s = ns
			}
		}
	}
	return s
}

// ---- HTTP ----

func (a *app) handleSites(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		rules, file, err := a.listRules()
		if err != nil {
			writeJSON(w, map[string]any{"error": err.Error()})
			return
		}
		// страны для привязки
		c := a.reload()
		cnt := map[string]int{}
		a.mu.Lock()
		for _, s := range c.servers {
			if !a.dead[s.Tag] {
				cnt[countryOf(s.Tag)]++
			}
		}
		a.mu.Unlock()
		type cc struct {
			Flag string `json:"flag"`
			N    int    `json:"n"`
		}
		var countries []cc
		for f, n := range cnt {
			countries = append(countries, cc{f, n})
		}
		sort.Slice(countries, func(i, j int) bool { return countries[i].N > countries[j].N })
		writeJSON(w, map[string]any{"rules": rules, "file": file, "countries": countries, "backups": listBackups()})
		return
	}
	var op siteOp
	if json.NewDecoder(r.Body).Decode(&op) != nil {
		http.Error(w, "bad request", 400)
		return
	}
	if op.Op == "restore" {
		if err := a.restoreBackup(op.Target); err != nil {
			w.WriteHeader(400)
			writeJSON(w, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, map[string]any{"ok": true})
		return
	}
	var file, oldT, newT, desc string
	var err error
	if len(op.Ops) > 0 {
		file, oldT, newT, desc, err = a.editSitesBatch(op.Ops)
	} else {
		file, oldT, newT, desc, err = a.editSites(op)
	}
	if err != nil {
		w.WriteHeader(400)
		writeJSON(w, map[string]any{"error": err.Error()})
		return
	}
	if newT == oldT {
		writeJSON(w, map[string]any{"ok": true, "nochange": true})
		return
	}
	rem, add := diffLines(oldT, newT)
	if op.Dry {
		err := testChanges([]cfgChange{{Name: filepath.Base(file), Content: newT}})
		writeJSON(w, map[string]any{"ok": err == nil, "desc": desc, "removed": rem, "added": add, "testError": fmtErr(err)})
		return
	}
	if err := a.applyConfig(desc, []cfgChange{{Name: filepath.Base(file), Content: newT}}); err != nil {
		w.WriteHeader(400)
		writeJSON(w, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "desc": desc})
}

// editSitesBatch — применить несколько правок к тексту по очереди. Правки с номером правила
// идут от больших номеров к меньшим (удаление правила не сдвигает остальные), добавления — в конце.
func (a *app) editSitesBatch(ops []siteOp) (file, oldText, newText, desc string, err error) {
	file, oldText, _, _, err = readRules()
	if err != nil {
		return
	}
	idx := make([]int, len(ops))
	for i := range idx {
		idx[i] = i
	}
	byRule := func(o siteOp) bool { return o.Op != "addTo" }
	sort.SliceStable(idx, func(i, j int) bool {
		a1, b1 := ops[idx[i]], ops[idx[j]]
		if byRule(a1) != byRule(b1) {
			return byRule(a1)
		}
		return byRule(a1) && a1.Rule > b1.Rule
	})
	cur := oldText
	var descs []string
	for _, i := range idx {
		o := ops[i]
		o.base, o.Ops, o.Dry = cur, nil, false
		var nt, d string
		if _, _, nt, d, err = a.editSites(o); err != nil {
			return
		}
		cur = nt
		descs = append(descs, d)
	}
	newText = cur
	desc = strings.Join(descs, "; ")
	if len(descs) > 1 {
		desc = fmt.Sprintf("сайты: %d изменений — %s", len(descs), desc)
	}
	return
}
