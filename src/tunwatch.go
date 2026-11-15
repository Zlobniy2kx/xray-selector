package main

// Сторож подключений Keenetic (WireGuard, прокси, OpenVPN…): каждые TunEverySec
// проверяются те же сервисы, что и у серверов подписки (Instagram, YouTube, Telegram,
// WhatsApp и их медиа), а раз в 5 минут — скорость. Результаты идут в ту же историю,
// поэтому у подключений есть баллы, «здоров N минут» и график — как у серверов.
// Резерв при аварии выбирает подключение с лучшими баллами.

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

func ifaceClient(iface string) (*http.Client, *http.Transport) {
	d := ifaceDialer(iface)
	tr := &http.Transport{DialContext: d.DialContext, DisableKeepAlives: true, TLSHandshakeTimeout: 8 * time.Second}
	return &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, tr
}

func checkURLIface(iface, url string, timeout time.Duration) targetRes {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cl, tr := ifaceClient(iface)
	defer tr.CloseIdleConnections()
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36")
	start := time.Now()
	resp, err := cl.Do(req)
	if err != nil {
		return targetRes{Err: cleanErr(err).Error()}
	}
	ms := time.Since(start).Milliseconds()
	io.CopyN(io.Discard, resp.Body, 16*1024)
	resp.Body.Close()
	if resp.StatusCode >= 500 {
		return targetRes{Ms: ms, Code: resp.StatusCode, Err: fmt.Sprintf("HTTP %d", resp.StatusCode)}
	}
	return targetRes{OK: true, Ms: max(ms, 1), Code: resp.StatusCode}
}

