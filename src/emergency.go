package main

// Аварийный режим: все серверы из списка сторожа перестали открывать сервисы,
// а напрямую интернет есть (обычно — провайдер начал резать эти серверы).
// Тогда проверяем ВСЕ серверы подписки (кроме «мёртвых»), находим рабочие,
// сразу переключаемся на лучший и добавляем найденные в список сторожа.

import (
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"
)

var emergMu sync.Mutex
var emergLast = map[string]int64{}

// ispOK — напрямую (мимо VPN) интернет работает: последняя проверка провайдера
// за 3 минуты удачная.
func (a *app) ispOK() bool {
	tr := a.trf
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if len(tr.isp) == 0 {
		return false
	}
	p := tr.isp[len(tr.isp)-1]
	return p.OK && time.Now().Unix()-p.T <= 180
}

func (a *app) startEmergency(group string) {
	emergMu.Lock()
	now := time.Now().Unix()
	every := int64(180) // не чаще раза в 3 минуты
	a.mu.Lock()
	if isReserveTag(a.desired[group]) {
		every = 600 // на резерве — проверять подписку раз в 10 минут
	}
	a.mu.Unlock()
	if now-emergLast[group] < every {
		emergMu.Unlock()
		return
	}
	emergLast[group] = now
	emergMu.Unlock()
	go a.emergencyScan(group)
}

func (a *app) emergencyScan(group string) {
	c := a.reload()
	w := a.watch
	w.mu.Lock()
	st := w.settings
	g := w.groups[group]
	if g == nil || !g.Enabled {
		w.mu.Unlock()
		return
	}
	inList := map[string]bool{}
	for _, s := range g.Servers {
		inList[s] = true
	}
	w.mu.Unlock()
	a.mu.Lock()
	var tags []string
	for _, s := range c.servers {
		if !a.dead[s.Tag] {
			tags = append(tags, s.Tag)
		}
	}
	a.mu.Unlock()
	if len(tags) == 0 || len(st.Targets) == 0 {
		return
	}
	log.Printf("[аварийный поиск %s] проверяю %d серверов", group, len(tags))
	ports, err := a.probe.ensure(c, nil)
	if err != nil {
		w.mu.Lock()
		w.addEvent(group, "warn", "аварийный поиск: не удалось запустить тестовый xray: "+err.Error())
		w.mu.Unlock()
		return
	}
	timeout := 6 * time.Second

	// этап 1: один быстрый сервис через каждый сервер (по 12 одновременно)
	first := st.Targets[0]
	for _, t := range st.Targets {
		if strings.Contains(t.Name, "YouTube") {
			first = t
			break
		}
	}
	type cand struct {
		tag string
		ms  int64
	}
	var mu sync.Mutex
	var ok1 []cand
	sem := make(chan struct{}, 12)
	var wg sync.WaitGroup
	for _, t := range tags {
		port, has := ports[t]
		if !has {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(t string, port int) {
			defer wg.Done()
			defer func() { <-sem }()
			if r := checkURL(port, first.URL, timeout); r.OK {
				mu.Lock()
				ok1 = append(ok1, cand{t, r.Ms})
				mu.Unlock()
			}
		}(t, port)
	}
	wg.Wait()
	sort.Slice(ok1, func(i, j int) bool { return ok1[i].ms < ok1[j].ms })
	if len(ok1) > 8 {
		ok1 = ok1[:8]
	}

	// этап 2: лучшие — по всем сервисам
	var good []cand
	for _, cd := range ok1 {
		allOK, sum := true, int64(0)
		var mu2 sync.Mutex
		var wg2 sync.WaitGroup
		for _, tg := range st.Targets {
			wg2.Add(1)
			go func(u string) {
				defer wg2.Done()
				r := checkURL(ports[cd.tag], u, timeout)
				mu2.Lock()
				if r.OK {
					sum += r.Ms
				} else {
					allOK = false
				}
				mu2.Unlock()
			}(tg.URL)
		}
		wg2.Wait()
		if allOK {
			good = append(good, cand{cd.tag, sum / int64(len(st.Targets))})
		}
	}
	a.probe.mu.Lock()
	a.probe.lastUse = time.Now()
	a.probe.mu.Unlock()
	sort.Slice(good, func(i, j int) bool { return good[i].ms < good[j].ms })

	if len(good) == 0 {
		if a.tryReserve(group) {
			return
		}
		w.mu.Lock()
		w.addEvent(group, "warn", fmt.Sprintf("аварийный поиск: проверил %d серверов — ни один не открывает все сервисы. Повторю через 3 минуты", len(tags)))
		w.mu.Unlock()
		return
	}
	best := good[0].tag
	var added []string
	w.mu.Lock()
	g = w.groups[group]
	if g == nil || !g.Enabled {
		w.mu.Unlock()
		return
	}
	for i, cd := range good {
		if i >= 3 {
			break
		}
		if !contains(g.Servers, cd.tag) {
			g.Servers = append(g.Servers, cd.tag)
			if g.Auto {
				if g.Trials == nil {
					g.Trials = map[string]int64{}
				}
				g.Trials[cd.tag] = time.Now().Unix()
			}
			added = append(added, cd.tag)
		}
		// свежая «здоровая» отметка, чтобы сторож не ушёл с него при первой же проверке
		h := w.health[cd.tag]
		if h == nil {
			h = &srvHealth{Targets: map[string]targetRes{}}
			w.health[cd.tag] = h
		}
		now := time.Now().Unix()
		h.OK, h.T, h.ConsecFail, h.Reason = true, now, 0, ""
		if h.ConsecOK < 2 {
			h.ConsecOK = 2
		}
		if h.OkSince == 0 {
			h.OkSince = now
		}
	}
	names := make([]string, 0, len(good))
	for i, cd := range good {
		if i >= 3 {
			break
		}
		names = append(names, cd.tag)
	}
	msg := fmt.Sprintf("все серверы из списка не открывают сервисы, а напрямую интернет есть — похоже, провайдер их режет. Проверил %d серверов, работают: %s. Переключаю на %s",
		len(tags), strings.Join(names, ", "), best)
	if len(added) > 0 {
		msg += "; добавил в список сторожа: " + strings.Join(added, ", ")
	}
	w.addEventShort(group, "switch", msg, "⚠️ Серверы из списка не работают → "+shortSrv(best, ""))
	delete(w.allDown, group)
	delete(w.allDown, "*")
	w.mu.Unlock()

	a.mu.Lock()
	a.desired[group] = best
	a.saveStateLocked()
	a.mu.Unlock()
	a.applyOnce()
	select {
	case w.kick <- struct{}{}:
	default:
	}
}
