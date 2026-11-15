package main

// Просмотр журналов прямо в панели: access/error Xray и журнал самой программы.
// Читаются только известные файлы, с конца, не больше ~1 МБ.

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// xrayLogPaths — пути access/error из конфига Xray (секция log), иначе стандартные для XKeen.
func xrayLogPaths(dir string) (access, errlog string) {
	access, errlog = "/opt/var/log/xray/access.log", "/opt/var/log/xray/error.log"
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var doc struct {
			Log *struct {
				Access string `json:"access"`
				Error  string `json:"error"`
			} `json:"log"`
		}
		if json.Unmarshal(stripJSONComments(b), &doc) == nil && doc.Log != nil {
			if doc.Log.Access != "" && doc.Log.Access != "none" {
				access = doc.Log.Access
			}
			if doc.Log.Error != "" && doc.Log.Error != "none" {
				errlog = doc.Log.Error
			}
		}
	}
	return
}

func tailFile(path string, maxBytes int64) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	start := int64(0)
	if st.Size() > maxBytes {
		start = st.Size() - maxBytes
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return nil, err
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	s := string(b)
	if start > 0 { // первая строка, скорее всего, обрезана
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[i+1:]
		}
	}
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return []string{}, nil
	}
	return strings.Split(s, "\n"), nil
}

func (a *app) handleLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	access, errlog := xrayLogPaths(*flagConfDir)
	files := map[string]string{"access": access, "error": errlog, "selector": *flagLog}
	which := q.Get("file")
	if which == "keenetic" {
		a.handleKeeneticLog(w, r)
		return
	}
	path, ok := files[which]
	if !ok || path == "" {
		w.WriteHeader(400)
		writeJSON(w, map[string]any{"error": "нет такого журнала"})
		return
	}
	limit, _ := strconv.Atoi(q.Get("lines"))
	if limit <= 0 || limit > 2000 {
		limit = 300
	}
	lines, err := tailFile(path, 1<<20)
	if err != nil {
		writeJSON(w, map[string]any{"lines": []string{}, "names": map[string]string{}, "path": path, "error": err.Error()})
		return
	}
	names := a.deviceNames()
	// имя устройства в фильтре → его IP-адреса (в журнале Xray только IP)
	query := strings.ToLower(strings.TrimSpace(q.Get("q")))
	var devIPs []string
	if query != "" {
		for ip, n := range names {
			if strings.Contains(strings.ToLower(n), strings.TrimPrefix(query, "-")) && !strings.HasPrefix(query, "-") {
				devIPs = append(devIPs, "from "+ip+":")
			}
		}
		if len(devIPs) > 0 {
			query = ""
		}
	}
	// период: только строки за последние N минут
	sinceMin, _ := strconv.Atoi(q.Get("since"))
	var since time.Time
	if sinceMin > 0 {
		since = time.Now().Add(-time.Duration(sinceMin) * time.Minute)
	}
	loc := a.userLoc()
	// фильтр: несколько слов через пробел — все должны быть в строке; «-слово» — исключить
	var inc, exc []string
	for _, t := range strings.Fields(query) {
		if strings.HasPrefix(t, "-") && len(t) > 1 {
			exc = append(exc, t[1:])
		} else {
			inc = append(inc, t)
		}
	}
	mode := q.Get("mode") // vpn | direct | block | ""
	var out []string
	for i := len(lines) - 1; i >= 0 && len(out) < limit; i-- {
		l := lines[i]
		ts, tsOK := logTime(l)
		if !since.IsZero() && tsOK && ts.Before(since) {
			break // дальше только старше
		}
		ll := strings.ToLower(l)
		match := true
		if len(devIPs) > 0 {
			match = false
			for _, d := range devIPs {
				if strings.Contains(l, d) {
					match = true
					break
				}
			}
		}
		for _, t := range inc {
			if !strings.Contains(ll, t) {
				match = false
				break
			}
		}
		for _, t := range exc {
			if match && strings.Contains(ll, t) {
				match = false
			}
		}
		if match && mode != "" && which == "access" {
			switch mode {
			case "direct":
				match = strings.Contains(l, "-> direct]")
			case "block":
				match = strings.Contains(l, "-> block]")
			case "vpn":
				match = strings.Contains(l, "accepted") && !strings.Contains(l, "-> direct]") && !strings.Contains(l, "-> block]")
			}
		}
		if match {
			if tsOK { // время — в часовом поясе пользователя
				l = ts.In(loc).Format("2006/01/02 15:04:05") + l[19:]
			}
			out = append(out, l)
		}
	}
	// вернуть в обычном порядке (старые сверху)
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	if out == nil {
		out = []string{}
	}
	writeJSON(w, map[string]any{"lines": out, "path": path, "total": len(lines), "names": names, "devices": len(devIPs) > 0})
}

