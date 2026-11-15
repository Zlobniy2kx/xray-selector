package main

// Баллы (0–100) — только для серверов сторожа, по его же частым проверкам за 6 часов:
//   доступность сервисов (Instagram, YouTube…) — 40
//   отклик сервисов (среднее время ответа)     — 20   ≤400 мс = максимум, ≥2000 мс = 0
//   скорость (медиана замеров)                 — 25   ≤1,5 Мбит/с = 0, ≥20 Мбит/с = максимум (по логарифму)
//   скачивание без «заморозки»                 — 15   доля замеров, где 300 КБ скачались целиком
//
// Автокандидаты: в списке сторожа есть «стабильные» серверы (📌, их ведёт человек)
// и N мест для кандидатов. На свободные места ставятся серверы с самым низким
// пингом по фоновой проверке; каждый кандидат проверяется TrialMin минут.
// Набрал PassScore и больше — остаётся и предлагается закрепить; меньше — уходит,
// на его место встаёт следующий. Кандидаты стоят в конце списка, поэтому сторож
// переключается на них, только если все стабильные сбоят.

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"time"
)

type wcheck struct {
	T      int64 `json:"t"`
	OK     bool  `json:"ok"`           // все сервисы открылись
	Ms     int32 `json:"ms,omitempty"` // среднее время ответа сервисов
	HasSpd bool  `json:"hs,omitempty"` // в этом заходе мерили скорость
	SpdOK  bool  `json:"so,omitempty"`
	Kbps   int32 `json:"k,omitempty"`
}

type scoreInfo struct {
	Score    int     `json:"score"`
	Eligible bool    `json:"eligible"` // данных достаточно
	Up       float64 `json:"up"`
	Ms       int     `json:"ms"`
	Mbps     float64 `json:"mbps"`
	DLOK     float64 `json:"dlOK"`
	N        int     `json:"n"`
	NS       int     `json:"ns"`
	PUp      int     `json:"pUp"`
	PPing    int     `json:"pPing"`
	PSpeed   int     `json:"pSpeed"`
	PDL      int     `json:"pDL"`
	Jitter   int     `json:"jitter"` // п. 29: разброс отклика, мс
	PJit     int     `json:"pJit"`   // сколько баллов снято за разброс
}

const scoreWindow = 6 * 3600    // окно для решений по кандидатам
const watchHistKeep = 24 * 3600 // сколько хранить историю проверок сторожа

func clamp01(x float64) float64 { return math.Max(0, math.Min(1, x)) }

// recordWatchLocked — вызывается под w.mu после каждого захода сторожа.
func (w *watcher) recordWatchLocked(tag string, c wcheck) {
	h := append(w.whist[tag], c)
	cut := c.T - watchHistKeep
	i := 0
	for i < len(h) && h[i].T < cut {
		i++
	}
	w.whist[tag] = h[i:]
	w.statsRecordLocked(tag, c)
}

func (w *watcher) scoresLocked(win int64) map[string]scoreInfo {
	now := time.Now().Unix()
	out := map[string]scoreInfo{}
	for tag, h := range w.whist {
		var s scoreInfo
		okN, sum := 0, 0
		var speeds []float64
		dl := 0
		for _, c := range h {
			if c.T < now-win {
				continue
			}
			s.N++
			if c.OK {
				okN++
				sum += int(c.Ms)
			}
			if c.HasSpd {
				s.NS++
				if c.SpdOK {
					dl++
					speeds = append(speeds, float64(c.Kbps)/1000)
				} else {
					speeds = append(speeds, 0)
				}
			}
		}
		if s.N == 0 {
			continue
		}
		s.Up = float64(okN) / float64(s.N)
		if okN > 0 {
			s.Ms = sum / okN
			s.PPing = int(math.Round(20 * clamp01((2000-float64(s.Ms))/1600)))
		}
		if s.NS > 0 {
			sort.Float64s(speeds)
			s.Mbps = speeds[len(speeds)/2]
			s.DLOK = float64(dl) / float64(s.NS)
		}
		s.PUp = int(math.Round(40 * s.Up))
		if s.Mbps > 0 {
			s.PSpeed = int(math.Round(25 * clamp01(math.Log(s.Mbps/1.5)/math.Log(20/1.5))))
		}
		s.PDL = int(math.Round(15 * s.DLOK))
		s.Jitter, _ = jitterOf(h, now-win)
		s.PJit = jitterPenalty(s.Jitter)
		s.Score = max(0, s.PUp+s.PPing+s.PSpeed+s.PDL-s.PJit)
		s.Eligible = s.N >= 20 && (s.NS >= 2 || s.Up < 0.5)
		out[tag] = s
	}
	return out
}

