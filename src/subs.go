package main

// Подписка: скачать список серверов (base64/текст со ссылками vless/vmess/trojan/ss
// или JSON-массив конфигов Xray), собрать outbounds и заменить серверы в
// 04_outbounds.json (direct/block и прочие служебные остаются). Применение — через
// applyConfig (проверка Xray, копия, откат). Срок и остаток трафика — из заголовка
// subscription-userinfo.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type subResult struct {
	Outbounds []map[string]any
	Skipped   []string
	Info      map[string]int64
	Via       string
	Meta      subMeta
}

func b64any(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	s = strings.NewReplacer("\n", "", "\r", "", " ", "").Replace(s)
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, nil
		}
	}
	return nil, errors.New("не base64")
}

// streamFromQuery — streamSettings из параметров ссылки (vless/trojan).
func streamFromQuery(q url.Values, defSec string) map[string]any {
	netw := q.Get("type")
	if netw == "" {
		netw = "tcp"
	}
	if netw == "tcp" {
		netw = "raw"
	}
	sec := q.Get("security")
	if sec == "" {
		sec = defSec
	}
	st := map[string]any{"network": netw, "security": sec}
	alpn := []string{}
	for _, x := range strings.Split(q.Get("alpn"), ",") {
		if x = strings.TrimSpace(x); x != "" {
			alpn = append(alpn, x)
		}
	}
	fp := q.Get("fp")
	switch sec {
	case "tls":
		t := map[string]any{}
		if v := q.Get("sni"); v != "" {
			t["serverName"] = v
		}
		if fp != "" {
			t["fingerprint"] = fp
		}
		if len(alpn) > 0 {
			t["alpn"] = alpn
		}
		if v := q.Get("pcs"); v != "" {
			t["pinnedPeerCertSha256"] = v
		}
		st["tlsSettings"] = t
	case "reality":
		t := map[string]any{"serverName": q.Get("sni"), "publicKey": q.Get("pbk"), "shortId": q.Get("sid")}
		if fp == "" {
			fp = "chrome"
		}
		t["fingerprint"] = fp
		if v := q.Get("spx"); v != "" {
			t["spiderX"] = v
		}
		if v := q.Get("pqv"); v != "" {
			t["mldsa65Verify"] = v
		}
		st["realitySettings"] = t
	}
	path, host := q.Get("path"), q.Get("host")
	switch netw {
	case "ws":
		w := map[string]any{"path": orDef(path, "/")}
		if host != "" {
			w["host"] = host
		}
		st["wsSettings"] = w
	case "httpupgrade":
		w := map[string]any{"path": orDef(path, "/")}
		if host != "" {
			w["host"] = host
		}
		st["httpupgradeSettings"] = w
	case "grpc":
		g := map[string]any{"serviceName": q.Get("serviceName")}
		if q.Get("mode") == "multi" {
			g["multiMode"] = true
		}
		if v := q.Get("authority"); v != "" {
			g["authority"] = v
		}
		st["grpcSettings"] = g
	case "xhttp", "splithttp":
		st["network"] = "xhttp"
		x := map[string]any{"path": orDef(path, "/")}
		if host != "" {
			x["host"] = host
		}
		if v := q.Get("mode"); v != "" {
			x["mode"] = v
		}
		if v := q.Get("extra"); v != "" {
			var e any
			if json.Unmarshal([]byte(v), &e) == nil {
				x["extra"] = e
			}
		}
		st["xhttpSettings"] = x
	case "raw":
		if q.Get("headerType") == "http" {
			req := map[string]any{"path": []string{orDef(path, "/")}}
			if host != "" {
				req["headers"] = map[string]any{"Host": strings.Split(host, ",")}
			}
			st["rawSettings"] = map[string]any{"header": map[string]any{"type": "http", "request": req}}
		}
	}
	return st
}