// logTime — время в начале строки журнала («2026/11/08 10:48:35…»), в часовом поясе роутера.
func logTime(l string) (time.Time, bool) {
	if len(l) < 19 || l[4] != '/' || l[10] != ' ' {
		return time.Time{}, false
	}
	t, err := time.ParseInLocation("2006/01/02 15:04:05", l[:19], time.Local)
	return t, err == nil
}

// userLoc — часовой пояс пользователя (настройка «Часовой пояс»).
func (a *app) userLoc() *time.Location {
	a.watch.mu.Lock()
	off := a.watch.settings.TzOffsetMin
	a.watch.mu.Unlock()
	return time.FixedZone("", off*60)
}

// handleKeeneticLog — системный журнал самого роутера (show log): обрывы провайдера,
// переключения каналов, Wi-Fi, DHCP — то, чего не видно в журналах Xray.
func (a *app) handleKeeneticLog(w http.ResponseWriter, r *http.Request) {
	// 0.9: формат и путь журнала в RCI разные у прошивок — пробуем несколько
	var b []byte
	var err error
	for _, path := range []string{"show/log?max-lines=600", "show/log", "show/log/max-lines/600"} {
		var bb []byte
		if bb, err = rciGet(path); err == nil && len(bb) > 20 && strings.Contains(string(bb), "message") {
			b = bb
			break
		} else if err == nil && b == nil {
			b = bb
		}
	}
	if b == nil && err != nil {
		writeJSON(w, map[string]any{"lines": []string{}, "names": map[string]string{}, "path": "Keenetic", "error": err.Error()})
		return
	}
	var root any
	json.Unmarshal(b, &root)
	type ent struct {
		t    string
		line string
	}
	var es []ent
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if m, ok := x["message"]; ok {
				msg := ""
				switch mm := m.(type) {
				case string:
					msg = mm
				case map[string]any:
					msg, _ = mm["message"].(string)
					if l, _ := mm["label"].(string); l != "" {
						msg = l + " " + msg
					}
				}
				ts, _ := x["timestamp"].(string)
				id, _ := x["ident"].(string)
				es = append(es, ent{ts, strings.TrimSpace(ts + " " + id + ": " + msg)})
				return
			}
			for _, y := range x {
				walk(y)
			}
		case []any:
			for _, y := range x {
				walk(y)
			}
		}
	}
	walk(root)
	sort.SliceStable(es, func(i, j int) bool { return es[i].t < es[j].t })
	qq := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	imp := r.URL.Query().Get("imp") == "1"
	var out []string
	for _, e := range es {
		if imp && !importantKeenetic(e.line) {
			continue
		}
		if qq == "" || strings.Contains(strings.ToLower(e.line), qq) {
			out = append(out, e.line)
		}
	}
	if out == nil {
		out = []string{}
	}
	resp := map[string]any{"lines": out, "names": a.deviceNames(), "path": "журнал Keenetic", "total": len(es)}
	if len(es) == 0 {
		raw := strings.TrimSpace(string(b))
		if len(raw) > 400 {
			raw = raw[:400] + "…"
		}
		resp["error"] = "Не удалось разобрать журнал Keenetic. Начало ответа роутера: " + raw
	}
	writeJSON(w, resp)
}

// importantKeenetic — строки журнала Keenetic, важные для интернета и VPN.
func importantKeenetic(l string) bool {
	x := strings.ToLower(l)
	for _, k := range []string{"pppoe", "gigabitethernet", "wireguard", "proxy", "openvpn", "ipsec", "sstp", "l2tp", "pptp",
		"connected", "disconnected", "link up", "link down", "is up", "is down", "lost", "dhcp", "dns", "https-dns",
		"reboot", "restart", "started", "stopped", "error", "fail", "ping-check", "internet", "wan", "isp", "xkeen", "xray"} {
		if strings.Contains(x, k) {
			return true
		}
	}
	return false
}