func (a *app) scoresWin(win int64) map[string]scoreInfo {
	a.watch.mu.Lock()
	defer a.watch.mu.Unlock()
	return a.watch.scoresLocked(win)
}

func (a *app) saveWatchHistory() {
	if *flagHistory == "" {
		return
	}
	a.watch.mu.Lock()
	b, err := json.Marshal(a.watch.whist)
	a.watch.mu.Unlock()
	if err == nil {
		os.WriteFile(*flagHistory+".watch", b, 0644)
	}
	a.saveEvents()
}

func (a *app) loadWatchHistory() {
	if *flagHistory == "" {
		return
	}
	b, err := os.ReadFile(*flagHistory + ".watch")
	if err != nil {
		return
	}
	var h map[string][]wcheck
	if json.Unmarshal(b, &h) == nil && h != nil {
		a.watch.whist = h
	}
	// восстановить «здоров N минут подряд» по истории, чтобы после перезапуска
	// программы сторож не начинал отсчёт заново (и сразу мог вернуться на лучший)
	now := time.Now().Unix()
	w := a.watch
	for tag, hist := range w.whist {
		if len(hist) == 0 || now-hist[len(hist)-1].T > 600 {
			continue
		}
		last := hist[len(hist)-1]
		h := &srvHealth{Targets: map[string]targetRes{}, T: last.T, OK: last.OK && (!last.HasSpd || last.SpdOK)}
		if last.Kbps > 0 {
			h.SpeedKbps = int(last.Kbps)
		}
		for i := len(hist) - 1; i >= 0; i-- {
			c := hist[i]
			if !c.OK || (c.HasSpd && !c.SpdOK) {
				break
			}
			h.ConsecOK++
			h.OkSince = c.T
		}
		if !h.OK {
			h.OkSince, h.ConsecFail = 0, 1
		}
		w.health[tag] = h
	}
}

// autoSyncBalancers — держит состав групп в конфиге Xray таким же, как список сторожа
// (раньше это происходило только при правке списка вручную).
func (a *app) autoSyncBalancers() {
	c := a.reload()
	w := a.watch
	w.mu.Lock()
	if w.settings.NoBalancerSync {
		w.mu.Unlock()
		return
	}
	type upd struct {
		group string
		list  []string
	}
	var todo []upd
	for _, g := range c.groups {
		wg := w.groups[g.Tag]
		if wg == nil || !wg.Enabled || len(wg.Servers) == 0 {
			continue
		}
		want := wg.syncList()
		same := len(g.Selector) == len(want)
		for i := 0; same && i < len(want); i++ {
			same = g.Selector[i] == want[i]
		}
		if !same {
			todo = append(todo, upd{g.Tag, want})
		}
	}
	w.mu.Unlock()
	for _, u := range todo {
		res, err := syncBalancer(*flagConfDir, u.group, u.list)
		w.mu.Lock()
		if err != nil {
			if w.lastSyncErr != err.Error() {
				w.lastSyncErr = err.Error()
				w.addEvent(u.group, "info", "не удалось обновить группу в конфиге Xray: "+err.Error())
			}
		} else if res.Changed {
			w.lastSyncErr = ""
			w.addEvent(u.group, "info", fmt.Sprintf("группа в конфиге Xray приведена к списку сторожа (%d серверов); вступит в силу при следующем перезапуске Xray", len(u.list)))
		}
		w.mu.Unlock()
	}
}

