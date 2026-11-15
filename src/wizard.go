package main

// 2.3, п. 8: мастер первого запуска.
//   GET  /api/wizard            — проверка роутера и что уже настроено;
//   POST {action:"check", source:"xkeen"|"input", input} — живая проверка серверов (фоном, прогресс по GET ?job=1);
//   POST {action:"pick"}        — подбор 6 разных лучших (сервисы + скорость), фоном;
//   POST {action:"apply-servers", input} — записать серверы из подписки/ссылок (проверка, копия, откат);
//   POST {action:"done"}        — мастер пройден.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
)

// сервисы для таблицы мастера
var wizTargets = []watchTarget{
	{Name: "YT", URL: "https://www.youtube.com/generate_204"},
	{Name: "WA", URL: "https://web.whatsapp.com/"},
	{Name: "TG", URL: "https://web.telegram.org/"},
	{Name: "IG", URL: "https://www.instagram.com/"},
	{Name: "GPT", URL: "https://chatgpt.com/"},
	{Name: "Claude", URL: "https://claude.ai/"},
}

type wizRow struct {
	Tag   string          `json:"tag"`
	Proto string          `json:"proto"`
	Alive bool            `json:"alive"`
	Ms    int64           `json:"ms"`
	Svc   map[string]bool `json:"svc"`
	OKN   int             `json:"okn"`
	Kbps  int             `json:"kbps,omitempty"`
	Err   string          `json:"err,omitempty"`
}

type wizJob struct {
	Kind    string    `json:"kind"` // check | pick
	Running bool      `json:"running"`
	Done    int       `json:"done"`
	Total   int       `json:"total"`
	Rows    []*wizRow `json:"rows"`
	Picked  []string  `json:"picked,omitempty"`
	Err     string    `json:"err,omitempty"`
	Source  string    `json:"source"`
	Started int64     `json:"started"`
}

var wiz struct {
	sync.Mutex
	job   *wizJob
	probe *probe
	conf  *xrayConf // серверы последней проверки (для «из ввода»)
	input string
}

func confFromOutbounds(obs []map[string]any) *xrayConf {
	c := &xrayConf{stamp: fmt.Sprintf("wiz-%d", time.Now().UnixNano())}
	for _, ob := range obs {
		tag, _ := ob["tag"].(string)
		proto, _ := ob["protocol"].(string)
		raw, _ := json.Marshal(ob)
		c.rawOutbound = append(c.rawOutbound, raw)
		addr, port := obAddr(ob)
		net := ""
		if ss, ok := ob["streamSettings"].(map[string]any); ok {
			net, _ = ss["network"].(string)
		}
		c.servers = append(c.servers, server{Tag: tag, Protocol: proto, Address: addr, Port: port, Network: net})
		c.allTags = append(c.allTags, tag)
	}
	return c
}

// wizParse — серверы из того, что ввёл пользователь: ссылка на подписку или строки vless://…
func (a *app) wizParse(input string) ([]map[string]any, error) {
	in := strings.TrimSpace(input)
	if in == "" {
		return nil, errors.New("вставьте ссылку на подписку или ссылки на серверы")
	}
	if strings.HasPrefix(in, "http://") || strings.HasPrefix(in, "https://") {
		if !strings.Contains(in, "\n") {
			a.watch.mu.Lock()
			ua := a.watch.settings.SubUA
			a.watch.mu.Unlock()
			b, h, _, err := a.smartGet(in, ua, 8<<20, 25*time.Second)
			if err != nil {
				return nil, errors.New("не удалось скачать подписку: " + err.Error())
			}
			obs, _, err := parseSubBody(b)
			if err != nil {
				return nil, err
			}
			obs, _ = splitInformers(obs)
			_ = h
			return obs, nil
		}
	}
	obs, skipped, err := parseSubBody([]byte(in))
	if err != nil {
		if len(skipped) > 0 {
			err = fmt.Errorf("%v (%s)", err, strings.Join(skipped, "; "))
		}
		return nil, err
	}
	obs, _ = splitInformers(obs)
	return obs, nil
}

