package main

// 0.9: DNS — данные, чтобы решить, какой сервер делать основным.
//   • история 24 ч подробно (dnsHist) + почасовые итоги 7 дней (файл xray-selector-dnshourly.json);
//   • причина сбоя: провайдер блокирует / сервер не отвечает / пропадал интернет (не считается);
//   • сообщения: «не отвечает» только после 3 неудач подряд (~15 мин), «снова отвечает» — при возврате;
//   • итог: «Подходит как основной» / «Лучше запасным» / «Не рекомендуется» / «Медленный».

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type dnsHour struct {
	H     int64 `json:"h"`
	N     int   `json:"n"`
	OK    int   `json:"ok"`
	Block int   `json:"b"` // провайдер блокирует
	Down  int   `json:"d"` // сервер не отвечал (и через VPN)
	All   int   `json:"a"` // не отвечали все (пропадал интернет) — не в счёт
	MsSum int64 `json:"ms"`
}

var dnsH = struct {
	hours map[string][]dnsHour
	fails map[string]int  // неудач подряд
	alert map[string]bool // уже сообщили
	saved int64
}{map[string][]dnsHour{}, map[string]int{}, map[string]bool{}, 0}

func dnsHourlyPath() string {
	return filepath.Join(filepath.Dir(*flagState), "xray-selector-dnshourly.json")
}

func loadDNSHourly() {
	if b, err := os.ReadFile(dnsHourlyPath()); err == nil {
		json.Unmarshal(b, &dnsH.hours)
		if dnsH.hours == nil {
			dnsH.hours = map[string][]dnsHour{}
		}
	}
}

// dnsAccountLocked — под dnsLast: почасовые итоги + события по каждому серверу.
// Возвращает события (текст, короткий текст для Telegram, вид).
func dnsAccountLocked(r *dnsReport) (evs [][3]string) {
	allDown := !r.Router.OK
	for _, x := range r.List {
		if x.OK {
			allDown = false
		}
	}
	add := func(k string, x dnsResult) {
		h := r.T - r.T%3600
		l := dnsH.hours[k]
		if n := len(l); n == 0 || l[n-1].H != h {
			l = append(l, dnsHour{H: h})
			cut := h - 7*24*3600
			i := 0
			for i < len(l) && l[i].H < cut {
				i++
			}
			l = l[i:]
		}
		b := &l[len(l)-1]
		b.N++
		switch {
		case x.OK:
			b.OK++
			b.MsSum += x.Ms
		case allDown:
			b.All++
		case x.Block:
			b.Block++
		default:
			b.Down++
		}
		dnsH.hours[k] = l
	}
	add("router", r.Router)
	for _, x := range r.List {
		add(x.Addr, x)
		if allDown {
			continue // пропал интернет — серверы не виноваты
		}
		if x.OK {
			if dnsH.alert[x.Addr] {
				evs = append(evs, [3]string{"DNS-сервер " + x.Name + " снова отвечает", "✅ DNS " + x.Name + " снова отвечает", "recover"})
			}
			dnsH.fails[x.Addr], dnsH.alert[x.Addr] = 0, false
			continue
		}
		dnsH.fails[x.Addr]++
		if dnsH.fails[x.Addr] >= 3 && !dnsH.alert[x.Addr] { // 3 проверки подряд ≈ 15 мин
			dnsH.alert[x.Addr] = true
			why := "сервер не отвечает (и через VPN тоже)"
			if x.Block {
				why = "провайдер его блокирует (через VPN отвечает)"
			}
			evs = append(evs, [3]string{"DNS-сервер " + x.Name + " не отвечает 15 мин — " + why, "⚠️ DNS " + x.Name + ": " + why, "warn"})
		}
	}
	if r.T-dnsH.saved >= 3600 && !demoMode {
		dnsH.saved = r.T
		if b, err := json.Marshal(dnsH.hours); err == nil {
			os.WriteFile(dnsHourlyPath(), b, 0644)
		}
	}
	return
}