// ---- автокандидаты ----

func (g *watchGroup) stable() []string {
	pin := map[string]bool{}
	for _, p := range g.Pinned {
		pin[p] = true
	}
	var out []string
	for _, s := range g.Servers {
		if pin[s] {
			out = append(out, s)
		}
	}
	return out
}

// syncList — какие серверы класть в балансировщик Xray: при автокандидатах —
// только стабильные (кандидаты ещё не проверены), иначе весь список.
func (g *watchGroup) syncList() []string {
	if g.Auto {
		if st := g.stable(); len(st) > 0 {
			out := append([]string(nil), st...)
			for _, r := range g.Reserve {
				if !contains(out, r) && contains(g.Servers, r) {
					out = append(out, r)
				}
			}
			return out
		}
	}
	return append([]string(nil), g.Servers...)
}

func (a *app) candLoop() {
	time.Sleep(60 * time.Second)
	for {
		a.candOnce("")
		a.autoSyncBalancers()
		time.Sleep(5 * time.Minute)
	}
}

// pingRanking — серверы по среднему пингу фоновой проверки (за 6 ч), только стабильно отвечающие.
func (a *app) pingRanking(valid map[string]bool) []string {
	now := time.Now().Unix()
	type r struct {
		tag string
		avg int
	}
	var rs []r
	a.mu.Lock()
	for tag, h := range a.hist {
		if !valid[tag] || a.dead[tag] {
			continue
		}
		n, ok, sum := 0, 0, 0
		for _, c := range h {
			if c.T < now-scoreWindow {
				continue
			}
			n++
			if c.OK {
				ok++
				sum += int(c.D)
			}
		}
		if n < 2 || ok == 0 || float64(ok)/float64(n) < 0.9 || !h[len(h)-1].OK {
			continue
		}
		rs = append(rs, r{tag, sum / ok})
	}
	a.mu.Unlock()
	sort.Slice(rs, func(i, j int) bool {
		if rs[i].avg != rs[j].avg {
			return rs[i].avg < rs[j].avg
		}
		return rs[i].tag < rs[j].tag
	})
	out := make([]string, len(rs))
	for i, x := range rs {
		out[i] = x.tag
	}
	return out
}