func (a *app) wizRunCheck(c *xrayConf, kind, source string, withSpeed bool) {
	wiz.Lock()
	if wiz.probe == nil {
		wiz.probe = &probe{name: " (мастер)", base: *flagPortBase + 1500}
	}
	pr := wiz.probe
	job := &wizJob{Kind: kind, Running: true, Total: len(c.servers), Source: source, Started: time.Now().Unix()}
	for _, s := range c.servers {
		job.Rows = append(job.Rows, &wizRow{Tag: s.Tag, Proto: strings.Trim(s.Protocol+" "+s.Network, " "), Svc: map[string]bool{}})
	}
	wiz.job = job
	wiz.Unlock()
	finish := func(err error) {
		wiz.Lock()
		job.Running = false
		if err != nil {
			job.Err = err.Error()
		}
		wiz.Unlock()
		pr.mu.Lock()
		pr.stopLocked()
		pr.mu.Unlock()
	}
	if len(c.servers) == 0 {
		finish(errors.New("серверов не найдено"))
		return
	}
	ports, err := pr.ensure(c, nil)
	if err != nil {
		finish(err)
		return
	}
	par := 8
	if guard.level() > 0 {
		par = 3 // роутер занят — по чуть-чуть
	}
	sem := make(chan struct{}, par)
	var wg sync.WaitGroup
	for _, row := range job.Rows {
		port := ports[row.Tag]
		if port == 0 {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(row *wizRow, port int) {
			defer wg.Done()
			defer func() { <-sem }()
			ms, err := testDelay(port)
			wiz.Lock()
			row.Alive, row.Ms = err == nil, ms
			if err != nil {
				row.Err = cleanErr(err).Error()
			}
			wiz.Unlock()
			if err == nil {
				for _, t := range wizTargets {
					r := checkURL(port, t.URL, 7*time.Second)
					wiz.Lock()
					row.Svc[t.Name] = r.OK
					if r.OK {
						row.OKN++
					}
					wiz.Unlock()
				}
			}
			wiz.Lock()
			job.Done++
			wiz.Unlock()
		}(row, port)
	}
	wg.Wait()
	if withSpeed { // п. 8.5: скорость для лучших 12
		wiz.Lock()
		cand := append([]*wizRow(nil), job.Rows...)
		wiz.Unlock()
		sort.Slice(cand, func(i, j int) bool {
			if cand[i].OKN != cand[j].OKN {
				return cand[i].OKN > cand[j].OKN
			}
			return cand[i].Ms < cand[j].Ms
		})
		a.watch.mu.Lock()
		spURL := a.watch.settings.SpeedURL
		a.watch.mu.Unlock()
		for i, r := range cand {
			if i >= 12 || !r.Alive || r.OKN == 0 || spURL == "" {
				break
			}
			if k, err := speedTest(ports[r.Tag], spURL, 12*time.Second); err == nil {
				wiz.Lock()
				r.Kbps = k
				wiz.Unlock()
			}
		}
		wiz.Lock()
		job.Picked = pickDiverse(job.Rows, 6)
		wiz.Unlock()
	}
	finish(nil)
}

// pickDiverse — n лучших, по возможности из разных стран и хотя бы один gRPC.
func pickDiverse(rows []*wizRow, n int) []string {
	l := []*wizRow{}
	for _, r := range rows {
		if r.Alive && r.OKN > 0 {
			l = append(l, r)
		}
	}
	sort.Slice(l, func(i, j int) bool {
		if l[i].OKN != l[j].OKN {
			return l[i].OKN > l[j].OKN
		}
		if (l[i].Kbps > 0) != (l[j].Kbps > 0) || abs(l[i].Kbps-l[j].Kbps) > 3000 {
			return l[i].Kbps > l[j].Kbps
		}
		return l[i].Ms < l[j].Ms
	})
	var out []string
	usedCC := map[string]bool{}
	take := func(r *wizRow) {
		out = append(out, r.Tag)
		_, cc, _ := flagCC(r.Tag)
		usedCC[cc] = true
	}
	has := func(t string) bool { return contains(out, t) }
	// хотя бы один gRPC
	for _, r := range l {
		if strings.Contains(r.Proto, "grpc") {
			take(r)
			break
		}
	}
	for _, r := range l { // разные страны
		if len(out) >= n {
			break
		}
		_, cc, _ := flagCC(r.Tag)
		if !has(r.Tag) && (cc == "" || !usedCC[cc]) {
			take(r)
		}
	}
	for _, r := range l { // добрать
		if len(out) >= n {
			break
		}
		if !has(r.Tag) {
			take(r)
		}
	}
	return out
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

type wizCheck struct {
	Name string `json:"name"`
	OK   bool   `json:"ok"`
	Info string `json:"info"`
	Warn bool   `json:"warn,omitempty"`
}

func (a *app) routerChecks() []wizCheck {
	var out []wizCheck
	add := func(n string, ok bool, info string) { out = append(out, wizCheck{Name: n, OK: ok, Info: info}) }
	if _, err := os.Stat(*flagXray); err == nil {
		add("Xray установлен", true, xrayVersion())
	} else {
		add("Xray установлен", false, "не найден "+*flagXray+" — сначала установите XKeen")
	}
	if p, err := exec.LookPath("xkeen"); err == nil {
		add("XKeen", true, p)
	} else {
		add("XKeen", false, "команда xkeen не найдена")
	}
	c := a.reload()
	add("Конфиги Xray", len(c.allTags) > 0, *flagConfDir)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	_, err := a.api.GetObservatory(ctx)
	cancel()
	add("Связь с Xray (API)", err == nil && c.apiFound, map[bool]string{true: "работает", false: "не отвечает — проверьте секцию api в конфигах"}[err == nil && c.apiFound])
	add("Группа серверов (balancer)", len(c.groups) > 0, func() string {
		if len(c.groups) > 0 {
			return c.groups[0].Tag
		}
		return "нет — панель не сможет выбирать сервер"
	}())
	if _, err := rciGet("show/version"); err == nil {
		add("Связь с Keenetic", true, "RCI 127.0.0.1:79")
	} else {
		add("Связь с Keenetic", false, "RCI недоступен — не будет списков устройств и туннелей")
	}
	g := guard.snapshot()
	mb, _ := g["availMB"].(int64)
	add("Свободная память", mb < 0 || mb >= 40, fmt.Sprintf("%d МБ", mb))
	add("Интернет у роутера", a.ispOK(), "")
	return out
}

func (a *app) handleWizard(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		if r.URL.Query().Get("job") != "" {
			wiz.Lock()
			b, _ := json.Marshal(wiz.job)
			wiz.Unlock()
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Write(b)
			return
		}
		a.watch.mu.Lock()
		st := a.watch.settings
		nWatch := 0
		for _, g := range a.watch.groups {
			nWatch += len(g.Servers)
		}
		a.watch.mu.Unlock()
		c := a.reload()
		resp := map[string]any{"done": st.WizardDone, "servers": len(c.servers), "watch": nWatch, "tg": st.TgToken != "", "pw": st.PassHash != "",
			"sub": st.SubURL}
		if r.URL.Query().Get("checks") == "1" {
			resp["checks"] = a.routerChecks()
		}
		writeJSON(w, resp)
		return
	}
	var req struct {
		Action string `json:"action"`
		Source string `json:"source"`
		Input  string `json:"input"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		http.Error(w, "bad request", 400)
		return
	}
	fail := func(err error) { w.WriteHeader(400); writeJSON(w, map[string]any{"error": err.Error()}) }
	wiz.Lock()
	busy := wiz.job != nil && wiz.job.Running
	wiz.Unlock()
	switch req.Action {
	case "check", "pick":
		if busy {
			fail(errors.New("проверка уже идёт"))
			return
		}
		var c *xrayConf
		src := req.Source
		if req.Action == "pick" {
			wiz.Lock()
			c, src = wiz.conf, "input"
			wiz.Unlock()
			if c == nil || req.Source == "xkeen" {
				c, src = a.reload(), "xkeen"
			}
		} else if req.Source == "xkeen" {
			c = a.reload()
		} else {
			obs, err := a.wizParse(req.Input)
			if err != nil {
				fail(err)
				return
			}
			c = confFromOutbounds(obs)
			wiz.Lock()
			wiz.conf, wiz.input = c, req.Input
			wiz.Unlock()
		}
		if req.Source == "xkeen" {
			wiz.Lock()
			wiz.conf, wiz.input = nil, ""
			wiz.Unlock()
		}
		go a.wizRunCheck(c, req.Action, src, req.Action == "pick")
		writeJSON(w, map[string]any{"ok": true, "total": len(c.servers)})
	case "apply-servers":
		if demoMode {
			fail(errors.New("в учебном режиме серверы XKeen не меняются"))
			return
		}
		obs, err := a.wizParse(req.Input)
		if err != nil {
			fail(err)
			return
		}
		plan, err := planSub(&subResult{Outbounds: obs})
		if err != nil {
			fail(err)
			return
		}
		if err := a.applyConfig(fmt.Sprintf("мастер: серверы (%d)", len(obs)), []cfgChange{{Name: plan.File, Content: plan.content}}); err != nil {
			fail(err)
			return
		}
		in := strings.TrimSpace(req.Input)
		if (strings.HasPrefix(in, "http://") || strings.HasPrefix(in, "https://")) && !strings.Contains(in, "\n") {
			a.watch.mu.Lock()
			a.watch.settings.SubURL, a.watch.settings.SubAuto, a.watch.settings.SubApplied = in, true, time.Now().Unix()
			a.watch.mu.Unlock()
			a.mu.Lock()
			a.saveStateLocked()
			a.mu.Unlock()
		}
		writeJSON(w, map[string]any{"ok": true, "added": len(plan.Added), "removed": len(plan.Removed)})
	case "done":
		a.watch.mu.Lock()
		a.watch.settings.WizardDone = true
		a.watch.addEvent("", "info", "мастер первого запуска пройден")
		a.watch.mu.Unlock()
		a.mu.Lock()
		a.saveStateLocked()
		a.mu.Unlock()
		writeJSON(w, map[string]any{"ok": true})
	case "reset":
		a.watch.mu.Lock()
		a.watch.settings.WizardDone = false
		a.watch.mu.Unlock()
		writeJSON(w, map[string]any{"ok": true})
	default:
		http.Error(w, "bad action", 400)
	}
}
