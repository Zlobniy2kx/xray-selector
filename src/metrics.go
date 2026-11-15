package main

// Метрики роутера для графиков: процессор, память, температура, скорость
// интернета (интерфейс маршрута по умолчанию), память Xray.
// Замер раз в 2 секунды, хранится последний час — это чтение пары файлов из /proc.

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type metricPoint struct {
	T     int64   `json:"t"`
	CPU   float64 `json:"cpu"`   // %
	Mem   int64   `json:"mem"`   // занято, КБ
	Temp  float64 `json:"temp"`  // °C, 0 — нет датчика
	Rx    float64 `json:"rx"`    // Мбит/с вход
	Tx    float64 `json:"tx"`    // Мбит/с выход
	XrayM int64   `json:"xrayM"` // память основного Xray, КБ
}

type metrics struct {
	mu       sync.Mutex
	points   []metricPoint
	memTotal int64
	iface    string
	cores    int
	hasTemp  bool
	uptime   int64
	load     string

	prevIdle, prevTotal uint64
	prevRx, prevTx      uint64
	prevT               time.Time
}

func readCPU() (idle, total uint64, cores int) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		l := sc.Text()
		if strings.HasPrefix(l, "cpu ") {
			fs := strings.Fields(l)[1:]
			for i, x := range fs {
				v, _ := strconv.ParseUint(x, 10, 64)
				total += v
				if i == 3 || i == 4 { // idle + iowait
					idle += v
				}
			}
		} else if strings.HasPrefix(l, "cpu") {
			cores++
		}
	}
	return
}

func readMem() (total, used int64) {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return
	}
	var avail int64 = -1
	var free, buf, cached int64
	for _, l := range strings.Split(string(b), "\n") {
		fs := strings.Fields(l)
		if len(fs) < 2 {
			continue
		}
		v, _ := strconv.ParseInt(fs[1], 10, 64)
		switch fs[0] {
		case "MemTotal:":
			total = v
		case "MemAvailable:":
			avail = v
		case "MemFree:":
			free = v
		case "Buffers:":
			buf = v
		case "Cached:":
			cached = v
		}
	}
	if avail < 0 {
		avail = free + buf + cached
	}
	return total, total - avail
}

func readTemp() (float64, bool) {
	var best float64
	found := false
	paths, _ := filepath.Glob("/sys/class/thermal/thermal_zone*/temp")
	more, _ := filepath.Glob("/sys/class/hwmon/hwmon*/temp*_input")
	for _, p := range append(paths, more...) {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
		if err != nil || v <= 0 {
			continue
		}
		if v > 1000 {
			v /= 1000
		}
		if v > 5 && v < 150 && v > best {
			best, found = v, true
		}
	}
	return best, found
}

// defaultIface — интерфейс маршрута по умолчанию; если не нашли — самый «трафиковый».
func defaultIface() string {
	if b, err := os.ReadFile("/proc/net/route"); err == nil {
		for _, l := range strings.Split(string(b), "\n")[1:] {
			fs := strings.Fields(l)
			if len(fs) > 2 && fs[1] == "00000000" && fs[7] == "00000000" {
				return fs[0]
			}
		}
	}
	var best string
	var bestB uint64
	for name, v := range readNetDev() {
		if name == "lo" || strings.HasPrefix(name, "br") {
			continue
		}
		if v[0]+v[1] > bestB {
			best, bestB = name, v[0]+v[1]
		}
	}
	return best
}

func readNetDev() map[string][2]uint64 {
	out := map[string][2]uint64{}
	b, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return out
	}
	for _, l := range strings.Split(string(b), "\n")[2:] {
		i := strings.Index(l, ":")
		if i < 0 {
			continue
		}
		fs := strings.Fields(l[i+1:])
		if len(fs) < 9 {
			continue
		}
		rx, _ := strconv.ParseUint(fs[0], 10, 64)
		tx, _ := strconv.ParseUint(fs[8], 10, 64)
		out[strings.TrimSpace(l[:i])] = [2]uint64{rx, tx}
	}
	return out
}