func (a *app) candOnce(onlyGroup string) {
	c := a.reload()
	valid := map[string]bool{}
	isGRPC := map[string]bool{}
	for _, s := range c.servers {
		valid[s.Tag] = true
		if strings.EqualFold(s.Network, "grpc") || strings.Contains(strings.ToLower(s.Tag), "grpc") {
			isGRPC[s.Tag] = true
		}
	}
	ranking := a.pingRanking(valid)
	a.mu.Lock()
	desired := map[string]string{}
	for k, v := range a.desired {
		desired[k] = v
	}
	a.mu.Unlock()

	now := time.Now().Unix()
	w := a.watch
	w.mu.Lock()
	st := w.settings
	scores := w.scoresLocked(scoreWindow) // 6 часов
	type upd struct {
		group string
		list  []string
	}
	var syncs []upd
	changed := false
	for gtag, g := range w.groups {
		if (onlyGroup != "" && gtag != onlyGroup) || !g.Auto {
			continue
		}
		if g.Trials == nil {
			g.Trials = map[string]int64{}
		}
		if g.Tried == nil {
			g.Tried = map[string]int64{}
		}
		if g.Passed == nil {
			g.Passed = map[string]bool{}
		}
		active := desired[gtag]
		locked := map[string]bool{}
		for _, s := range g.Locked {
			locked[s] = true
		}
		excluded := map[string]bool{}
		for _, x := range g.Excluded {
			excluded[x] = true
		}
		ev := func(text string) { w.addEvent(gtag, "auto", text); changed = true }
		sc := func(s string) int { return scores[s].Score }

		// текущие уровни
		var main, reserve, cands []string
		seen := map[string]bool{}
		for _, s := range g.Pinned {
			if valid[s] && !seen[s] {
				main, seen[s] = append(main, s), true
			}
		}
		for _, s := range g.Reserve {
			if valid[s] && !seen[s] && !excluded[s] && contains(g.Servers, s) {
				reserve, seen[s] = append(reserve, s), true
			}
		}
		for _, s := range g.Servers {
			if valid[s] && !seen[s] {
				if g.Passed[s] { // прошедшие проверку при старой схеме — в запасные
					reserve, seen[s] = append(reserve, s), true
					continue
				}
				cands, seen[s] = append(cands, s), true
			}
		}
		for _, s := range g.Locked { // 🔒 всегда в основных
			if valid[s] && !contains(main, s) {
				main = append(main, s)
				reserve, cands = removeStr(reserve, s), removeStr(cands, s)
			}
		}

		// 1) кандидаты: быстрая отбраковка и проверка
		var keep []string
		for _, s := range cands {
			start, ok := g.Trials[s]
			if !ok {
				g.Trials[s] = now
				start = now
			}
			age, si := now-start, scores[s]
			reject, why := false, ""
			switch {
			case age >= 30*60 && si.N >= 10 && si.Up < 0.8:
				reject, why = true, fmt.Sprintf("за %d мин сервисы открывались только в %d%% проверок", age/60, int(si.Up*100))
			case age >= int64(st.TrialMin)*60 && si.Eligible && si.Score < st.PassScore:
				reject, why = true, fmt.Sprintf("набрал %d баллов из нужных %d", si.Score, st.PassScore)
			}
			if reject && s == active {
				reject = false
			}
			if reject {
				g.Tried[s] = now
				delete(g.Trials, s)
				ev(fmt.Sprintf("кандидат %s не прошёл проверку (%s) — убран", s, why))
				continue
			}
			if age >= int64(st.TrialMin)*60 && si.Eligible && si.Score >= st.PassScore {
				delete(g.Trials, s)
				reserve = append(reserve, s)
				ev(fmt.Sprintf("кандидат %s прошёл проверку: %d баллов (%.0f%% доступность, %.1f Мбит/с) — в запасные 🟡", s, si.Score, si.Up*100, si.Mbps))
				continue
			}
			keep = append(keep, s)
		}
		cands = keep

		byScore := func(l []string) {
			sort.SliceStable(l, func(i, j int) bool { return sc(l[i]) > sc(l[j]) })
		}
		byScore(reserve)
		// худший основной, которого можно понизить
		worstMain := func(skip func(string) bool) int {
			wi := -1
			for i, s := range main {
				if locked[s] || s == active || (skip != nil && skip(s)) {
					continue
				}
				if wi < 0 || sc(s) < sc(main[wi]) {
					wi = i
				}
			}
			return wi
		}

		// 2) основной, который давно плохо работает (а другие основные — хорошо), — убрать
		okMain := 0
		for _, s := range main {
			if scores[s].Eligible && scores[s].Up >= 0.9 {
				okMain++
			}
		}
		for i := 0; i < len(main); i++ {
			s := main[i]
			si := scores[s]
			if locked[s] || s == active || !si.Eligible || si.Up >= 0.6 || okMain < 2 || len(main) <= 3 {
				continue
			}
			main = append(main[:i], main[i+1:]...)
			i--
			g.Tried[s] = now
			ev(fmt.Sprintf("основной %s за 6 часов работал только в %d%% проверок — убран из списка", s, int(si.Up*100)))
		}

		// 3) свободные места в основных — лучшими запасными
		for len(main) < st.MainSlots && len(reserve) > 0 {
			s := reserve[0]
			reserve = reserve[1:]
			main = append(main, s)
			ev(fmt.Sprintf("запасной %s (%d баллов) переведён в основные 📌 — было свободное место", s, sc(s)))
		}
		for len(main) > st.MainSlots { // мест стало меньше
			wi := worstMain(nil)
			if wi < 0 {
				break
			}
			s := main[wi]
			main = append(main[:wi], main[wi+1:]...)
			reserve = append([]string{s}, reserve...)
		}

		// 4) повышение: не чаще раза в 6 часов, запасной лучше худшего основного на 5+ баллов
		if len(reserve) > 0 && now-g.LastPromote >= 6*3600 {
			r := reserve[0]
			if wi := worstMain(nil); wi >= 0 && scores[r].Eligible && sc(r) >= sc(main[wi])+5 {
				m := main[wi]
				main[wi] = r
				reserve[0] = m
				g.LastPromote = now
				ev(fmt.Sprintf("%s (%d баллов за 6 ч) переведён в основные вместо %s (%d) — тот теперь запасной", r, sc(r), m, sc(m)))
			}
		}

		// 5) разнообразие: среди основных хотя бы один gRPC и хотя бы две страны
		swapIn := func(pred func(string) bool, why string, keepPred func(string) bool) {
			for ri, r := range reserve {
				if !pred(r) || !scores[r].Eligible || sc(r) < st.PassScore {
					continue
				}
				if len(main) < st.MainSlots {
					main = append(main, r)
					reserve = append(reserve[:ri], reserve[ri+1:]...)
				} else {
					wi := worstMain(keepPred)
					if wi < 0 {
						return
					}
					m := main[wi]
					main[wi] = r
					reserve[ri] = m
				}
				ev(fmt.Sprintf("%s переведён в основные: %s", r, why))
				return
			}
		}
		hasG := false
		for _, s := range main {
			hasG = hasG || isGRPC[s]
		}
		if !hasG {
			swapIn(func(s string) bool { return isGRPC[s] }, "среди основных не было ни одного gRPC (его провайдеры режут реже)", nil)
		}
		countries := map[string]int{}
		for _, s := range main {
			countries[countryOf(s)]++
		}
		if len(countries) < 2 && len(main) > 0 {
			only := countryOf(main[0])
			swapIn(func(s string) bool { return countryOf(s) != only }, "все основные были из одной страны", func(s string) bool { return isGRPC[s] && countGRPC(main, isGRPC) == 1 })
		}

		// 6) лишние запасные — худшие уходят
		byScore(reserve)
		for len(reserve) > st.ReserveSlots {
			s := reserve[len(reserve)-1]
			if s == active && len(reserve) > 1 {
				s = reserve[len(reserve)-2]
			}
			reserve = removeStr(reserve, s)
			g.Tried[s] = now
			ev(fmt.Sprintf("запасной %s (%d баллов) вытеснен более сильными", s, sc(s)))
		}

		// 7) свободные места кандидатов — с лучшим пингом (если gRPC нигде нет — сначала gRPC)
		inList := map[string]bool{}
		for _, l := range [][]string{main, reserve, cands} {
			for _, s := range l {
				inList[s] = true
			}
		}
		anyG := false
		for s := range inList {
			anyG = anyG || isGRPC[s]
		}
		order := ranking
		if !anyG {
			var gr, rest []string
			for _, s := range ranking {
				if isGRPC[s] {
					gr = append(gr, s)
				} else {
					rest = append(rest, s)
				}
			}
			order = append(gr, rest...)
		}
		for _, s := range order {
			if len(cands) >= st.CandSlots {
				break
			}
			if inList[s] || excluded[s] {
				continue
			}
			if t, ok := g.Tried[s]; ok && now-t < 24*3600 {
				continue
			}
			cands = append(cands, s)
			inList[s] = true
			g.Trials[s] = now
			w.addEvent(gtag, "info", "новый кандидат на проверку: "+s)
			changed = true
		}
		for len(cands) > st.CandSlots { // лишние — убрать (добавленные вручную не трогаем)
			drop := -1
			for i := len(cands) - 1; i >= 0; i-- {
				if cands[i] != active && g.ManualCand[cands[i]] == 0 {
					drop = i
					break
				}
			}
			if drop < 0 {
				break
			}
			delete(g.Trials, cands[drop])
			cands = append(cands[:drop], cands[drop+1:]...)
			changed = true
		}
		for t := range g.ManualCand {
			if !contains(cands, t) {
				delete(g.ManualCand, t)
			}
		}

		// 8) раз в 30 минут — сортировка по баллам внутри уровней
		if now-g.LastSort >= 1800 {
			byScore(main)
			byScore(reserve)
			sort.SliceStable(cands, func(i, j int) bool { return g.Trials[cands[i]] < g.Trials[cands[j]] })
			g.LastSort = now
		}

		for t := range g.Trials {
			if !inList[t] {
				delete(g.Trials, t)
			}
		}
		for t, at := range g.Tried {
			if now-at > 24*3600 {
				delete(g.Tried, t)
			}
		}
		g.Passed = map[string]bool{}
		var lk []string
		for _, s := range g.Locked {
			if contains(main, s) {
				lk = append(lk, s)
			}
		}
		g.Locked = lk
		newList := append(append(append([]string(nil), main...), reserve...), cands...)
		same := len(newList) == len(g.Servers) && strings.Join(main, "\x00") == strings.Join(g.Pinned, "\x00") && strings.Join(reserve, "\x00") == strings.Join(g.Reserve, "\x00")
		for i := 0; same && i < len(newList); i++ {
			same = newList[i] == g.Servers[i]
		}
		if !same {
			g.Pinned, g.Reserve, g.Servers = main, reserve, newList
			changed = true
			syncs = append(syncs, upd{gtag, g.syncList()})
		}
	}
	noSync := st.NoBalancerSync
	w.lastAutoTop = now
	w.mu.Unlock()

	if changed {
		a.mu.Lock()
		a.saveStateLocked()
		a.mu.Unlock()
	}
	if !noSync {
		for _, u := range syncs {
			if len(u.list) == 0 {
				continue
			}
			if res, err := syncBalancer(*flagConfDir, u.group, u.list); err != nil {
				w.mu.Lock()
				w.addEvent(u.group, "info", "не удалось обновить группу в конфиге Xray: "+err.Error())
				w.mu.Unlock()
			} else if res.Changed {
				w.mu.Lock()
				w.addEvent(u.group, "info", fmt.Sprintf("группа в конфиге Xray обновлена: основные и запасные (%d)", len(u.list)))
				w.mu.Unlock()
			}
		}
	}
}

// countryOf — страна по флагу в начале названия сервера (🇩🇪 Germany… → 🇩🇪).
func countryOf(tag string) string {
	r := []rune(strings.TrimSpace(tag))
	if len(r) >= 2 && r[0] >= 0x1F1E6 && r[0] <= 0x1F1FF && r[1] >= 0x1F1E6 && r[1] <= 0x1F1FF {
		return string(r[:2])
	}
	f := strings.Fields(tag)
	if len(f) > 0 {
		return strings.TrimRight(f[0], "-0123456789")
	}
	return tag
}

func countGRPC(l []string, isG map[string]bool) int {
	n := 0
	for _, s := range l {
		if isG[s] {
			n++
		}
	}
	return n
}

// tiers — уровни для выбора: основные, затем запасные, затем кандидаты.
func (g *watchGroup) tiers() [][]string {
	if !g.Auto || len(g.Pinned) == 0 {
		return [][]string{g.Servers}
	}
	var main, res, cand []string
	for _, s := range g.Servers {
		switch {
		case contains(g.Pinned, s):
			main = append(main, s)
		case contains(g.Reserve, s):
			res = append(res, s)
		default:
			cand = append(cand, s)
		}
	}
	return [][]string{main, res, cand}
}
