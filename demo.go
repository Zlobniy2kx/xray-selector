package main

// 2.3, п. 14: учебный режим.
//   xray-selector -demo          — вторая копия панели на порту 9092 со своими копиями конфигов
//                                  и настроек (/opt/etc/xray-selector-demo). XKeen не перезапускается,
//                                  балансировщик настоящего Xray не трогается, Telegram молчит,
//                                  проверочные порты — 23000+.
//   xray-selector -demo-remove   — удалить учебную копию одной командой.
// В панели — жёлтая плашка, «Сбросить учебную копию», «Показать разницу».

import (
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var (
	demoMode     = false
	flagDemo     = flag.Bool("demo", false, "учебный режим: отдельная копия конфигов и настроек на порту 9092, роутер не меняется")
	flagDemoRm   = flag.Bool("demo-remove", false, "удалить учебную копию и выйти")
	realConfDir  string
	realStateDir string
)

func demoDir() string { return filepath.Join(realStateDir, "xray-selector-demo") }

func copyDir(src, dst string, onlyJSON bool) error {
	os.MkdirAll(dst, 0755)
	ents, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range ents {
		if e.IsDir() || (onlyJSON && !strings.HasSuffix(e.Name(), ".json")) {
			continue
		}
		in, err := os.Open(filepath.Join(src, e.Name()))
		if err != nil {
			continue
		}
		out, err := os.Create(filepath.Join(dst, e.Name()))
		if err == nil {
			io.Copy(out, in)
			out.Close()
		}
		in.Close()
	}
	return nil
}

func copyFile(src, dst string) {
	if b, err := os.ReadFile(src); err == nil {
		os.WriteFile(dst, b, 0644)
	}
}

// demoPrepare — вызывается из main() сразу после flag.Parse.
func demoPrepare() (exit bool) {
	realConfDir, realStateDir = *flagConfDir, filepath.Dir(*flagState)
	if *flagDemoRm {
		if err := os.RemoveAll(demoDir()); err != nil {
			fmt.Println("не удалось удалить:", err)
		} else {
			fmt.Println("учебная копия удалена:", demoDir())
		}
		return true
	}
	if !*flagDemo {
		return false
	}
	demoMode = true
	dd := demoDir()
	if _, err := os.Stat(filepath.Join(dd, "configs")); err != nil {
		demoReset()
	}
	*flagConfDir = filepath.Join(dd, "configs")
	*flagState = filepath.Join(dd, "xray-selector.json")
	*flagHistory = filepath.Join(dd, "history.json")
	*flagPortBase = 23000
	if h, _, err := net.SplitHostPort(*flagListen); err == nil {
		*flagListen = net.JoinHostPort(h, "9092")
	}
	if *flagLog != "" {
		*flagLog = filepath.Join(dd, "xray-selector.log")
	}
	return false
}

// demoReset — заново скопировать настоящие конфиги и настройки в учебную копию.
func demoReset() error {
	dd := demoDir()
	os.RemoveAll(dd)
	if err := copyDir(realConfDir, filepath.Join(dd, "configs"), true); err != nil {
		return err
	}
	for _, f := range []string{"xray-selector.json", "xray-selector-rules.json", "xray-selector-traffic.json", "xray-selector-hourly.json", "xray-selector-catalog.json"} {
		copyFile(filepath.Join(realStateDir, f), filepath.Join(dd, f))
	}
	return nil
}

func (a *app) handleDemo(w http.ResponseWriter, r *http.Request) {
	if !demoMode {
		http.Error(w, "это не учебная копия", 400)
		return
	}
	if r.Method == "POST" {
		if err := demoReset(); err != nil {
			w.WriteHeader(500)
			writeJSON(w, map[string]any{"error": err.Error()})
			return
		}
		a.loadState()
		writeJSON(w, map[string]any{"ok": true})
		return
	}
	// разница учебной копии и настоящих конфигов
	type fd struct {
		Name    string   `json:"name"`
		State   string   `json:"state"` // changed | added | removed
		Removed []string `json:"removed,omitempty"`
		Added   []string `json:"added,omitempty"`
	}
	names := map[string]bool{}
	for _, d := range []string{realConfDir, *flagConfDir} {
		fs, _ := filepath.Glob(filepath.Join(d, "*.json"))
		for _, f := range fs {
			names[filepath.Base(f)] = true
		}
	}
	var keys []string
	for k := range names {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []fd
	for _, n := range keys {
		ra, e1 := os.ReadFile(filepath.Join(realConfDir, n))
		da, e2 := os.ReadFile(filepath.Join(*flagConfDir, n))
		switch {
		case e1 != nil && e2 == nil:
			out = append(out, fd{Name: n, State: "added"})
		case e1 == nil && e2 != nil:
			out = append(out, fd{Name: n, State: "removed"})
		case string(ra) != string(da):
			rm, ad := diffLines(string(ra), string(da))
			out = append(out, fd{Name: n, State: "changed", Removed: rm, Added: ad})
		}
	}
	rb, _ := os.ReadFile(filepath.Join(realStateDir, "xray-selector-rules.json"))
	db, _ := os.ReadFile(filepath.Join(demoDir(), "xray-selector-rules.json"))
	writeJSON(w, map[string]any{"files": out, "rulesChanged": string(rb) != string(db), "dir": demoDir(),
		"remove": "xray-selector -demo-remove"})
}
