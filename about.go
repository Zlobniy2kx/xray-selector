package main

// 2.3: «О программе», адрес веб-интерфейса Keenetic (п. 31), поиск XKeen UI (п. 30),
// просмотр и правка JSON-конфигов Xray в экспертном режиме (п. 30) — через то же
// безопасное применение (xray -test → резервная копия → перезапуск → откат).

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	appAuthor  = "Злобыч"
	appDesc    = "Панель управления Xray/XKeen для роутеров Keenetic: мониторинг и автовыбор серверов"
	supportURL = "https://boosty.to/xrayselector" // кнопка «♥ Сказать спасибо разработчику»
	projectURL = "https://github.com/Zlobniy2kx/xray-selector"
	githubRepo = "Zlobniy2kx/xray-selector" // для проверки новых версий
)

var routerWeb struct {
	sync.Mutex
	t    time.Time
	port int
	from string
}

var reHTTPPort = regexp.MustCompile(`(?m)^\s*ip http (?:port|listen(?: port)?)\s+(\d{2,5})\b`)

// keeneticWebPort — порт веб-интерфейса роутера. Сначала разобранная конфигурация
// (show/rc/ip/http), затем текст running-config, по умолчанию 80.
// ПРОВЕРИТЬ на реальном RCI: какое поле/строка хранит порт.
func keeneticWebPort() (int, string) {
	routerWeb.Lock()
	defer routerWeb.Unlock()
	if !routerWeb.t.IsZero() && time.Since(routerWeb.t) < 10*time.Minute {
		return routerWeb.port, routerWeb.from
	}
	port, from := 80, "по умолчанию"
	if b, err := rciGet("show/rc/ip/http"); err == nil {
		var m map[string]any
		if json.Unmarshal(b, &m) == nil {
			if p := findPort(m); p > 0 {
				port, from = p, "rc/ip/http"
			}
		}
	}
	if from == "по умолчанию" {
		if b, err := rciGet("show/running-config"); err == nil {
			text := string(b)
			var m map[string]any
			if json.Unmarshal(b, &m) == nil {
				if arr, ok := m["message"].([]any); ok {
					var sb strings.Builder
					for _, x := range arr {
						sb.WriteString(fmt.Sprint(x))
						sb.WriteByte('\n')
					}
					text = sb.String()
				}
			}
			if mm := reHTTPPort.FindStringSubmatch(text); mm != nil {
				if p, _ := strconv.Atoi(mm[1]); p > 0 && p < 65536 {
					port, from = p, "running-config"
				}
			}
		}
	}
	routerWeb.t, routerWeb.port, routerWeb.from = time.Now(), port, from
	return port, from
}

func findPort(v any) int {
	switch x := v.(type) {
	case map[string]any:
		for k, y := range x {
			if strings.EqualFold(k, "port") {
				switch p := y.(type) {
				case float64:
					return int(p)
				case string:
					n, _ := strconv.Atoi(p)
					return n
				case map[string]any:
					if n := findPort(p); n > 0 {
						return n
					}
				}
			}
		}
		for k, y := range x {
			if strings.Contains(strings.ToLower(k), "ssl") {
				continue // порт HTTPS не нужен
			}
			if n := findPort(y); n > 0 {
				return n
			}
		}
	}
	return 0
}

// XKeen UI — сторонняя веб-панель для XKeen. С роутера её не удаляем, только находим.
func findXkeenUI() (bool, int) {
	for _, p := range []int{1000, 1001, 9090} {
		if p == listenPort() {
			continue
		}
		c, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(p), 400*time.Millisecond)
		if err != nil {
			continue
		}
		c.SetDeadline(time.Now().Add(time.Second))
		fmt.Fprintf(c, "GET / HTTP/1.0\r\nHost: 127.0.0.1\r\n\r\n")
		buf := make([]byte, 4096)
		n, _ := c.Read(buf)
		c.Close()
		low := strings.ToLower(string(buf[:n]))
		if strings.Contains(low, "xkeen") || (p == 1000 && strings.HasPrefix(low, "http/")) {
			return true, p
		}
	}
	for _, f := range []string{"/opt/share/xkeen-ui", "/opt/etc/xkeen-ui"} {
		if _, err := os.Stat(f); err == nil {
			return true, 1000
		}
	}
	return false, 0
}

func listenPort() int {
	_, p, err := net.SplitHostPort(*flagListen)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(p)
	return n
}

func (a *app) handleAbout(w http.ResponseWriter, r *http.Request) {
	port, from := keeneticWebPort()
	ui, uiPort := findXkeenUI()
	writeJSON(w, map[string]any{
		"name": "xray-selector", "version": version, "author": appAuthor, "desc": appDesc,
		"supportURL": supportURL, "projectURL": projectURL,
		"routerPort": port, "routerPortFrom": from,
		"xkeenUI": ui, "xkeenUIPort": uiPort,
		"guard": guard.snapshot(), "demo": demoMode,
	})
}

// ---- JSON-конфиги Xray (экспертный режим) ----

var reCfgName = regexp.MustCompile(`^[A-Za-z0-9_.\-]+\.json$`)

func (a *app) handleXcfg(w http.ResponseWriter, r *http.Request) {
	dir := *flagConfDir
	if r.Method == http.MethodGet {
		name := r.URL.Query().Get("name")
		if name == "" {
			fs, _ := filepath.Glob(filepath.Join(dir, "*.json"))
			out := []map[string]any{}
			for _, f := range fs {
				st, err := os.Stat(f)
				if err != nil {
					continue
				}
				out = append(out, map[string]any{"name": filepath.Base(f), "size": st.Size(), "mtime": st.ModTime().Unix()})
			}
			writeJSON(w, map[string]any{"dir": dir, "files": out})
			return
		}
		if !reCfgName.MatchString(name) {
			http.Error(w, "плохое имя файла", 400)
			return
		}
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			http.Error(w, err.Error(), 404)
			return
		}
		writeJSON(w, map[string]any{"name": name, "content": string(b)})
		return
	}
	var req struct {
		Name    string `json:"name"`
		Content string `json:"content"`
		Check   bool   `json:"check"` // только проверить, не записывать
	}
	if err := jsonDecode(r, &req); err != nil || !reCfgName.MatchString(req.Name) {
		http.Error(w, "плохой запрос", 400)
		return
	}
	var tmp any
	if err := json.Unmarshal(stripJSONComments([]byte(req.Content)), &tmp); err != nil {
		w.WriteHeader(400)
		writeJSON(w, map[string]any{"error": "это не JSON: " + err.Error()})
		return
	}
	ch := []cfgChange{{Name: req.Name, Content: req.Content}}
	if req.Check {
		if err := testChanges(ch); err != nil {
			w.WriteHeader(400)
			writeJSON(w, map[string]any{"error": "xray -test: " + err.Error()})
			return
		}
		writeJSON(w, map[string]any{"ok": true, "checked": true})
		return
	}
	if demoMode {
		w.WriteHeader(400)
		writeJSON(w, map[string]any{"error": "в учебном режиме конфиги XKeen не меняются"})
		return
	}
	if err := a.applyConfig("правка "+req.Name+" вручную", ch); err != nil {
		w.WriteHeader(500)
		writeJSON(w, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}
