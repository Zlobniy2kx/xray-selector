package main

// 2.3, п. 29: бесплатная статистика серверов — только из уже собранных проверок сторожа.
//   • джиттер (разброс отклика) — учитывается в баллах;
//   • доступность по часам суток — сторож избегает серверов, которые в этот час обычно падают;
//   • падения и среднее время восстановления (сутки / неделя);
//   • часы работы основным; трафик — из статистики Xray (раздел трафика).
// Хранение: подробно 24 ч (whist), дальше — почасовые итоги до 7 дней (здесь).

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type hourAgg struct {
	H       int64 `json:"h"`  // начало часа (unix)
	N       int   `json:"n"`  // проверок
	OK      int   `json:"ok"` // удачных
	MsSum   int64 `json:"ms"` // сумма отклика удачных
	MsSq    int64 `json:"sq"` // сумма квадратов (для джиттера)
	Falls   int   `json:"f"`  // падений (2+ неудачи подряд)
	DownSec int64 `json:"d"`  // сколько длились завершившиеся падения
	Rec     int   `json:"r"`  // сколько падений завершилось
	Active  int64 `json:"a"`  // секунд был основным (активным)
}

type srvRun struct {
	failRun   int
	downStart int64
}

func hourOf(t int64) int64 { return t - t%3600 }

// statsRecordLocked — под w.mu, из recordWatchLocked.
func (w *watcher) statsRecordLocked(tag string, c wcheck) {
	if w.hourly == nil {
		w.hourly = map[string][]hourAgg{}
		w.runs = map[string]*srvRun{}
	}
	b := w.bucketLocked(tag, c.T)
	b.N++
	if c.OK {
		b.OK++
		b.MsSum += int64(c.Ms)
		b.MsSq += int64(c.Ms) * int64(c.Ms)
	}
	r := w.runs[tag]
	if r == nil {
		r = &srvRun{}
		w.runs[tag] = r
	}
	if !c.OK {
		r.failRun++
		if r.failRun == 1 {
			r.downStart = c.T
		}
		if r.failRun == 2 {
			b.Falls++
		}
	} else {
		if r.failRun >= 2 {
			b.DownSec += c.T - r.downStart
			b.Rec++
		}
		r.failRun = 0
	}
}

func (w *watcher) bucketLocked(tag string, t int64) *hourAgg {
	h := hourOf(t)
	l := w.hourly[tag]
	if n := len(l); n > 0 && l[n-1].H == h {
		return &w.hourly[tag][n-1]
	}
	l = append(l, hourAgg{H: h})
	cut := h - 7*24*3600
	i := 0
	for i < len(l) && l[i].H < cut {
		i++
	}
	w.hourly[tag] = l[i:]
	return &w.hourly[tag][len(w.hourly[tag])-1]
}

// noteActiveLocked — учесть время работы основным (вызывается в каждом заходе сторожа).
func (w *watcher) noteActiveLocked(tag string, now int64) {
	if tag == "" || w.lastActiveT == 0 {
		w.lastActiveT = now
		return
	}
	d := now - w.lastActiveT
	w.lastActiveT = now
	if d <= 0 || d > 600 {
		return
	}
	if w.hourly == nil {
		w.hourly = map[string][]hourAgg{}
		w.runs = map[string]*srvRun{}
	}
	w.bucketLocked(tag, now).Active += d
}

// jitterOf — стандартное отклонение отклика удачных проверок за окно (мс).
func jitterOf(h []wcheck, since int64) (int, int) {
	var n int
	var sum, sq float64
	for _, c := range h {
		if c.T >= since && c.OK && c.Ms > 0 {
			n++
			sum += float64(c.Ms)
			sq += float64(c.Ms) * float64(c.Ms)
		}
	}
	if n < 5 {
		return 0, n
	}
	m := sum / float64(n)
	return int(math.Sqrt(math.Max(0, sq/float64(n)-m*m))), n
}

// jitterPenalty — сколько баллов снять за нестабильный отклик (0…8).
func jitterPenalty(j int) int {
	if j <= 80 {
		return 0
	}
	return int(math.Round(8 * clamp01(float64(j-80)/520)))
}