func procRSS(pid string) int64 {
	b, err := os.ReadFile("/proc/" + pid + "/status")
	if err != nil {
		return 0
	}
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, "VmRSS:") {
			fs := strings.Fields(l)
			if len(fs) >= 2 {
				v, _ := strconv.ParseInt(fs[1], 10, 64)
				return v
			}
		}
	}
	return 0
}

func (a *app) mainXrayPid() string {
	ents, _ := os.ReadDir("/proc")
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || isProbeProc(pid) {
			continue
		}
		comm, err := os.ReadFile("/proc/" + e.Name() + "/comm")
		if err == nil && strings.TrimSpace(string(comm)) == "xray" {
			return e.Name()
		}
	}
	return ""
}

func (a *app) metricsLoop() {
	m := a.met
	tick := time.NewTicker(2 * time.Second)
	n := 0
	for range tick.C {
		if n%30 == 0 { // раз в минуту — переопределить интерфейс (мог смениться провайдер)
			if ifc := defaultIface(); ifc != "" {
				m.mu.Lock()
				if ifc != m.iface {
					m.iface, m.prevRx, m.prevTx = ifc, 0, 0
				}
				m.mu.Unlock()
			}
		}
		n++
		now := time.Now()
		idle, total, cores := readCPU()
		memT, memU := readMem()
		temp, hasT := readTemp()
		dev := readNetDev()
		var xm int64
		if pid := a.mainXrayPid(); pid != "" {
			xm = procRSS(pid)
		}
		up := int64(0)
		if b, err := os.ReadFile("/proc/uptime"); err == nil {
			f, _ := strconv.ParseFloat(strings.Fields(string(b))[0], 64)
			up = int64(f)
		}
		load := ""
		if b, err := os.ReadFile("/proc/loadavg"); err == nil {
			fs := strings.Fields(string(b))
			if len(fs) >= 3 {
				load = strings.Join(fs[:3], " ")
			}
		}

		m.mu.Lock()
		p := metricPoint{T: now.Unix(), Mem: memU, Temp: temp, XrayM: xm}
		if m.prevTotal > 0 && total > m.prevTotal {
			dt, di := float64(total-m.prevTotal), float64(idle-m.prevIdle)
			p.CPU = round1(100 * (1 - di/dt))
		}
		m.prevIdle, m.prevTotal = idle, total
		if v, ok := dev[m.iface]; ok {
			if m.prevRx > 0 && !m.prevT.IsZero() && v[0] >= m.prevRx && v[1] >= m.prevTx {
				sec := now.Sub(m.prevT).Seconds()
				p.Rx = round1(float64(v[0]-m.prevRx) * 8 / sec / 1e6)
				p.Tx = round1(float64(v[1]-m.prevTx) * 8 / sec / 1e6)
			}
			m.prevRx, m.prevTx = v[0], v[1]
		}
		m.prevT = now
		m.memTotal, m.cores, m.hasTemp, m.uptime, m.load = memT, cores, hasT, up, load
		m.points = append(m.points, p)
		if len(m.points) > 1800 {
			m.points = m.points[len(m.points)-1800:]
		}
		m.mu.Unlock()
	}
}

func round1(x float64) float64 { return float64(int64(x*10+0.5)) / 10 }

func (a *app) metricsSnapshot(since int64) map[string]any {
	m := a.met
	m.mu.Lock()
	defer m.mu.Unlock()
	var pts []metricPoint
	for _, p := range m.points {
		if p.T > since {
			pts = append(pts, p)
		}
	}
	if pts == nil {
		pts = []metricPoint{}
	}
	return map[string]any{
		"points": pts, "memTotal": m.memTotal, "iface": m.iface, "cores": m.cores,
		"hasTemp": m.hasTemp, "uptime": m.uptime, "load": m.load,
	}
}

// isProbeProc — это наш тестовый xray (их два: «все» и «сторож»), а не основной.
func isProbeProc(pid int) bool {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
	return err == nil && strings.Contains(string(b), "xray-selector-probe")
}
