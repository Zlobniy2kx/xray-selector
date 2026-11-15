package main

import (
	"bufio"
	"context"
	"crypto/subtle"
	"embed"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed ui.html
var uiFS embed.FS

var version = "0.9.2.2"

var (
	flagListen   = flag.String("listen", "192.168.1.1:9091", "адрес веб-панели")
	flagAPI      = flag.String("api", "127.0.0.1:10085", "адрес Xray API (config.api.listen)")
	flagConfDir  = flag.String("confdir", "/opt/etc/xray/configs", "папка конфигов Xray")
	flagXray     = flag.String("xray", "/opt/sbin/xray", "путь к бинарнику xray (для теста пинга)")
	flagState    = flag.String("state", "/opt/etc/xray-selector.json", "файл с сохранённым выбором")
	flagURL      = flag.String("url", "https://www.gstatic.com/generate_204", "URL для теста задержки")
	flagTimeout  = flag.Int("timeout", 5000, "таймаут теста, мс")
	flagParallel = flag.Int("parallel", 6, "сколько серверов тестировать одновременно")
	flagPortBase = flag.Int("probe-port", 21000, "первый локальный порт тестового xray")
	flagAuth     = flag.String("auth", "", "логин:пароль для входа в панель (пусто — без пароля)")
	flagIdle     = flag.Int("probe-idle", 90, "через сколько секунд простоя гасить тестовый xray")
	flagLog      = flag.String("log", "", "файл журнала (пусто — stderr)")
	flagTgAPI    = flag.String("tg-api", "https://api.telegram.org", "адрес Telegram Bot API")
	flagMonitor  = flag.Duration("monitor", 10*time.Minute, "как часто проверять все серверы в фоне (0 — выключить)")
	flagKeep     = flag.Duration("keep", 24*time.Hour, "сколько хранить историю проверок")
	flagHistory  = flag.String("history", "/tmp/xray-selector-history.json", "файл истории проверок (в /tmp — в оперативной памяти)")
)

// ---------------- конфиг Xray ----------------

type server struct {
	Tag      string `json:"tag"`
	Protocol string `json:"protocol"`
	Network  string `json:"network"`
	Security string `json:"security"`
	Address  string `json:"address"`
	Port     int    `json:"port"`
}

type group struct {
	Tag      string   `json:"tag"`
	Strategy string   `json:"strategy"`
	Selector []string `json:"selector"`
	Members  []string `json:"members"`
	Fallback string   `json:"fallback"`
	// runtime
	Override string `json:"override"`
	Current  string `json:"current"`
	Desired  string `json:"desired"`
	APIError string `json:"apiError,omitempty"`
}

type xrayConf struct {
	servers     []server
	allTags     []string
	rawOutbound []json.RawMessage
	groups      []group
	apiFound    bool
	apiServices []string
	stamp       string
}

func stripJSONComments(b []byte) []byte {
	out := make([]byte, 0, len(b))
	inStr, esc := false, false
	for i := 0; i < len(b); i++ {
		c := b[i]
		if inStr {
			out = append(out, c)
			if esc {
				esc = false
			} else if c == '\\' {
				esc = true
			} else if c == '"' {
				inStr = false
			}
			continue
		}
		if c == '"' {
			inStr = true
			out = append(out, c)
			continue
		}
		if c == '/' && i+1 < len(b) && b[i+1] == '/' {
			for i < len(b) && b[i] != '\n' {
				i++
			}
			out = append(out, '\n')
			continue
		}
		if c == '#' { // xray тоже разрешает # комментарии
			for i < len(b) && b[i] != '\n' {
				i++
			}
			out = append(out, '\n')
			continue
		}
		if c == '/' && i+1 < len(b) && b[i+1] == '*' {
			i += 2
			for i+1 < len(b) && !(b[i] == '*' && b[i+1] == '/') {
				i++
			}
			i++
			continue
		}
		out = append(out, c)
	}
	return out
}

func loadXrayConf(dir string) (*xrayConf, error) {
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	sort.Strings(files)
	if len(files) == 0 {
		return nil, fmt.Errorf("в %s нет *.json", dir)
	}
	c := &xrayConf{}
	var stamp strings.Builder
	for _, f := range files {
		st, err := os.Stat(f)
		if err == nil {
			fmt.Fprintf(&stamp, "%s:%d:%d;", f, st.Size(), st.ModTime().UnixNano())
		}
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		b = stripJSONComments(b)
		var doc struct {
			Outbounds []json.RawMessage `json:"outbounds"`
			Routing   *struct {
				Balancers []struct {
					Tag      string   `json:"tag"`
					Selector []string `json:"selector"`
					Fallback string   `json:"fallbackTag"`
					Strategy *struct {
						Type string `json:"type"`
					} `json:"strategy"`
				} `json:"balancers"`
			} `json:"routing"`
			API *struct {
				Listen   string   `json:"listen"`
				Services []string `json:"services"`
			} `json:"api"`
		}
		if len(strings.TrimSpace(string(b))) == 0 {
			continue
		}
		if err := json.Unmarshal(b, &doc); err != nil {
			log.Printf("пропускаю %s: %v", f, err)
			continue
		}
		if doc.API != nil {
			c.apiFound = true
			c.apiServices = append(c.apiServices, doc.API.Services...)
		}
		for _, raw := range doc.Outbounds {
			var ob struct {
				Tag      string `json:"tag"`
				Protocol string `json:"protocol"`
				Settings struct {
					Vnext []struct {
						Address string `json:"address"`
						Port    int    `json:"port"`
					} `json:"vnext"`
					Servers []struct {
						Address string `json:"address"`
						Port    int    `json:"port"`
					} `json:"servers"`
					Address string `json:"address"`
					Port    int    `json:"port"`
				} `json:"settings"`
				Stream struct {
					Network  string `json:"network"`
					Security string `json:"security"`
				} `json:"streamSettings"`
			}
			if json.Unmarshal(raw, &ob) != nil || ob.Tag == "" {
				continue
			}
			c.rawOutbound = append(c.rawOutbound, raw)
			c.allTags = append(c.allTags, ob.Tag)
			switch strings.ToLower(ob.Protocol) {
			case "freedom", "blackhole", "dns", "loopback", "":
				continue
			}
			s := server{Tag: ob.Tag, Protocol: ob.Protocol, Network: ob.Stream.Network, Security: ob.Stream.Security}
			if s.Network == "" {
				s.Network = "tcp"
			}
			if len(ob.Settings.Vnext) > 0 {
				s.Address, s.Port = ob.Settings.Vnext[0].Address, ob.Settings.Vnext[0].Port
			} else if len(ob.Settings.Servers) > 0 {
				s.Address, s.Port = ob.Settings.Servers[0].Address, ob.Settings.Servers[0].Port
			} else {
				s.Address, s.Port = ob.Settings.Address, ob.Settings.Port
			}
			c.servers = append(c.servers, s)
		}
		if doc.Routing != nil {
			for _, b := range doc.Routing.Balancers {
				if strings.HasPrefix(b.Tag, "xsel-") { // служебные балансировщики «сайт → страна»
					continue
				}
				g := group{Tag: b.Tag, Selector: b.Selector, Fallback: b.Fallback, Strategy: "random"}
				if b.Strategy != nil && b.Strategy.Type != "" {
					g.Strategy = b.Strategy.Type
				}
				c.groups = append(c.groups, g)
			}
		}
	}
	// участники балансировщика — по префиксу, как в Xray
	for i := range c.groups {
		for _, t := range c.allTags {
			for _, p := range c.groups[i].Selector {
				if strings.HasPrefix(t, p) {
					c.groups[i].Members = append(c.groups[i].Members, t)
					break
				}
			}
		}
	}
	c.stamp = stamp.String()
	return c, nil
}

// ---------------- тест задержки ----------------

type probe struct {
	mu      sync.Mutex
	cmd     *exec.Cmd
	done    chan struct{}
	stamp   string
	ports   map[string]int
	lastUse time.Time
	tmpFile string
	base    int    // первый порт
	name    string // для журнала
	subKey  string // какой набор серверов поднят
}

func (p *probe) stopLocked() {
	if p.cmd != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
		<-p.done
	}
	p.cmd = nil
	p.ports = nil
	if p.tmpFile != "" {
		os.Remove(p.tmpFile)
	}
}