// hourAvail — доступность сервера в этот час суток по прошлым дням (без последних суток).
// Возвращает долю и число дней с данными.
func (w *watcher) hourAvailLocked(tag string, localHour, tzOffMin int, now int64) (float64, int) {
	n, ok, days := 0, 0, 0
	for _, b := range w.hourly[tag] {
		if b.H >= now-20*3600 {
			continue // сегодняшний день уже видно в обычных баллах
		}
		if time.Unix(b.H, 0).In(time.FixedZone("", tzOffMin*60)).Hour() != localHour || b.N == 0 {
			continue
		}
		days++
		n += b.N
		ok += b.OK
	}
	if n == 0 {
		return 1, 0
	}
	return float64(ok) / float64(n), days
}

// hourPenaltyLocked — штраф для решения сторожа: сервер обычно падает в этот час (≥3 дней данных).
func (w *watcher) hourPenaltyLocked(tag string, now int64) int {
	off := w.settings.TzOffsetMin
	h := time.Unix(now, 0).In(time.FixedZone("", off*60)).Hour()
	av, days := w.hourAvailLocked(tag, h, off, now)
	if days < 3 || av >= 0.9 {
		return 0
	}
	return int(math.Round(15 * clamp01((0.9-av)/0.4)))
}

// serverWeekLocked — сводка для панели «i».
func (w *watcher) serverWeekLocked(tag string, now int64) map[string]any {
	off := w.settings.TzOffsetMin
	byHour := make([]map[string]any, 24)
	for h := 0; h < 24; h++ {
		av, days := w.hourAvailLocked(tag, h, off, now+20*3600) // все дни
		byHour[h] = map[string]any{"h": h, "up": math.Round(av*1000) / 10, "days": days}
	}
	sum := func(since int64) (falls, rec int, down, active int64) {
		for _, b := range w.hourly[tag] {
			if b.H >= since {
				falls += b.Falls
				rec += b.Rec
				down += b.DownSec
				active += b.Active
			}
		}
		return
	}
	f1, r1, d1, a1 := sum(now - 24*3600)
	f7, r7, d7, a7 := sum(now - 7*24*3600)
	avg := func(d int64, r int) int64 {
		if r == 0 {
			return 0
		}
		return d / int64(r)
	}
	j6, n6 := jitterOf(w.whist[tag], now-scoreWindow)
	j24, _ := jitterOf(w.whist[tag], now-24*3600)
	days := 0
	if l := w.hourly[tag]; len(l) > 0 {
		days = int((now-l[0].H)/86400) + 1
	}
	return map[string]any{"byHour": byHour, "falls24": f1, "falls7": f7, "recAvg24": avg(d1, r1), "recAvg7": avg(d7, r7),
		"active24": a1, "active7": a7, "jitter6": j6, "jitterN": n6, "jitter24": j24, "jitterPen": jitterPenalty(j6), "days": days,
		"hourPen": w.hourPenaltyLocked(tag, now)}
}

// ---- сохранение почасовых итогов (на флеш — раз в час, файл маленький) ----

func hourlyPath() string { return filepath.Join(filepath.Dir(*flagState), "xray-selector-hourly.json") }

func (a *app) saveHourly() {
	a.watch.mu.Lock()
	b, err := json.Marshal(a.watch.hourly)
	a.watch.mu.Unlock()
	if err == nil {
		os.WriteFile(hourlyPath()+".tmp", b, 0644)
		os.Rename(hourlyPath()+".tmp", hourlyPath())
	}
}

func (a *app) loadHourly() {
	b, err := os.ReadFile(hourlyPath())
	if err != nil {
		return
	}
	var m map[string][]hourAgg
	if json.Unmarshal(b, &m) != nil {
		return
	}
	a.watch.mu.Lock()
	a.watch.hourly, a.watch.runs = m, map[string]*srvRun{}
	for _, l := range m {
		sort.Slice(l, func(i, j int) bool { return l[i].H < l[j].H })
	}
	a.watch.mu.Unlock()
}

func (a *app) hourlyLoop() {
	a.loadHourly()
	for {
		time.Sleep(time.Hour)
		if !demoMode {
			a.saveHourly()
		}
	}
}