func orDef(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func linkName(u *url.URL, def string) string {
	if u.Fragment != "" {
		return strings.TrimSpace(u.Fragment)
	}
	return def
}

// parseLink — одна ссылка → outbound Xray.
func parseLink(link string) (map[string]any, error) {
	link = strings.TrimSpace(link)
	switch {
	case strings.HasPrefix(link, "vmess://"):
		b, err := b64any(link[8:])
		if err != nil {
			return nil, err
		}
		var v map[string]any
		if err := json.Unmarshal(b, &v); err != nil {
			return nil, err
		}
		str := func(k string) string { return strings.TrimSpace(fmt.Sprint(orNil(v[k]))) }
		port, _ := strconv.Atoi(str("port"))
		q := url.Values{}
		q.Set("type", str("net"))
		q.Set("security", map[bool]string{true: "tls", false: "none"}[str("tls") == "tls"])
		q.Set("sni", str("sni"))
		q.Set("host", str("host"))
		q.Set("path", str("path"))
		q.Set("fp", str("fp"))
		q.Set("alpn", str("alpn"))
		if str("net") == "grpc" {
			q.Set("serviceName", str("path"))
			q.Set("mode", str("type"))
		} else if str("type") == "http" {
			q.Set("headerType", "http")
		}
		scy := str("scy")
		if scy == "" {
			scy = "auto"
		}
		return map[string]any{"tag": orDef(str("ps"), str("add")), "protocol": "vmess",
			"settings":       map[string]any{"vnext": []any{map[string]any{"address": str("add"), "port": port, "users": []any{map[string]any{"id": str("id"), "security": scy}}}}},
			"streamSettings": streamFromQuery(q, "none")}, nil
	case strings.HasPrefix(link, "vless://"), strings.HasPrefix(link, "trojan://"):
		u, err := url.Parse(link)
		if err != nil {
			return nil, err
		}
		port, _ := strconv.Atoi(u.Port())
		q := u.Query()
		if strings.HasPrefix(link, "vless://") {
			user := map[string]any{"id": u.User.Username(), "encryption": orDef(q.Get("encryption"), "none")}
			if f := q.Get("flow"); f != "" {
				user["flow"] = f
			}
			return map[string]any{"tag": linkName(u, u.Hostname()), "protocol": "vless",
				"settings":       map[string]any{"vnext": []any{map[string]any{"address": u.Hostname(), "port": port, "users": []any{user}}}},
				"streamSettings": streamFromQuery(q, "none")}, nil
		}
		return map[string]any{"tag": linkName(u, u.Hostname()), "protocol": "trojan",
			"settings":       map[string]any{"servers": []any{map[string]any{"address": u.Hostname(), "port": port, "password": u.User.Username()}}},
			"streamSettings": streamFromQuery(q, "tls")}, nil
	case strings.HasPrefix(link, "ss://"):
		rest := link[5:]
		name := ""
		if i := strings.Index(rest, "#"); i >= 0 {
			name, _ = url.PathUnescape(rest[i+1:])
			rest = rest[:i]
		}
		if i := strings.Index(rest, "?"); i >= 0 {
			rest = rest[:i]
		}
		var method, pass, hostport string
		if at := strings.LastIndex(rest, "@"); at >= 0 {
			cred := rest[:at]
			hostport = rest[at+1:]
			if b, err := b64any(cred); err == nil {
				cred = string(b)
			} else if c, err := url.PathUnescape(cred); err == nil {
				cred = c
			}
			method, pass, _ = strings.Cut(cred, ":")
		} else {
			b, err := b64any(rest)
			if err != nil {
				return nil, err
			}
			s := string(b)
			at := strings.LastIndex(s, "@")
			if at < 0 {
				return nil, errors.New("ss: нет @")
			}
			method, pass, _ = strings.Cut(s[:at], ":")
			hostport = s[at+1:]
		}
		hostport = strings.TrimSuffix(hostport, "/")
		h, p, ok := strings.Cut(hostport, ":")
		if !ok {
			return nil, errors.New("ss: нет порта")
		}
		h = strings.Trim(h, "[]")
		port, _ := strconv.Atoi(p)
		return map[string]any{"tag": orDef(name, h), "protocol": "shadowsocks",
			"settings": map[string]any{"servers": []any{map[string]any{"address": h, "port": port, "method": method, "password": pass}}}}, nil
	}
	sch := link
	if i := strings.Index(link, "://"); i > 0 {
		sch = link[:i]
	}
	return nil, errors.New("не поддерживается: " + sch)
}

func orNil(v any) any {
	if v == nil {
		return ""
	}
	if f, ok := v.(float64); ok {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return v
}

// parseSubBody — тело ответа подписки → outbounds.
func parseSubBody(body []byte) ([]map[string]any, []string, error) {
	t := strings.TrimSpace(string(body))
	if strings.HasPrefix(t, "[") || strings.HasPrefix(t, "{") {
		var arr []map[string]any
		if strings.HasPrefix(t, "{") {
			var one map[string]any
			if err := json.Unmarshal([]byte(t), &one); err != nil {
				return nil, nil, err
			}
			arr = []map[string]any{one}
		} else if err := json.Unmarshal([]byte(t), &arr); err != nil {
			return nil, nil, err
		}
		var out []map[string]any
		for _, cfg := range arr {
			name, _ := cfg["remarks"].(string)
			obs, _ := cfg["outbounds"].([]any)
			var pick map[string]any
			for _, o := range obs {
				m, _ := o.(map[string]any)
				p, _ := m["protocol"].(string)
				if p == "vless" || p == "vmess" || p == "trojan" || p == "shadowsocks" || p == "hysteria" || p == "wireguard" {
					if pick == nil || m["tag"] == "proxy" {
						pick = m
					}
				}
			}
			if pick == nil {
				continue
			}
			if name != "" {
				pick["tag"] = name
			}
			delete(pick, "mux")
			out = append(out, pick)
		}
		if len(out) == 0 {
			return nil, nil, errors.New("в JSON-подписке нет серверов")
		}
		return out, nil, nil
	}
	if !strings.Contains(t, "://") {
		if b, err := b64any(t); err == nil {
			t = string(b)
		}
	}
	var out []map[string]any
	var skipped []string
	for _, l := range strings.Split(t, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") || !strings.Contains(l, "://") {
			continue
		}
		ob, err := parseLink(l)
		if err != nil {
			skipped = append(skipped, err.Error())
			continue
		}
		out = append(out, ob)
	}
	if len(out) == 0 {
		return nil, skipped, errors.New("в подписке не найдено ни одного сервера")
	}
	return out, skipped, nil
}

func parseUserinfo(h string) map[string]int64 {
	out := map[string]int64{}
	for _, part := range strings.Split(h, ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if ok {
			n, _ := strconv.ParseFloat(strings.TrimSpace(v), 64)
			out[strings.ToLower(k)] = int64(n)
		}
	}
	return out
}

// fetchSub — скачать подписку: сначала через текущий VPN-сервер, потом напрямую.
func (a *app) fetchSub(u, ua string) (*subResult, error) {
	try := func(cl *http.Client) (*subResult, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", ua)
		resp, err := cl.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		if err != nil {
			return nil, err
		}
		obs, skipped, err := parseSubBody(body)
		if err != nil {
			return nil, err
		}
		meta := parseSubMeta(resp.Header)
		obs, meta.Informers = splitInformers(obs)
		return &subResult{Outbounds: obs, Skipped: skipped, Info: parseUserinfo(resp.Header.Get("subscription-userinfo")), Meta: meta}, nil
	}
	var errs []string
	c := a.reload()
	a.mu.Lock()
	active := ""
	for _, g := range c.groups {
		if v := a.desired[g.Tag]; v != "" && !isReserveTag(v) {
			active = v
		}
	}
	a.mu.Unlock()
	if active != "" {
		if ports, err := a.wprobe.ensure(c, a.watchProbeTags()); err == nil && ports[active] > 0 {
			cl, tr := socksClient(ports[active])
			cl.CheckRedirect = nil
			r, err := try(cl)
			tr.CloseIdleConnections()
			if err == nil {
				r.Via = "через " + active
				return r, nil
			}
			errs = append(errs, "через VPN: "+explainNetErr(err))
		}
	}
	r, err := try(&http.Client{Timeout: 25 * time.Second})
	if err == nil {
		r.Via = "напрямую"
		return r, nil
	}
	errs = append(errs, "напрямую: "+explainNetErr(err))
	return nil, errors.New(strings.Join(errs, "; "))
}

// outboundsFile — файл, где лежат серверы (больше всего vless/vmess/trojan/ss).
func outboundsFile() (string, error) {
	files, _ := filepath.Glob(filepath.Join(*flagConfDir, "*.json"))
	best, bestN := "", -1
	for _, f := range files {
		if filepath.Base(f) == reserveFile {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var d struct {
			Outbounds []struct {
				Protocol string `json:"protocol"`
			} `json:"outbounds"`
		}
		if json.Unmarshal(stripJSONComments(b), &d) != nil {
			continue
		}
		n := 0
		for _, o := range d.Outbounds {
			switch o.Protocol {
			case "vless", "vmess", "trojan", "shadowsocks", "hysteria", "wireguard", "socks", "http":
				n++
			}
		}
		if n > bestN && len(d.Outbounds) > 0 {
			best, bestN = f, n
		}
	}
	if best == "" {
		// Свежая установка XKeen: конфиги — заготовки с одними комментариями,
		// массива outbounds нет ни в одном файле. Серверы из подписки всё равно
		// нужно куда-то положить, поэтому берём штатный 04_outbounds.json —
		// если его нет, planSub создаст файл при записи.
		if _, err := os.Stat(*flagConfDir); err != nil {
			return "", errors.New("не нашёл папку конфигов Xray: " + *flagConfDir)
		}
		return filepath.Join(*flagConfDir, "04_outbounds.json"), nil
	}
	return best, nil
}

type subPlan struct {
	File    string   `json:"file"`
	Added   []string `json:"added"`
	Removed []string `json:"removed"`
	Changed []string `json:"changed"`
	Same    int      `json:"same"`
	Skipped []string `json:"skipped"`
	Via     string   `json:"via"`
	content string
}

// planSub — новое содержимое файла серверов и что изменится.
func planSub(r *subResult) (*subPlan, error) {
	f, err := outboundsFile()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(f)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	var doc map[string]json.RawMessage
	if len(b) > 0 {
		if err := json.Unmarshal(stripJSONComments(b), &doc); err != nil {
			return nil, err
		}
	}
	var old []map[string]any
	json.Unmarshal(doc["outbounds"], &old)
	oldProxy := map[string]string{}
	var keep []map[string]any
	for _, o := range old {
		p, _ := o["protocol"].(string)
		tag, _ := o["tag"].(string)
		switch p {
		case "vless", "vmess", "trojan", "shadowsocks", "hysteria", "wireguard":
			jb, _ := json.Marshal(o)
			oldProxy[tag] = string(jb)
		default:
			keep = append(keep, o)
		}
	}
	// уникальные теги
	used := map[string]int{}
	for _, o := range keep {
		if t, _ := o["tag"].(string); t != "" {
			used[t]++
		}
	}
	plan := &subPlan{File: filepath.Base(f), Skipped: r.Skipped, Via: r.Via}
	newTags := map[string]bool{}
	var all []any
	for _, o := range r.Outbounds {
		tag, _ := o["tag"].(string)
		tag = strings.TrimSpace(tag)
		if tag == "" {
			tag = "server"
		}
		base := tag
		for used[tag] > 0 {
			used[base]++
			tag = fmt.Sprintf("%s #%d", base, used[base])
		}
		used[tag]++
		o["tag"] = tag
		newTags[tag] = true
		jb, _ := json.Marshal(o)
		if prev, ok := oldProxy[tag]; !ok {
			plan.Added = append(plan.Added, tag)
		} else if !sameJSON(prev, string(jb)) {
			plan.Changed = append(plan.Changed, tag)
		} else {
			plan.Same++
		}
		all = append(all, o)
	}
	for t := range oldProxy {
		if !newTags[t] {
			plan.Removed = append(plan.Removed, t)
		}
	}
	sort.Strings(plan.Removed)
	for _, o := range keep {
		all = append(all, o)
	}
	// Правила панели ссылаются на теги direct и block. На свежем XKeen
	// (в 04_outbounds.json был только комментарий) их нет, и Xray не стартует
	// с «could not find outbound: direct» — добавляем стандартные.
	for _, std := range []struct{ tag, proto string }{{"direct", "freedom"}, {"block", "blackhole"}} {
		exists := false
		for _, o := range all {
			if m, ok := o.(map[string]any); ok {
				if t, _ := m["tag"].(string); t == std.tag {
					exists = true
					break
				}
			}
		}
		if !exists {
			all = append(all, map[string]any{"tag": std.tag, "protocol": std.proto})
		}
	}
	doc2 := map[string]any{"outbounds": all}
	out, _ := json.MarshalIndent(doc2, "", "  ")
	plan.content = string(out)
	return plan, nil
}

func sameJSON(a, b string) bool {
	var x, y any
	json.Unmarshal([]byte(a), &x)
	json.Unmarshal([]byte(b), &y)
	xa, _ := json.Marshal(x)
	ya, _ := json.Marshal(y)
	return string(xa) == string(ya)
}

func (a *app) subUpdate(apply bool, why string) (*subPlan, error) {
	a.watch.mu.Lock()
	st := a.watch.settings
	a.watch.mu.Unlock()
	if st.SubURL == "" {
		return nil, errors.New("ссылка на подписку не задана")
	}
	r, err := a.fetchSub(st.SubURL, st.SubUA)
	now := time.Now().Unix()
	a.watch.mu.Lock()
	a.watch.settings.SubChecked = now
	if err != nil {
		a.watch.settings.SubErr = err.Error()
	} else {
		a.watch.settings.SubErr = ""
		s := &a.watch.settings
		if len(r.Info) > 0 {
			s.SubUp, s.SubDown, s.SubTotal, s.SubExpire = r.Info["upload"], r.Info["download"], r.Info["total"], r.Info["expire"]
		}
		s.SubTitle, s.SubSupport, s.SubWebPage, s.SubUpdateH, s.SubInformers = r.Meta.Title, r.Meta.Support, r.Meta.WebPage, r.Meta.UpdateH, r.Meta.Informers
		s.SubInfExpire, s.SubInfLeft = infoFromInformers(r.Meta.Informers, time.Now())
	}
	a.watch.mu.Unlock()
	a.mu.Lock()
	a.saveStateLocked()
	a.mu.Unlock()
	if err != nil {
		return nil, err
	}
	plan, err := planSub(r)
	if err != nil {
		return nil, err
	}
	if !apply || len(plan.Added)+len(plan.Removed)+len(plan.Changed) == 0 {
		if !apply {
			if terr := testChanges([]cfgChange{{Name: plan.File, Content: plan.content}}); terr != nil {
				return plan, errors.New("Xray не принял бы новый список: " + terr.Error())
			}
		}
		return plan, nil
	}
	desc := fmt.Sprintf("подписка%s: +%d новых, −%d убрано, %d изменено", why, len(plan.Added), len(plan.Removed), len(plan.Changed))
	if err := a.applyConfig(desc, []cfgChange{{Name: plan.File, Content: plan.content}}); err != nil {
		return plan, err
	}
	a.watch.mu.Lock()
	a.watch.settings.SubApplied = time.Now().Unix()
	a.watch.mu.Unlock()
	// выбранный сервер исчез из подписки — пусть сторож выберет заново
	c := a.reload()
	a.mu.Lock()
	for g, t := range a.desired {
		if t != "" && !isReserveTag(t) && !isServerTagIn(c, t) {
			delete(a.desired, g)
		}
	}
	a.saveStateLocked()
	a.mu.Unlock()
	select {
	case a.watch.kick <- struct{}{}:
	default:
	}
	return plan, nil
}

// subLoop — автообновление подписки и предупреждение о её окончании.
func (a *app) subLoop() {
	time.Sleep(2 * time.Minute)
	for {
		a.watch.mu.Lock()
		st := a.watch.settings
		a.watch.mu.Unlock()
		now := time.Now().Unix()
		if st.SubURL != "" && st.SubAuto && now-st.SubChecked >= int64(st.SubEveryH)*3600 {
			if _, err := a.subUpdate(true, " (по расписанию)"); err != nil {
				a.watch.mu.Lock()
				a.watch.addEvent("", "warn", "обновление подписки не удалось: "+err.Error())
				a.watch.mu.Unlock()
			}
		}
		// предупреждение: срок < 3 дней или трафика < 10% — раз в сутки, в личку
		a.watch.mu.Lock()
		s := &a.watch.settings
		msg, short := "", ""
		exp, _ := subEffective(*s)
		if exp > 0 && exp-now < 3*86400 && exp > now {
			msg = fmt.Sprintf("подписка заканчивается через %s — продлите", fmtLeft(exp-now))
			short = "⏳ Подписка VPN заканчивается через " + fmtLeft(exp-now)
		} else if exp > 0 && exp <= now {
			msg, short = "срок подписки истёк", "⛔ Срок подписки VPN истёк"
		}
		if s.SubTotal > 0 && s.SubUp+s.SubDown > s.SubTotal*8/10 { // 80 % трафика
			t := fmt.Sprintf("трафика подписки осталось %s из %s", fmtBytes(s.SubTotal-s.SubUp-s.SubDown), fmtBytes(s.SubTotal))
			msg = strings.TrimPrefix(msg+"; ", "; ") + t
			short = strings.TrimPrefix(short+"; ", "; ") + "📶 " + t
		}
		if msg != "" && now-s.SubWarned > 86400 {
			s.SubWarned = now
			a.watch.addEventShort("", "warn", msg, short)
		}
		a.watch.mu.Unlock()
		time.Sleep(10 * time.Minute)
	}
}

func (a *app) handleSub(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		a.watch.mu.Lock()
		s := a.watch.settings
		a.watch.mu.Unlock()
		eff, effSrc := subEffective(s)
		writeJSON(w, map[string]any{"url": s.SubURL, "ua": s.SubUA, "auto": s.SubAuto, "everyH": s.SubEveryH, "up": s.SubUp, "down": s.SubDown,
			"total": s.SubTotal, "expire": s.SubExpire, "checked": s.SubChecked, "applied": s.SubApplied, "err": s.SubErr,
			"title": s.SubTitle, "support": s.SubSupport, "webPage": s.SubWebPage, "updateH": s.SubUpdateH, "informers": s.SubInformers,
			"infExpire": s.SubInfExpire, "infLeft": s.SubInfLeft, "paidUntil": s.SubPaidUntil, "effExpire": eff, "effSrc": effSrc})
		return
	}
	var req struct {
		URL    *string `json:"url"`
		UA     *string `json:"ua"`
		Auto   *bool   `json:"auto"`
		EveryH *int    `json:"everyH"`
		Paid   *int64  `json:"paidUntil"` // ручное «Оплачено до» (unix), 0 — убрать
		Action string  `json:"action"`    // save | preview | apply
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		http.Error(w, "bad request", 400)
		return
	}
	a.watch.mu.Lock()
	s := &a.watch.settings
	if req.URL != nil {
		s.SubURL = strings.TrimSpace(*req.URL)
	}
	if req.UA != nil {
		s.SubUA = strings.TrimSpace(*req.UA)
	}
	if req.Auto != nil {
		s.SubAuto = *req.Auto
	}
	if req.EveryH != nil && *req.EveryH > 0 {
		s.SubEveryH = *req.EveryH
	}
	if req.Paid != nil && *req.Paid >= 0 {
		s.SubPaidUntil = *req.Paid
		s.SubWarned = 0
	}
	s.normalize()
	a.watch.mu.Unlock()
	a.mu.Lock()
	a.saveStateLocked()
	a.mu.Unlock()
	if req.Action == "preview" || req.Action == "apply" {
		plan, err := a.subUpdate(req.Action == "apply", " (вручную)")
		resp := map[string]any{"plan": plan}
		if err != nil {
			resp["error"] = err.Error()
			w.WriteHeader(400)
		}
		writeJSON(w, resp)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}