func (p *probe) idleLoop() {
	for range time.Tick(10 * time.Second) {
		p.mu.Lock()
		if p.cmd != nil && time.Since(p.lastUse) > time.Duration(*flagIdle)*time.Second {
			log.Printf("тестовый xray простаивает — останавливаю")
			p.stopLocked()
		}
		p.mu.Unlock()
	}
}

// ensure запускает отдельный, временный экземпляр xray только с исходящими
// (никак не мешает основному): на каждый сервер — свой socks-вход на 127.0.0.1.
// subset — только эти серверы (nil — все): сторожу не нужно держать в памяти все 60+ серверов.
func (p *probe) ensure(c *xrayConf, subset []string) (map[string]int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastUse = time.Now()
	key := ""
	var want map[string]bool
	if subset != nil {
		want = map[string]bool{}
		ss := append([]string(nil), subset...)
		sort.Strings(ss)
		key = strings.Join(ss, "\x00")
		for _, t := range ss {
			want[t] = true
		}
	}
	if p.cmd != nil && p.stamp == c.stamp && p.subKey == key {
		select {
		case <-p.done:
		default:
			return p.ports, nil
		}
	}
	p.stopLocked()
	base := p.base
	if base == 0 {
		base = *flagPortBase
	}

	ports := map[string]int{}
	var inb []map[string]any
	var rules []map[string]any
	var outs []json.RawMessage
	n := 0
	for i, s := range c.servers {
		if want != nil && !want[s.Tag] {
			continue
		}
		port := base + n
		n++
		tag := "probe-" + strconv.Itoa(i)
		ports[s.Tag] = port
		inb = append(inb, map[string]any{
			"tag": tag, "listen": "127.0.0.1", "port": port, "protocol": "socks",
			"settings": map[string]any{"udp": false, "auth": "noauth"},
		})
		rules = append(rules, map[string]any{"inboundTag": []string{tag}, "outboundTag": s.Tag})
		_ = i
	}
	if n == 0 {
		return nil, errors.New("нет серверов для проверки")
	}
	outs = c.rawOutbound
	if want != nil {
		outs = nil
		for _, raw := range c.rawOutbound {
			var o struct {
				Tag string `json:"tag"`
			}
			json.Unmarshal(raw, &o)
			if want[o.Tag] || !isServerTagIn(c, o.Tag) { // служебные (freedom, dialerProxy…) оставляем
				outs = append(outs, raw)
			}
		}
	}
	cfg := map[string]any{
		"log":       map[string]any{"loglevel": "error", "access": "none"},
		"inbounds":  inb,
		"outbounds": outs,
		"routing":   map[string]any{"domainStrategy": "AsIs", "rules": rules},
	}
	b, _ := json.Marshal(cfg)
	f, err := os.CreateTemp("", "xray-selector-probe-*.json")
	if err != nil {
		return nil, err
	}
	f.Write(b)
	f.Close()
	p.tmpFile = f.Name()

	cmd := exec.Command(*flagXray, "run", "-c", p.tmpFile)
	cmd.Env = append(os.Environ(), "GOGC=50")
	stderr, _ := cmd.StderrPipe()
	cmd.Stdout = nil
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("не удалось запустить %s: %v", *flagXray, err)
	}
	done := make(chan struct{})
	var lastErr strings.Builder
	go func() {
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			if lastErr.Len() < 2000 {
				lastErr.WriteString(sc.Text() + "\n")
			}
		}
	}()
	go func() { cmd.Wait(); close(done) }()
	p.cmd, p.done, p.stamp, p.ports, p.subKey = cmd, done, c.stamp, ports, key

	// ждём, пока поднимется последний порт
	last := base + n - 1
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-done:
			p.cmd = nil
			return nil, fmt.Errorf("тестовый xray завершился: %s", strings.TrimSpace(lastErr.String()))
		default:
		}
		conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(last), 300*time.Millisecond)
		if err == nil {
			conn.Close()
			log.Printf("тестовый xray%s запущен (%d серверов)", p.name, n)
			return ports, nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	p.stopLocked()
	return nil, errors.New("тестовый xray не поднялся за 8 секунд")
}

