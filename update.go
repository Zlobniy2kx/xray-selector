package main

// 2.3, п. 11 и 13: загрузки из интернета через рабочий VPN-сервер, запасной путь — напрямую.
//   • проверка новых версий панели на GitHub (раз в сутки), установка в один клик;
//   • smartGet — общий помощник (подписка, списки адресов сервисов, обновления).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// smartGet — GET сначала через активный VPN-сервер (тестовый xray), потом напрямую.
func (a *app) smartGet(url, ua string, limit int64, timeout time.Duration) ([]byte, http.Header, string, error) {
	try := func(cl *http.Client) ([]byte, http.Header, error) {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return nil, nil, err
		}
		if ua != "" {
			req.Header.Set("User-Agent", ua)
		}
		resp, err := cl.Do(req)
		if err != nil {
			return nil, nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return nil, nil, fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		b, err := io.ReadAll(io.LimitReader(resp.Body, limit))
		return b, resp.Header, err
	}
	var errs []string
	if demoMode {
		errs = append(errs, "учебный режим")
	} else if active := a.activeServer(); active != "" {
		c := a.reload()
		if ports, err := a.wprobe.ensure(c, a.watchProbeTags()); err == nil && ports[active] > 0 {
			cl, tr := socksClient(ports[active])
			cl.CheckRedirect = nil
			b, h, err := try(cl)
			tr.CloseIdleConnections()
			if err == nil {
				return b, h, "через " + active, nil
			}
			errs = append(errs, "через VPN: "+explainNetErr(err))
		}
	}
	b, h, err := try(&http.Client{Timeout: timeout})
	if err == nil {
		return b, h, "напрямую", nil
	}
	errs = append(errs, "напрямую: "+explainNetErr(err))
	return nil, nil, "", errors.New(strings.Join(errs, "; "))
}

func (a *app) activeServer() string {
	c := a.reload()
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, g := range c.groups {
		if v := a.desired[g.Tag]; v != "" && !isReserveTag(v) {
			return v
		}
	}
	return ""
}

// ---- новые версии ----

var upd struct {
	sync.Mutex
	Checked int64  `json:"checked"`
	Latest  string `json:"latest"`
	Notes   string `json:"notes"`
	Page    string `json:"page"`
	Asset   string `json:"asset"`
	Via     string `json:"via"`
	Err     string `json:"err"`
	Busy    bool   `json:"busy"`
}

// verNewer: "2.3.1" > "2.3.0"
func verNewer(a, b string) bool {
	pa, pb := strings.Split(strings.TrimPrefix(a, "v"), "."), strings.Split(strings.TrimPrefix(b, "v"), ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(strings.TrimFunc(pa[i], func(r rune) bool { return r < '0' || r > '9' }))
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(strings.TrimFunc(pb[i], func(r rune) bool { return r < '0' || r > '9' }))
		}
		if x != y {
			return x > y
		}
	}
	return false
}

func (a *app) checkUpdate() error {
	if githubRepo == "" {
		return errors.New("адрес проекта на GitHub ещё не задан")
	}
	b, _, via, err := a.smartGet("https://api.github.com/repos/"+githubRepo+"/releases/latest", "xray-selector/"+version, 1<<20, 20*time.Second)
	upd.Lock()
	defer upd.Unlock()
	upd.Checked, upd.Via = time.Now().Unix(), via
	if err != nil {
		upd.Err = err.Error()
		return err
	}
	var rel struct {
		Tag    string `json:"tag_name"`
		Body   string `json:"body"`
		URL    string `json:"html_url"`
		Assets []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(b, &rel); err != nil {
		upd.Err = "непонятный ответ GitHub"
		return err
	}
	upd.Err, upd.Latest, upd.Page, upd.Asset = "", strings.TrimPrefix(rel.Tag, "v"), rel.URL, ""
	if len(rel.Body) > 1500 {
		rel.Body = rel.Body[:1500] + "…"
	}
	upd.Notes = rel.Body
	for _, as := range rel.Assets {
		if as.Name == "xray-selector.tar.gz" {
			upd.Asset = as.URL
		}
	}
	return nil
}

func (a *app) updateLoop() {
	time.Sleep(5 * time.Minute)
	for {
		if githubRepo != "" && !demoMode {
			guard.pace(10 * time.Minute)
			a.checkUpdate()
		}
		time.Sleep(24 * time.Hour)
	}
}

func (a *app) handleUpdate(w http.ResponseWriter, r *http.Request) {
	snap := func() map[string]any {
		upd.Lock()
		defer upd.Unlock()
		return map[string]any{"repo": githubRepo, "current": version, "checked": upd.Checked, "latest": upd.Latest, "notes": upd.Notes,
			"page": upd.Page, "canInstall": upd.Asset != "", "via": upd.Via, "err": upd.Err, "busy": upd.Busy,
			"newer": upd.Latest != "" && verNewer(upd.Latest, version)}
	}
	if r.Method != "POST" {
		writeJSON(w, snap())
		return
	}
	var req struct {
		Action string `json:"action"` // check | install
	}
	jsonDecode(r, &req)
	if req.Action == "check" {
		a.checkUpdate()
		writeJSON(w, snap())
		return
	}
	if req.Action != "install" {
		http.Error(w, "bad action", 400)
		return
	}
	if demoMode {
		http.Error(w, "в учебном режиме обновление выключено", 400)
		return
	}
	upd.Lock()
	asset, busy := upd.Asset, upd.Busy
	if asset != "" && !busy {
		upd.Busy = true
	}
	upd.Unlock()
	if asset == "" || busy {
		http.Error(w, "нечего устанавливать (сначала «Проверить»)", 400)
		return
	}
	defer func() { upd.Lock(); upd.Busy = false; upd.Unlock() }()
	b, _, via, err := a.smartGet(asset, "xray-selector/"+version, 64<<20, 3*time.Minute)
	if err != nil {
		w.WriteHeader(502)
		writeJSON(w, map[string]any{"error": "не удалось скачать: " + err.Error()})
		return
	}
	newVer, err := installArchive(bytes.NewReader(b))
	if err != nil {
		w.WriteHeader(400)
		writeJSON(w, map[string]any{"error": err.Error()})
		return
	}
	a.watch.mu.Lock()
	a.watch.addEvent("", "info", "панель обновлена с GitHub ("+via+"): "+version+" → "+newVer+", перезапускаюсь")
	a.watch.mu.Unlock()
	a.saveEvents()
	writeJSON(w, map[string]any{"ok": true, "version": newVer})
	go func() {
		time.Sleep(time.Second)
		cmd := exec.Command("sh", "-c", "sleep 1; /opt/etc/init.d/S99xray-selector restart >/dev/null 2>&1")
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		cmd.Start()
	}()
}
