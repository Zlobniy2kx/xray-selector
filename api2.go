package main

// API версии 2.0: события и колокольчик, карточка сервера («i»), резервная копия,
// иконки/манифест приложения, шрифт флагов.

import (
	"embed"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

//go:embed assets/*
var assetsFS embed.FS

func (a *app) handleAsset(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/")
	types := map[string]string{"flags.ttf": "font/ttf", "icon-180.png": "image/png", "icon-512.png": "image/png", "favicon.png": "image/png"}
	ct, ok := types[name]
	if !ok {
		http.NotFound(w, r)
		return
	}
	b, err := assetsFS.ReadFile("assets/" + name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "public, max-age=604800")
	w.Write(b)
}

func handleManifest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/manifest+json")
	json.NewEncoder(w).Encode(map[string]any{
		"name": "Xray Selector", "short_name": "Xray", "start_url": "/", "display": "standalone",
		"background_color": "#0b0c0f", "theme_color": "#0b0c0f", "lang": "ru",
		"icons": []map[string]string{
			{"src": "/icon-180.png", "sizes": "180x180", "type": "image/png"},
			{"src": "/icon-512.png", "sizes": "512x512", "type": "image/png", "purpose": "any maskable"},
		},
		"shortcuts": []map[string]any{ // п. 27: долгое нажатие на значок
			{"name": "Сторож", "url": "/#watch"}, {"name": "Правила", "url": "/#sites"},
			{"name": "Устройства", "url": "/#devices"}, {"name": "Журнал", "url": "/#logs"},
		},
	})
}

// ---- события ----