type dnsStat struct {
	Up24    float64 `json:"up24"`
	Up7     float64 `json:"up7"`
	N24     int     `json:"n24"`
	Days    int     `json:"days"`
	Block7  int     `json:"block7"`  // проверок, где блокировал провайдер
	Down7   int     `json:"down7"`   // где сервер не отвечал
	Eps     int     `json:"eps"`     // сбоев за 24 ч (подряд идущие неудачи = один сбой)
	LongMin int     `json:"longMin"` // самый долгий сбой за 24 ч, мин
	Med     int64   `json:"med"`     // медиана отклика, мс (24 ч)
	P90     int64   `json:"p90"`
	BadHrs  []int   `json:"badHrs,omitempty"` // часы суток, когда сбоит (по 7 дням)
	Verdict string  `json:"verdict"`
	Rank    int     `json:"rank"`
	Cache   bool    `json:"cache,omitempty"` // ответ из кэша роутера (< 5 мс)
}

func dnsStatLocked(k string, tzOff int) dnsStat {
	var s dnsStat
	h := dnsHist[k]
	var ms []int64
	ok, cnt := 0, 0
	var ep, longest, runStart int64
	inRun := false
	for i, p := range h {
		cnt++
		if p.OK {
			ok++
			ms = append(ms, p.Ms)
			if inRun {
				if d := p.T - runStart; d > longest {
					longest = d
				}
				inRun = false
			}
		} else if !inRun {
			inRun, runStart = true, p.T
			ep++
		}
		_ = i
	}
	if inRun && len(h) > 0 {
		if d := h[len(h)-1].T - runStart + 300; d > longest {
			longest = d
		}
	}
	s.N24, s.Eps, s.LongMin = cnt, int(ep), int(longest/60)
	if cnt > 0 {
		s.Up24 = float64(ok) * 100 / float64(cnt)
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i] < ms[j] })
	if len(ms) > 0 {
		s.Med, s.P90 = ms[len(ms)/2], ms[len(ms)*9/10]
	}
	s.Cache = k == "router" && s.Med < 5
	n7, ok7, all7 := 0, 0, 0
	type hb struct{ n, bad int }
	byH := map[int]*hb{}
	days := map[int64]bool{}
	for _, b := range dnsH.hours[k] {
		n7 += b.N - b.All
		ok7 += b.OK
		all7 += b.All
		s.Block7 += b.Block
		s.Down7 += b.Down
		days[b.H/86400] = true
		hr := time.Unix(b.H, 0).In(time.FixedZone("", tzOff*60)).Hour()
		x := byH[hr]
		if x == nil {
			x = &hb{}
			byH[hr] = x
		}
		x.n += b.N - b.All
		x.bad += b.Block + b.Down
	}
	s.Days = len(days)
	if n7 > 0 {
		s.Up7 = float64(ok7) * 100 / float64(n7)
	} else {
		s.Up7 = s.Up24
	}
	for hr, x := range byH {
		if x.n >= 6 && float64(x.bad)/float64(x.n) > 0.2 {
			s.BadHrs = append(s.BadHrs, hr)
		}
	}
	sort.Ints(s.BadHrs)
	switch {
	case s.N24 < 6:
		s.Verdict, s.Rank = "Мало данных", 3
	case s.Up7 >= 99 && s.Med < 300:
		s.Verdict, s.Rank = "Подходит как основной", 0
	case s.Block7 > s.Down7 && s.Up7 < 95:
		s.Verdict, s.Rank = "Не рекомендуется: часто блокирует провайдер", 5
	case s.Up7 < 90:
		s.Verdict, s.Rank = "Не рекомендуется: часто не отвечает", 5
	case s.Med >= 300:
		s.Verdict, s.Rank = fmt.Sprintf("Медленный (обычно %d мс)", s.Med), 2
	default:
		s.Verdict, s.Rank = "Лучше запасным", 1
	}
	if k == "router" {
		s.Verdict, s.Rank = "", 0
	}
	return s
}
