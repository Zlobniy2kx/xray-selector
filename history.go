package main

// Фоновый мониторинг доступности: каждые N минут проверяем все серверы
// и храним историю проверок, чтобы видеть, сколько раз сервер «лежал».

import (
	"encoding/json"
	"log"
	"os"
	"strconv"
	"strings"
	"time"
)

type check struct {
	T  int64 `json:"t"` // unix, сек
	OK bool  `json:"ok"`
	D  int32 `json:"d,omitempty"` // задержка, мс
}

type winStat struct {
	N    int `json:"n"`    // проверок
	Fail int `json:"fail"` // из них неудачных
	Avg  int `json:"avg"`  // средняя задержка удачных, мс
}

type srvStats struct {
	W   map[string]winStat `json:"w"`   // окна w1, w2, w3, w5, w12, w24 (часы)
	Bar string             `json:"bar"` // последние проверки: 1 — ок, 0 — нет (старые слева)
}

var statWindows = []int{1, 6, 24} // 6 ч = scoreWindow сторожа

const barLen = 30

// record сохраняет итоги одного захода проверок. Если не ответил вообще ни один
// сервер из большого захода — скорее всего пропал интернет у самого роутера,
// такой заход в статистику не идёт.
func (a *app) record(round map[string]result) {
	if len(round) == 0 {
		return
	}
	okCount := 0
	for _, r := range round {
		if r.Err == "" {
			okCount++
		}
	}
	if okCount == 0 && len(round) >= 5 {
		log.Printf("мониторинг: не ответил ни один из %d серверов — похоже, нет интернета; заход не учитываю", len(round))
		return
	}
	now := time.Now().Unix()
	cut := now - int64(*flagKeep/time.Second)
	a.mu.Lock()
	for tag, r := range round {
		h := a.hist[tag]
		h = append(h, check{T: r.T / 1000, OK: r.Err == "", D: int32(r.Delay)})
		i := 0
		for i < len(h) && h[i].T < cut {
			i++
		}
		a.hist[tag] = h[i:]
	}
	a.mu.Unlock()
	a.saveHistory()
}

func (a *app) statsLocked(valid map[string]bool) map[string]srvStats {
	now := time.Now().Unix()
	out := map[string]srvStats{}
	for tag, h := range a.hist {
		if !valid[tag] || len(h) == 0 {
			continue
		}
		var s srvStats
		calc := func(sec int64) winStat {
			var w winStat
			sum := 0
			for _, c := range h {
				if c.T < now-sec {
					continue
				}
				w.N++
				if !c.OK {
					w.Fail++
				} else {
					sum += int(c.D)
				}
			}
			if ok := w.N - w.Fail; ok > 0 {
				w.Avg = sum / ok
			}
			return w
		}
		s.W = map[string]winStat{}
		for _, hrs := range statWindows {
			s.W["w"+strconv.Itoa(hrs)] = calc(int64(hrs) * 3600)
		}
		var b strings.Builder
		start := max(0, len(h)-barLen)
		for _, c := range h[start:] {
			if c.OK {
				b.WriteByte('1')
			} else {
				b.WriteByte('0')
			}
		}
		s.Bar = b.String()
		out[tag] = s
	}
	return out
}

func (a *app) saveHistory() {
	if *flagHistory == "" {
		return
	}
	a.mu.Lock()
	b, err := json.Marshal(a.hist)
	a.mu.Unlock()
	if err != nil {
		return
	}
	tmp := *flagHistory + ".tmp"
	if os.WriteFile(tmp, b, 0644) == nil {
		os.Rename(tmp, *flagHistory)
	}
}

func (a *app) loadHistory() {
	if *flagHistory == "" {
		return
	}
	b, err := os.ReadFile(*flagHistory)
	if err != nil {
		return
	}
	var h map[string][]check
	if json.Unmarshal(b, &h) == nil && h != nil {
		a.hist = h
		for tag, hh := range h {
			if len(hh) > 0 {
				c := hh[len(hh)-1]
				r := result{Delay: int64(c.D), T: c.T * 1000}
				if !c.OK {
					r = result{Err: "не ответил при последней проверке", T: c.T * 1000}
				}
				a.results[tag] = r
			}
		}
		log.Printf("история проверок загружена: %d серверов", len(h))
	}
}

func (a *app) monitorLoop() {
	if *flagMonitor <= 0 {
		log.Printf("фоновый мониторинг выключен (-monitor 0)")
		return
	}
	log.Printf("фоновый мониторинг: проверка всех серверов каждые %s, история %s", *flagMonitor, *flagKeep)
	time.Sleep(min(20*time.Second, *flagMonitor))
	for {
		guard.pace(10 * time.Minute) // роутер занят — полную проверку откладываем (п. 9)
		start := time.Now()
		c := a.reload()
		tags := make([]string, 0, len(c.servers))
		a.mu.Lock()
		for _, s := range c.servers {
			if !a.dead[s.Tag] { // «мёртвые» в фоне не проверяем
				tags = append(tags, s.Tag)
			}
		}
		a.mu.Unlock()
		a.runTests(tags)
		a.mu.Lock()
		a.lastRound = time.Now().Unix()
		a.mu.Unlock()
		if d := *flagMonitor - time.Since(start); d > 0 {
			time.Sleep(d)
		}
	}
}