func (a *app) handleEvents(w http.ResponseWriter, r *http.Request) {
	wt := a.watch
	if r.Method == "POST" {
		var req struct {
			ReadUpTo int64 `json:"readUpTo"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		wt.mu.Lock()
		if req.ReadUpTo > wt.lastReadEv {
			wt.lastReadEv = req.ReadUpTo
		}
		wt.mu.Unlock()
		a.mu.Lock()
		a.saveStateLocked()
		a.mu.Unlock()
		writeJSON(w, map[string]any{"ok": true})
		return
	}
	wt.mu.Lock()
	ev := make([]watchEvent, len(wt.events))
	copy(ev, wt.events)
	lr := wt.lastReadEv
	wt.mu.Unlock()
	writeJSON(w, map[string]any{"events": ev, "lastRead": lr})
}

func (a *app) saveEvents() {
	if *flagHistory == "" {
		return
	}
	a.watch.mu.Lock()
	b, _ := json.Marshal(map[string]any{"events": a.watch.events, "next": a.watch.nextEvID})
	a.watch.mu.Unlock()
	os.WriteFile(*flagHistory+".events", b, 0644)
}

func (a *app) loadEvents() {
	b, err := os.ReadFile(*flagHistory + ".events")
	if err != nil {
		return
	}
	var f struct {
		Events []watchEvent `json:"events"`
		Next   int64        `json:"next"`
	}
	if json.Unmarshal(b, &f) == nil {
		a.watch.events, a.watch.nextEvID = f.Events, f.Next
	}
}

// ---- карточка сервера ----

func (a *app) handleServer(w http.ResponseWriter, r *http.Request) {
	tag := r.URL.Query().Get("tag")
	win, _ := strconv.ParseInt(r.URL.Query().Get("win"), 10, 64)
	if win < 600 {
		win = scoreWindow
	}
	c := a.reload()
	var srv *server
	for i := range c.servers {
		if c.servers[i].Tag == tag {
			srv = &c.servers[i]
		}
	}
	if srv == nil {
		http.Error(w, "нет такого сервера", 404)
		return
	}
	now := time.Now().Unix()
	a.mu.Lock()
	var mon []check
	for _, x := range a.hist[tag] {
		if x.T >= now-24*3600 {
			mon = append(mon, x)
		}
	}
	dead := a.dead[tag]
	active := false
	for _, v := range a.desired {
		if v == tag {
			active = true
		}
	}
	a.mu.Unlock()

	// проверки сторожа за 24 ч — по 15 минут
	type bucket struct {
		T    int64   `json:"t"`
		N    int     `json:"n"`
		OK   int     `json:"ok"`
		Ms   int     `json:"ms"`
		Mbps float64 `json:"mbps"`
	}
	wt := a.watch
	wt.mu.Lock()
	var buckets []bucket
	bm := map[int64]*bucket{}
	var spdN map[int64]int = map[int64]int{}
	for _, x := range wt.whist[tag] {
		if x.T < now-24*3600 {
			continue
		}
		k := x.T / 900 * 900
		b := bm[k]
		if b == nil {
			b = &bucket{T: k}
			bm[k] = b
		}
		b.N++
		if x.OK {
			b.OK++
			b.Ms += int(x.Ms)
		}
		if x.HasSpd && x.SpdOK {
			b.Mbps += float64(x.Kbps) / 1000
			spdN[k]++
		}
	}
	for k, b := range bm {
		if b.OK > 0 {
			b.Ms /= b.OK
		}
		if spdN[k] > 0 {
			b.Mbps = round1(b.Mbps / float64(spdN[k]))
		}
		buckets = append(buckets, *b)
	}
	score := wt.scoresLocked(scoreWindow)[tag]
	sc1 := wt.scoresLocked(3600)[tag]
	week := wt.serverWeekLocked(tag, now)
	sc24 := wt.scoresLocked(24 * 3600)[tag]
	_ = win
	var health *srvHealth
	if h := wt.health[tag]; h != nil {
		cp := *h
		health = &cp
	}
	role := ""
	for _, g := range wt.groups {
		for _, s := range g.Servers {
			if s == tag {
				role = "watch"
				for _, p := range g.Pinned {
					if p == tag && g.Auto {
						role = "stable"
					}
				}
				if g.Auto && role == "watch" && contains(g.Reserve, tag) {
					role = "reserve"
				}
				if g.Auto && role == "watch" {
					role = "candidate"
				}
			}
		}
	}
	wt.mu.Unlock()

	// трафик и устройства за сегодня
	tr := a.trf
	tr.mu.Lock()
	day := tr.dayLocked(a.dayKey(time.Now()))
	var srvBytes bytesPair
	if p := day.Servers[tag]; p != nil {
		srvBytes = *p
	}
	srvBytes.Conns = day.SrvConns[tag]
	devs := []map[string]any{}
	for k, m := range day.DevSrv {
		if p := m[tag]; p != nil && (p.Conns > 0 || p.Up+p.Down > 0) {
			devs = append(devs, map[string]any{"id": k, "conns": p.Conns, "bytes": p.Up + p.Down})
		}
	}
	tr.mu.Unlock()
	labels := a.deviceLabels()
	for _, d := range devs {
		d["name"] = labels[d["id"].(string)]
	}

	// журнал только этого сервера
	acc, _ := xrayLogPaths(*flagConfDir)
	var lines []string
	if all, err := tailFile(acc, 1<<20); err == nil {
		needle := "-> " + tag + "]"
		for i := len(all) - 1; i >= 0 && len(lines) < 80; i-- {
			if strings.Contains(all[i], needle) {
				lines = append(lines, all[i])
			}
		}
		for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
			lines[i], lines[j] = lines[j], lines[i]
		}
	}
	writeJSON(w, map[string]any{
		"server": srv, "monitor": mon, "watch": buckets, "score": score, "score1h": sc1, "score24h": sc24, "week": week, "traffic7": a.serverTraffic7(tag), "health": health,
		"role": role, "dead": dead, "active": active, "traffic": srvBytes, "devices": devs,
		"log": lines, "names": a.deviceNames(),
	})
}

// ---- резервная копия ----

func (a *app) handleBackup(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		var req struct {
			State   json.RawMessage     `json:"state"`
			Devices map[string]*devInfo `json:"devices"`
			Configs map[string]string   `json:"xrayConfigs"` // 2.3: конфиги Xray — восстанавливаются только по галочке
			Rules   json.RawMessage     `json:"rules"`       // 2.3: сервисы, режимы, устройства
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil || len(req.State) < 2 {
			http.Error(w, "это не файл резервной копии Xray Selector", 400)
			return
		}
		var probe map[string]any
		if json.Unmarshal(req.State, &probe) != nil {
			http.Error(w, "повреждённый файл", 400)
			return
		}
		if err := os.WriteFile(*flagState, req.State, 0644); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		a.watch.mu.Lock()
		a.watch.groups = map[string]*watchGroup{}
		a.watch.mu.Unlock()
		a.mu.Lock()
		a.desired = map[string]string{}
		a.dead = map[string]bool{}
		a.mu.Unlock()
		a.loadState()
		if len(req.Rules) > 2 {
			os.WriteFile(rulesPath(), req.Rules, 0644)
		}
		if req.Devices != nil {
			tr := a.trf
			tr.mu.Lock()
			for k, d := range req.Devices {
				if cur := tr.devices[k]; cur != nil {
					cur.Name, cur.MergeInto = d.Name, d.MergeInto
				} else {
					tr.devices[k] = d
				}
			}
			tr.lastSave = 0
			tr.mu.Unlock()
		}
		a.watch.mu.Lock()
		a.watch.addEvent("", "info", "настройки восстановлены из резервной копии")
		a.watch.mu.Unlock()
		if len(req.Configs) > 0 && !demoMode {
			var ch []cfgChange
			for n, c := range req.Configs {
				if reCfgName.MatchString(n) {
					ch = append(ch, cfgChange{Name: n, Content: c})
				}
			}
			if err := a.applyConfig("восстановление конфигов Xray из резервной копии", ch); err != nil {
				writeJSON(w, map[string]any{"ok": true, "cfgError": err.Error()})
				return
			}
		}
		writeJSON(w, map[string]any{"ok": true})
		return
	}
	st, _ := os.ReadFile(*flagState)
	tr := a.trf
	tr.mu.Lock()
	devs := map[string]*devInfo{}
	for k, d := range tr.devices {
		cp := *d
		devs[k] = &cp
	}
	tr.mu.Unlock()
	rulesB, _ := os.ReadFile(rulesPath())
	cfgs := map[string]string{}
	if fs, _ := filepath.Glob(filepath.Join(*flagConfDir, "*.json")); len(fs) > 0 {
		for _, f := range fs {
			if b, err := os.ReadFile(f); err == nil && len(b) < 4<<20 {
				cfgs[filepath.Base(f)] = string(b)
			}
		}
	}
	w.Header().Set("Content-Disposition", "attachment; filename=\"xray-selector-backup-"+time.Now().Format("2006-01-02")+".json\"")
	writeJSON(w, map[string]any{"app": "xray-selector", "version": version, "created": time.Now().Unix(),
		"state": json.RawMessage(orEmpty(st)), "devices": devs, "xrayConfigs": cfgs, "rules": json.RawMessage(orEmpty(rulesB))})
}

func orEmpty(b []byte) []byte {
	if len(b) == 0 {
		return []byte("{}")
	}
	return b
}

// serverTraffic7 — трафик сервера за 7 дней (из статистики Xray).
func (a *app) serverTraffic7(tag string) int64 {
	now := time.Now()
	keys := map[string]bool{}
	for i := 0; i < 7; i++ {
		keys[a.dayKey(now.Add(-time.Duration(i)*24*time.Hour))] = true
	}
	tr := a.trf
	tr.mu.Lock()
	defer tr.mu.Unlock()
	var sum int64
	for k, d := range tr.days {
		if keys[k] && d != nil {
			if p := d.Servers[tag]; p != nil {
				sum += p.Up + p.Down
			}
		}
	}
	return sum
}