func socks5Dial(ctx context.Context, proxyAddr, target string) (net.Conn, error) {
	d := net.Dialer{}
	conn, err := d.DialContext(ctx, "tcp", proxyAddr)
	if err != nil {
		return nil, err
	}
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	}
	fail := func(e error) (net.Conn, error) { conn.Close(); return nil, e }
	if _, err := conn.Write([]byte{5, 1, 0}); err != nil {
		return fail(err)
	}
	buf := make([]byte, 262)
	if _, err := io.ReadFull(conn, buf[:2]); err != nil {
		return fail(err)
	}
	host, portStr, _ := net.SplitHostPort(target)
	port, _ := strconv.Atoi(portStr)
	req := []byte{5, 1, 0, 3, byte(len(host))}
	req = append(req, host...)
	req = binary.BigEndian.AppendUint16(req, uint16(port))
	if _, err := conn.Write(req); err != nil {
		return fail(err)
	}
	if _, err := io.ReadFull(conn, buf[:4]); err != nil {
		return fail(err)
	}
	if buf[1] != 0 {
		return fail(fmt.Errorf("socks error %d", buf[1]))
	}
	var skip int
	switch buf[3] {
	case 1:
		skip = 4
	case 4:
		skip = 16
	case 3:
		if _, err := io.ReadFull(conn, buf[:1]); err != nil {
			return fail(err)
		}
		skip = int(buf[0])
	}
	if _, err := io.ReadFull(conn, buf[:skip+2]); err != nil {
		return fail(err)
	}
	conn.SetDeadline(time.Time{})
	return conn, nil
}