func speedTestIface(iface, url string, timeout time.Duration) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cl, tr := ifaceClient(iface)
	defer tr.CloseIdleConnections()
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	resp, err := cl.Do(req)
	if err != nil {
		return 0, cleanErr(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return 0, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	start := time.Now()
	n, err := io.Copy(io.Discard, resp.Body)
	el := time.Since(start)
	if n < 64*1024 {
		if err != nil && n > 0 {
			return 0, fmt.Errorf("зависло: пришло %d КБ и поток встал", n/1024)
		}
		if err != nil {
			return 0, cleanErr(err)
		}
		return 0, fmt.Errorf("получено всего %d КБ", n/1024)
	}
	if el < time.Millisecond {
		el = time.Millisecond
	}
	return int(float64(n*8) / el.Seconds() / 1000), nil
}

// applyCheckLocked — обновить здоровье и историю по результатам одной проверки (под w.mu).
func (w *watcher) applyCheckLocked(tag string, res map[string]targetRes, spd *[2]any, now int64) {
	st := w.settings
	h := w.health[tag]
	if h == nil {
		h = &srvHealth{}
		w.health[tag] = h
	}
	h.Targets, h.T = res, now
	wc := wcheck{T: now, OK: true}
	msSum, msN := 0, 0
	var bad []string
	for _, t := range st.Targets {
		if r := res[t.Name]; r.OK {
			msSum += int(r.Ms)
			msN++
		} else {
			wc.OK = false
			bad = append(bad, t.Name+" ("+r.Err+")")
		}
	}
	if msN > 0 {
		wc.Ms = int32(msSum / msN)
	}
	if spd != nil {
		h.SpeedKbps, h.SpeedErr, h.SpeedT = spd[0].(int), spd[1].(string), now
		wc.HasSpd, wc.SpdOK, wc.Kbps = true, h.SpeedErr == "", int32(h.SpeedKbps)
	}
	if h.SpeedT > 0 && h.SpeedErr != "" {
		bad = append(bad, "скорость ("+h.SpeedErr+")")
	}
	w.recordWatchLocked(tag, wc)
	h.Checks++
	if len(bad) == 0 {
		if !h.OK || h.OkSince == 0 {
			h.OkSince = now
		}
		h.OK, h.Reason, h.ConsecFail = true, "", 0
		h.ConsecOK++
	} else {
		h.OK, h.Reason, h.OkSince = false, strings.Join(bad, ", "), 0
		h.ConsecFail++
		h.ConsecOK = 0
		h.Fails++
	}
}

var tunMu sync.Mutex

// tunnelRound — одна проверка всех подключений Keenetic.
func (a *app) tunnelRound(force bool) {
	if !tunMu.TryLock() {
		return
	}
	defer tunMu.Unlock()
	w := a.watch
	w.mu.Lock()
	st := w.settings
	w.mu.Unlock()
	if st.TunOff || len(st.Targets) == 0 {
		return
	}
	timeout := time.Duration(st.TimeoutMs) * time.Millisecond
	now := time.Now().Unix()
	for _, x := range tunnelIfaces() {
		if !x.Up {
			w.mu.Lock()
			h := w.health[x.Tag]
			if h == nil {
				h = &srvHealth{}
				w.health[x.Tag] = h
			}
			h.OK, h.Reason, h.T, h.OkSince, h.ConsecOK = false, "подключение в Keenetic не активно", now, 0, 0
			w.mu.Unlock()
			continue
		}
		res := map[string]targetRes{}
		var mu sync.Mutex
		var wg sync.WaitGroup
		for _, t := range st.Targets {
			wg.Add(1)
			go func(t watchTarget) {
				defer wg.Done()
				checkSem <- struct{}{}
				defer func() { <-checkSem }()
				r := checkURLIface(x.Name, t.URL, timeout)
				mu.Lock()
				res[t.Name] = r
				mu.Unlock()
			}(t)
		}
		wg.Wait()
		var spd *[2]any
		w.mu.Lock()
		h := w.health[x.Tag]
		needSpd := st.SpeedURL != "" && (force || h == nil || now-h.SpeedT >= 300)
		w.mu.Unlock()
		if needSpd {
			k, err := speedTestIface(x.Name, st.SpeedURL, 8*time.Second)
			e := ""
			if err != nil {
				e = err.Error()
			}
			spd = &[2]any{k, e}
		}
		w.mu.Lock()
		w.applyCheckLocked(x.Tag, res, spd, time.Now().Unix())
		w.mu.Unlock()
	}
}

func (a *app) tunnelLoop() {
	time.Sleep(45 * time.Second)
	for {
		a.tunnelRound(false)
		a.watch.mu.Lock()
		every := a.watch.settings.TunEverySec
		a.watch.mu.Unlock()
		if every < 30 {
			every = 60
		}
		time.Sleep(time.Duration(every) * time.Second)
		guard.pace(3 * time.Minute)
	}
}

// reserveOrder — подключения резерва: сначала здоровые с лучшими баллами.
func (a *app) reserveOrder(conf []reserveEntry) []reserveEntry {
	w := a.watch
	w.mu.Lock()
	sc := w.scoresLocked(scoreWindow)
	type it struct {
		e  reserveEntry
		ok bool
		s  int
	}
	var l []it
	for _, e := range conf {
		h := w.health[e.Tag]
		l = append(l, it{e, h != nil && h.OK, sc[e.Tag].Score})
	}
	w.mu.Unlock()
	sort.SliceStable(l, func(i, j int) bool {
		if l[i].ok != l[j].ok {
			return l[i].ok
		}
		return l[i].s > l[j].s
	})
	out := make([]reserveEntry, len(l))
	for i, x := range l {
		out[i] = x.e
	}
	return out
}

func (a *app) handleTunnels(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		go a.tunnelRound(true)
		writeJSON(w, map[string]any{"ok": true})
		return
	}
	list := tunnelIfaces()
	conf := map[string]bool{}
	for _, e := range reserveConfigured() {
		conf[e.Iface] = true
	}
	wt := a.watch
	wt.mu.Lock()
	sc := wt.scoresLocked(scoreWindow)
	var out []map[string]any
	for _, x := range list {
		row := map[string]any{"iface": x, "inReserve": conf[x.Name]}
		if h := wt.health[x.Tag]; h != nil {
			row["health"] = *h
		}
		if s, ok := sc[x.Tag]; ok {
			row["score"] = s
		}
		out = append(out, row)
	}
	every, off := wt.settings.TunEverySec, wt.settings.TunOff
	wt.mu.Unlock()
	a.mu.Lock()
	active := ""
	for _, v := range a.desired {
		if isReserveTag(v) {
			active = v
		}
	}
	a.mu.Unlock()
	writeJSON(w, map[string]any{"tunnels": out, "every": every, "off": off, "active": active})
}
