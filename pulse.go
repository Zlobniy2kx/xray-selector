package main

// «Пульс» интернета: каждые 5 секунд короткое TCP-соединение напрямую с несколькими
// адресами. Ловит короткие обрывы (на 5–60 с), которых не видно в ежеминутной проверке:
// именно из-за них игры пишут «нет соединения» и выкидывают из матча.

import (
	"net"
	"net/http"
	"sync"
	"time"
)

type outage struct {
	From  int64  `json:"from"`
	To    int64  `json:"to"` // 0 — ещё длится
	Iface string `json:"iface"`
}

var pulse struct {
	sync.Mutex
	outages []outage
	cur     *outage
	lastOK  int64
	checks  int64
	fails   int64
}

func pulseOnce() bool {
	targets := []string{"77.88.8.8:53", "77.88.8.1:53", "1.1.1.1:53", "195.208.4.1:53"}
	type r struct{ ok bool }
	ch := make(chan bool, len(targets))
	for _, t := range targets {
		go func(t string) {
			c, err := net.DialTimeout("tcp", t, 2*time.Second)
			if err == nil {
				c.Close()
			}
			ch <- err == nil
		}(t)
	}
	ok := false
	for range targets {
		if <-ch {
			ok = true
		}
	}
	return ok
}

func (a *app) pulseLoop() {
	time.Sleep(20 * time.Second)
	fail := 0
	for {
		ok := pulseOnce()
		now := time.Now().Unix()
		a.met.mu.Lock()
		iface := a.met.iface
		a.met.mu.Unlock()
		pulse.Lock()
		pulse.checks++
		var ev, kind string
		if ok {
			fail = 0
			pulse.lastOK = now
			if pulse.cur != nil {
				pulse.cur.To = now
				d := pulse.cur.To - pulse.cur.From
				ev, kind = "интернет пропадал на "+fmtDur(d)+" (провайдер / линия до роутера)", "warn"
				pulse.outages = append(pulse.outages, *pulse.cur)
				if len(pulse.outages) > 300 {
					pulse.outages = pulse.outages[len(pulse.outages)-300:]
				}
				pulse.cur = nil
			}
		} else {
			pulse.fails++
			fail++
			if fail == 2 && pulse.cur == nil { // две неудачи подряд (≈10 с) — обрыв
				from := pulse.lastOK
				if from == 0 {
					from = now - 10
				}
				pulse.cur = &outage{From: from + 1, Iface: iface}
			}
		}
		pulse.Unlock()
		if ev != "" {
			a.watch.mu.Lock()
			a.watch.addEvent("", kind, ev)
			a.watch.mu.Unlock()
		}
		time.Sleep(5 * time.Second)
	}
}

func fmtDur(s int64) string {
	switch {
	case s < 60:
		return itoa(s) + " с"
	case s < 3600:
		return itoa(s/60) + " мин " + itoa(s%60) + " с"
	}
	return itoa(s/3600) + " ч " + itoa(s%3600/60) + " мин"
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

func (a *app) handlePulse(w http.ResponseWriter, r *http.Request) {
	pulse.Lock()
	out := append([]outage(nil), pulse.outages...)
	if pulse.cur != nil {
		out = append(out, *pulse.cur)
	}
	resp := map[string]any{"outages": out, "checks": pulse.checks, "fails": pulse.fails, "lastOK": pulse.lastOK, "every": 5}
	pulse.Unlock()
	writeJSON(w, resp)
}