// testDelay — как в Mihomo с unified-delay: первый запрос «прогревает»
// соединение (рукопожатие), задержка — время второго запроса.
func testDelay(port int) (int64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*flagTimeout)*time.Millisecond)
	defer cancel()
	addr := "127.0.0.1:" + strconv.Itoa(port)
	tr := &http.Transport{
		Proxy:             nil,
		DialContext:       func(ctx context.Context, _, a string) (net.Conn, error) { return socks5Dial(ctx, addr, a) },
		MaxIdleConns:      1,
		DisableKeepAlives: false,
		ForceAttemptHTTP2: false,
	}
	defer tr.CloseIdleConnections()
	cl := &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	do := func() (time.Duration, error) {
		req, _ := http.NewRequestWithContext(ctx, "GET", *flagURL, nil)
		start := time.Now()
		resp, err := cl.Do(req)
		if err != nil {
			return 0, err
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode >= 500 {
			return 0, fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		return time.Since(start), nil
	}
	first, err := do()
	if err != nil {
		return 0, cleanErr(err)
	}
	second, err := do()
	if err != nil {
		return first.Milliseconds(), nil
	}
	ms := second.Milliseconds()
	if ms < 1 {
		ms = 1
	}
	return ms, nil
}

func cleanErr(err error) error {
	s := err.Error()
	switch {
	case strings.Contains(s, "deadline exceeded"), strings.Contains(s, "timeout"):
		return errors.New("timeout")
	case strings.Contains(s, "EOF"):
		return errors.New("соединение закрыто")
	}
	if i := strings.LastIndex(s, ": "); i > 0 && len(s) > 80 {
		s = s[i+2:]
	}
	return errors.New(s)
}

// ---------------- приложение ----------------

type result struct {
	Delay int64  `json:"delay"`
	Err   string `json:"err,omitempty"`
	T     int64  `json:"t"`
}

type app struct {
	api    *xrayAPI
	probe  *probe // все серверы — для тестов задержки, гасится при простое
	wprobe *probe // только серверы сторожа — работает постоянно

	mu      sync.Mutex
	conf    *xrayConf
	confErr string
	results map[string]result
	testing map[string]bool
	desired map[string]string // balancer -> outbound ("" = авто)

	hist  map[string][]check
	watch *watcher
	dead  map[string]bool // «мёртвые» серверы: не проверяются в фоне
	met   *metrics
	trf   *traffic

	vpnDownSince int64 // под watch.mu: с какого момента VPN не работает (0 — работает)
	tg           *tgSender
	lastRound    int64
}

func (a *app) reload() *xrayConf {
	c, err := loadXrayConf(*flagConfDir)
	a.mu.Lock()
	defer a.mu.Unlock()
	if err != nil {
		a.confErr = err.Error()
		if a.conf == nil {
			a.conf = &xrayConf{}
		}
		return a.conf
	}
	a.conf, a.confErr = c, ""
	return c
}

func (a *app) loadState() {
	b, err := os.ReadFile(*flagState)
	if err != nil {
		return
	}
	var st struct {
		Selected map[string]string `json:"selected"`
		Dead     []string          `json:"dead"`
		LastRead int64             `json:"lastReadEv"`
		Watch    *struct {
			Settings *watchSettings         `json:"settings"`
			Groups   map[string]*watchGroup `json:"groups"`
		} `json:"watch"`
	}
	if json.Unmarshal(b, &st) != nil {
		return
	}
	a.watch.lastReadEv = st.LastRead
	for _, t := range st.Dead {
		a.dead[t] = true
	}
	if st.Selected != nil {
		a.desired = st.Selected
	}
	defer func() { // сразу подключиться к последнему лучшему, не дожидаясь проверок
		a.watch.freshUntil = time.Now().Unix() + 180
		for k, g := range a.watch.groups {
			if g.Enabled && g.Best != "" && contains(g.Servers, g.Best) && a.desired[k] != g.Best {
				log.Printf("[%s] запуск: сразу ставлю последний лучший — %s", k, g.Best)
				a.desired[k] = g.Best
			}
		}
	}()
	if st.Watch != nil {
		if st.Watch.Settings != nil {
			s := *st.Watch.Settings
			s.normalize()
			a.watch.settings = s
		}
		for k, g := range st.Watch.Groups {
			if g != nil {
				a.watch.groups[k] = g
			}
		}
	}
}

func (a *app) saveStateLocked() {
	a.watch.mu.Lock()
	wg := map[string]watchGroup{}
	for k, v := range a.watch.groups {
		wg[k] = *v
	}
	ws := a.watch.settings
	lastRead := a.watch.lastReadEv
	a.watch.mu.Unlock()
	b, _ := json.MarshalIndent(map[string]any{
		"lastReadEv": lastRead,
		"selected":   a.desired,
		"dead":       a.deadListLocked(),
		"watch":      map[string]any{"settings": ws, "groups": wg},
	}, "", "  ")
	tmp := *flagState + ".tmp"
	if os.WriteFile(tmp, b, 0644) == nil {
		os.Rename(tmp, *flagState)
	}
}

// applyLoop следит, чтобы выбор пережил перезапуск Xray (override живёт только в памяти Xray).
func (a *app) applyLoop() {
	for {
		a.applyOnce()
		time.Sleep(10 * time.Second)
	}
}

func (a *app) applyOnce() {
	a.mu.Lock()
	want := map[string]string{}
	for k, v := range a.desired {
		want[k] = v
	}
	a.mu.Unlock()
	for bal, target := range want {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		info, err := a.api.GetBalancerInfo(ctx, bal)
		if err == nil && info.Override == "" && target != "" {
			// Xray только что (пере)запустился — выбор сбросился. Ставим последний лучший,
			// если он здоров, а не просто последний выбранный.
			w := a.watch
			w.mu.Lock()
			w.freshUntil = time.Now().Unix() + 180
			// 0.9: остаёмся на том, где были, если он здоров; на «лучший» — только если текущий плох,
			// а лучший сам свежо проверен и без неудач (иначе вернулись бы на только что упавший).
			if g := w.groups[bal]; g != nil && g.Enabled && g.Best != "" && g.Best != target {
				cur, best := w.health[target], w.health[g.Best]
				curBad := cur == nil || !cur.OK || cur.ConsecFail > 0
				if curBad && best != nil && best.OK && best.ConsecFail == 0 && best.ConsecOK >= 2 {
					w.addEvent(bal, "info", "Xray перезапустился — подключаюсь к лучшему → "+g.Best)
					target = g.Best
				}
			}
			w.mu.Unlock()
			if target != want[bal] {
				a.mu.Lock()
				a.desired[bal] = target
				a.saveStateLocked()
				a.mu.Unlock()
			}
		}
		if err == nil && info.Override != target {
			if err = a.api.OverrideBalancer(ctx, bal, target); err == nil {
				if target == "" {
					log.Printf("[%s] → авто", bal)
				} else {
					log.Printf("[%s] → %s", bal, target)
				}
			}
		}
		cancel()
	}
}

func (a *app) runTests(tags []string) {
	c := a.reload()
	valid := map[string]bool{}
	for _, s := range c.servers {
		valid[s.Tag] = true
	}
	var list []string
	a.mu.Lock()
	for _, t := range tags {
		if valid[t] && !a.testing[t] {
			a.testing[t] = true
			list = append(list, t)
		}
	}
	a.mu.Unlock()
	if len(list) == 0 {
		return
	}
	ports, err := a.probe.ensure(c, nil)
	if err != nil {
		log.Printf("тест: %v", err)
		a.mu.Lock()
		now := time.Now().UnixMilli()
		for _, t := range list {
			delete(a.testing, t)
			a.results[t] = result{Err: err.Error(), T: now}
		}
		a.mu.Unlock()
		return
	}
	sem := make(chan struct{}, max(1, *flagParallel))
	var wg sync.WaitGroup
	round := map[string]result{}
	for _, t := range list {
		wg.Add(1)
		sem <- struct{}{}
		go func(t string) {
			defer wg.Done()
			defer func() { <-sem }()
			d, err := testDelay(ports[t])
			r := result{Delay: d, T: time.Now().UnixMilli()}
			if err != nil {
				r = result{Err: err.Error(), T: r.T}
			}
			a.mu.Lock()
			a.results[t] = r
			round[t] = r
			delete(a.testing, t)
			a.mu.Unlock()
		}(t)
	}
	wg.Wait()
	a.record(round)
	a.probe.mu.Lock()
	a.probe.lastUse = time.Now()
	a.probe.mu.Unlock()
}

func (a *app) handleState(w http.ResponseWriter, r *http.Request) {
	win, _ := strconv.ParseInt(r.URL.Query().Get("win"), 10, 64)
	if win < 600 || win > 7*24*3600 {
		win = scoreWindow
	}
	c := a.reload()
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	var apiErr string
	obs, err := a.api.GetObservatory(ctx)
	if err != nil {
		obs = map[string]obsStatus{}
	}
	groups := make([]group, len(c.groups))
	copy(groups, c.groups)
	a.mu.Lock()
	desired := map[string]string{}
	for k, v := range a.desired {
		desired[k] = v
	}
	a.mu.Unlock()
	for i := range groups {
		info, err := a.api.GetBalancerInfo(ctx, groups[i].Tag)
		if err != nil {
			groups[i].APIError = err.Error()
			apiErr = err.Error()
			continue
		}
		groups[i].Override = info.Override
		if len(info.Principle) > 0 {
			groups[i].Current = info.Principle[0]
		}
		groups[i].Desired = desired[groups[i].Tag]
	}
	a.mu.Lock()
	res := map[string]result{}
	for k, v := range a.results {
		res[k] = v
	}
	testing := []string{}
	for k := range a.testing {
		testing = append(testing, k)
	}
	confErr := a.confErr
	valid := map[string]bool{}
	for _, s := range c.servers {
		valid[s.Tag] = true
	}
	stats := a.statsLocked(valid)
	lastRound := a.lastRound
	a.mu.Unlock()
	hasObsSvc, hasRouteSvc := false, false
	for _, s := range c.apiServices {
		switch strings.ToLower(s) {
		case "observatoryservice":
			hasObsSvc = true
		case "routingservice":
			hasRouteSvc = true
		}
	}
	writeJSON(w, map[string]any{
		"version":     version,
		"groups":      groups,
		"servers":     c.servers,
		"results":     res,
		"observatory": obs,
		"testing":     testing,
		"apiError":    apiErr,
		"apiInConfig": c.apiFound,
		"apiRouting":  hasRouteSvc,
		"apiObs":      hasObsSvc,
		"confError":   confErr,
		"testURL":     *flagURL,
		"stats":       stats,
		"scores":      a.scoresWin(win),
		"dead":        a.deadList(),
		"monitorSec":  int64(*flagMonitor / time.Second),
		"keepSec":     int64(*flagKeep / time.Second),
		"lastRound":   lastRound,
		"watch":       a.watchSnapshot(),
		"balSync":     a.balancerSyncState(c),
	})
}

func (a *app) handleSelect(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Group  string `json:"group"`
		Target string `json:"target"`
	}
	if r.Method != "POST" || json.NewDecoder(r.Body).Decode(&req) != nil || req.Group == "" {
		http.Error(w, "bad request", 400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if err := a.api.OverrideBalancer(ctx, req.Group, req.Target); err != nil {
		w.WriteHeader(502)
		writeJSON(w, map[string]any{"error": err.Error()})
		return
	}
	a.manualSelected(req.Group, req.Target)
	a.mu.Lock()
	a.desired[req.Group] = req.Target
	if req.Target == "" {
		delete(a.desired, req.Group)
	}
	a.saveStateLocked()
	a.mu.Unlock()
	if req.Target == "" {
		log.Printf("[%s] → авто (вручную из панели)", req.Group)
	} else {
		log.Printf("[%s] → %s (вручную из панели)", req.Group, req.Target)
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (a *app) handleTest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Tags []string `json:"tags"`
	}
	if r.Method != "POST" || json.NewDecoder(r.Body).Decode(&req) != nil {
		http.Error(w, "bad request", 400)
		return
	}
	go a.runTests(req.Tags)
	writeJSON(w, map[string]any{"ok": true})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(v)
}

func withAuth(h http.Handler) http.Handler {
	if *flagAuth == "" {
		return h
	}
	user, pass, _ := strings.Cut(*flagAuth, ":")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || subtle.ConstantTimeCompare([]byte(u), []byte(user)) != 1 || subtle.ConstantTimeCompare([]byte(p), []byte(pass)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="xray-selector"`)
			http.Error(w, "unauthorized", 401)
			return
		}
		h.ServeHTTP(w, r)
	})
}

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "-v" || os.Args[1] == "version" || os.Args[1] == "--version") {
		fmt.Println("xray-selector", version)
		return
	}
	resetPass := flag.Bool("reset-password", false, "сбросить пароль панели и выйти")
	flag.Parse()
	if demoPrepare() {
		return
	}
	if *resetPass {
		a := &app{watch: newWatcher(), dead: map[string]bool{}, desired: map[string]string{}}
		a.loadState()
		a.watch.settings.PassHash, a.watch.settings.PassSalt = "", ""
		a.mu.Lock()
		a.saveStateLocked()
		a.mu.Unlock()
		os.Remove(sessionsPath())
		fmt.Println("пароль панели сброшен — перезапустите: /opt/etc/init.d/S99xray-selector restart")
		return
	}
	if *flagLog != "" {
		if f, err := os.OpenFile(*flagLog, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644); err == nil {
			log.SetOutput(f)
		}
	}
	if len(os.Args) > 1 && (os.Args[1] == "-v" || os.Args[1] == "version") {
		fmt.Println("xray-selector", version)
		return
	}
	a := &app{
		api:     newXrayAPI(*flagAPI),
		probe:   &probe{name: " (все)"},
		wprobe:  &probe{name: " (сторож)", base: *flagPortBase + 1000},
		results: map[string]result{},
		hist:    map[string][]check{},
		dead:    map[string]bool{},
		watch:   newWatcher(),
		met:     &metrics{},
		trf:     newTraffic(),
		testing: map[string]bool{},
		desired: map[string]string{},
	}
	a.tg = newTgSender(a)
	a.watch.notify = a.tg.enqueue
	a.loadState()
	a.loadHistory()
	a.loadWatchHistory()
	a.loadEvents()
	a.loadTraffic()
	c := a.reload()
	log.Printf("xray-selector %s: %d серверов, %d групп; панель http://%s", version, len(c.servers), len(c.groups), *flagListen)
	if !c.apiFound {
		log.Printf("ВНИМАНИЕ: в конфигах Xray нет секции \"api\" — выбор сервера работать не будет")
	}
	go a.applyLoop()
	go a.probe.idleLoop()
	go a.wprobe.idleLoop()
	if !demoMode {
		go a.xrayWatchdog()
	}
	go a.monitorLoop()
	go a.watchLoop()
	go a.candLoop()
	if !demoMode { // учебная копия Telegram не трогает
		go a.tg.loop()
		go a.tg.pollLoop()
	}
	go a.metricsLoop()
	go a.accessFollowLoop()
	go a.conntrackLoop()
	go a.xrayStatsLoop()
	go a.ispLoop()
	go a.trafficSaveLoop()
	if !demoMode {
		go a.pinLoop()
	}
	go a.vpnDownLoop()
	go a.digestLoop()
	go a.subLoop()
	go a.updateLoop()
	go a.defaultsLoop()
	go a.rulesLoop()
	go a.hourlyLoop()
	go a.hintsLoop()
	go a.catalogLoop()
	go a.goodSnapshotLoop()
	go a.reserveAutoLoop()
	go a.dnsLoop()
	go a.keeneticHostsLoop()
	go a.tunnelLoop()
	go a.pulseLoop()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/state", a.handleState)
	mux.HandleFunc("/api/select", a.handleSelect)
	mux.HandleFunc("/api/test", a.handleTest)
	mux.HandleFunc("/api/metrics", func(w http.ResponseWriter, r *http.Request) {
		since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
		writeJSON(w, a.metricsSnapshot(since))
	})
	mux.HandleFunc("/api/traffic", a.handleTraffic)
	mux.HandleFunc("/api/flow", a.handleFlow)
	mux.HandleFunc("/api/devices", a.handleDevices)
	mux.HandleFunc("/api/xray-stats", a.handleXrayStats)
	mux.HandleFunc("/api/tg-route", a.handleTgRoute)
	mux.HandleFunc("/api/xray-test", a.handleXrayTest)
	mux.HandleFunc("/api/events", a.handleEvents)
	mux.HandleFunc("/api/server", a.handleServer)
	mux.HandleFunc("/api/backup", a.handleBackup)
	mux.HandleFunc("/manifest.webmanifest", handleManifest)
	for _, n := range []string{"/flags.ttf", "/icon-180.png", "/icon-512.png", "/favicon.png"} {
		mux.HandleFunc(n, a.handleAsset)
	}
	mux.HandleFunc("/api/watch", a.handleWatch)
	mux.HandleFunc("/api/telegram", a.handleTelegram)
	mux.HandleFunc("/api/logs", a.handleLogs)
	mux.HandleFunc("/api/dead", a.handleDead)
	mux.HandleFunc("/api/xray-restart", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "POST only", 405)
			return
		}
		a.watch.mu.Lock()
		a.watch.addEvent("", "info", "перезапуск XKeen/Xray из панели")
		a.watch.pauseUntil = time.Now().Unix() + 40 // не принимать перезапуск за сбой серверов
		a.watch.mu.Unlock()
		if err := a.restartXray(); err != nil {
			w.WriteHeader(500)
			writeJSON(w, map[string]any{"error": err.Error()})
			return
		}
		go func() {
			for _, d := range []time.Duration{2, 4, 8} {
				time.Sleep(d * time.Second)
				a.applyOnce()
			}
		}()
		writeJSON(w, map[string]any{"ok": true})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		b, _ := uiFS.ReadFile("ui.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(b)
	})
	mux.HandleFunc("/login", a.handleLogin)
	mux.HandleFunc("/api/login", a.handleLogin)
	mux.HandleFunc("/api/logout", a.handleLogout)
	mux.HandleFunc("/api/password", a.handlePassword)
	mux.HandleFunc("/api/sites", a.handleSites)
	mux.HandleFunc("/api/diag", a.handleDiag)
	mux.HandleFunc("/api/reserve", a.handleReserve)
	mux.HandleFunc("/api/sub", a.handleSub)
	mux.HandleFunc("/api/maint", a.handleMaint)
	mux.HandleFunc("/api/dns", a.handleDNS)
	mux.HandleFunc("/api/tunnels", a.handleTunnels)
	mux.HandleFunc("/api/pulse", a.handlePulse)
	mux.HandleFunc("/api/rci", a.handleRCI)
	mux.HandleFunc("/api/self-update", a.handleSelfUpdate)
	mux.HandleFunc("/api/about", a.handleAbout)
	mux.HandleFunc("/api/xcfg", a.handleXcfg)
	mux.HandleFunc("/api/update", a.handleUpdate)
	mux.HandleFunc("/api/rules", a.handleRules)
	mux.HandleFunc("/api/wizard", a.handleWizard)
	mux.HandleFunc("/api/demo", a.handleDemo)
	loadSessions()
	srv := &http.Server{Addr: *flagListen, Handler: withAuth(a.authMiddleware(mux)), ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(srv.ListenAndServe())
}

func (a *app) deadListLocked() []string {
	l := make([]string, 0, len(a.dead))
	for t := range a.dead {
		l = append(l, t)
	}
	sort.Strings(l)
	return l
}

func (a *app) deadList() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.deadListLocked()
}

// handleDead: POST {tags, dead} — перенести серверы в «мёртвые» или вернуть.
func (a *app) handleDead(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Tags []string `json:"tags"`
		Dead bool     `json:"dead"`
	}
	if r.Method != "POST" || json.NewDecoder(r.Body).Decode(&req) != nil {
		http.Error(w, "bad request", 400)
		return
	}
	// серверы из списка сторожа и активный сейчас — не трогаем
	busy := map[string]bool{}
	a.watch.mu.Lock()
	for _, g := range a.watch.groups {
		for _, s := range g.Servers {
			busy[s] = true
		}
	}
	a.watch.mu.Unlock()
	a.mu.Lock()
	for _, t := range a.desired {
		busy[t] = true
	}
	var skipped []string
	n := 0
	for _, t := range req.Tags {
		if req.Dead {
			if busy[t] {
				skipped = append(skipped, t)
				continue
			}
			if !a.dead[t] {
				a.dead[t] = true
				n++
			}
		} else if a.dead[t] {
			delete(a.dead, t)
			n++
		}
	}
	a.saveStateLocked()
	a.mu.Unlock()
	if req.Dead {
		log.Printf("в «мёртвые» перенесено серверов: %d", n)
	} else {
		log.Printf("из «мёртвых» возвращено серверов: %d", n)
	}
	writeJSON(w, map[string]any{"ok": true, "changed": n, "skipped": skipped})
}

func isServerTagIn(c *xrayConf, tag string) bool {
	for _, s := range c.servers {
		if s.Tag == tag {
			return true
		}
	}
	return false
}

// watchProbeTags — какие серверы держать в «лёгком» тестовом xray: списки сторожа
// и текущие выбранные (через них же ходит Telegram).
func (a *app) watchProbeTags() []string {
	seen := map[string]bool{}
	var out []string
	a.mu.Lock()
	for _, v := range a.desired {
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	a.mu.Unlock()
	w := a.watch
	w.mu.Lock()
	for _, g := range w.groups {
		for _, s := range g.Servers {
			if !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
	}
	w.mu.Unlock()
	if out == nil {
		out = []string{}
	}
	return out
}
