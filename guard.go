package main

// Защита роутера от перегрузки (2.3, п. 9).
// После инцидента 2.2.8 (geoip читался по категории — load 30) все тяжёлые фоновые
// работы спрашивают guard: если роутер занят — притормаживают, если мало памяти —
// большие базы не загружаются вовсе (берутся встроенные списки).

import (
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

type loadGuard struct {
	mu      sync.Mutex
	t       time.Time
	load1   float64
	cores   int
	availMB int64
	slowed  int64 // сколько раз притормозили (для «Обслуживания»)
	skipped int64 // сколько раз не стали грузить базу из-за памяти
	lastMsg string
}

var guard = &loadGuard{}

func (g *loadGuard) refreshLocked() {
	if time.Since(g.t) < 3*time.Second {
		return
	}
	g.t = time.Now()
	if b, err := os.ReadFile("/proc/loadavg"); err == nil {
		if f := strings.Fields(string(b)); len(f) > 0 {
			g.load1, _ = strconv.ParseFloat(f[0], 64)
		}
	}
	if g.cores == 0 {
		_, _, c := readCPU()
		if c <= 0 {
			c = runtime.NumCPU()
		}
		g.cores = c
	}
	tot, used := readMem()
	if tot > 0 {
		g.availMB = (tot - used) / 1024
	} else {
		g.availMB = -1
	}
}

// level: 0 — спокойно, 1 — занят (притормаживаем), 2 — перегружен (только самое нужное).
func (g *loadGuard) level() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.refreshLocked()
	c := float64(g.cores)
	if c < 1 {
		c = 1
	}
	switch {
	case g.load1 > c*4 || (g.availMB >= 0 && g.availMB < 15):
		return 2
	case g.load1 > c*2:
		return 1
	}
	return 0
}

// pace — вызывать в начале шага фоновой работы. При нагрузке ждёт (до max), не держит блокировок.
func (g *loadGuard) pace(max time.Duration) {
	for waited := time.Duration(0); waited < max; waited += 5 * time.Second {
		if g.level() == 0 {
			return
		}
		g.mu.Lock()
		g.slowed++
		g.mu.Unlock()
		time.Sleep(5 * time.Second)
	}
}

// memFor — хватит ли памяти, чтобы прочитать файл size байт целиком (нужно ~3× плюс запас 25 МБ).
func (g *loadGuard) memFor(size int64, what string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.refreshLocked()
	if g.availMB < 0 {
		return true // не знаем — не мешаем
	}
	need := size*3/(1<<20) + 25
	if g.availMB >= need {
		return true
	}
	g.skipped++
	g.lastMsg = time.Now().Format("02.01 15:04") + ": не загружаю " + what + " — свободно " + strconv.FormatInt(g.availMB, 10) + " МБ, нужно " + strconv.FormatInt(need, 10)
	return false
}

func (g *loadGuard) snapshot() map[string]any {
	lv := g.level()
	g.mu.Lock()
	defer g.mu.Unlock()
	return map[string]any{"level": lv, "load1": g.load1, "cores": g.cores, "availMB": g.availMB,
		"slowed": g.slowed, "skipped": g.skipped, "last": g.lastMsg}
}
