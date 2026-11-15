package main

// Обслуживание: даты баз geosite/geoip, обновление баз (xkeen -ug), версии,
// обновление самой панели загрузкой архива (без scp).

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

var maintMu sync.Mutex

type datInfo struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	Mod  int64  `json:"mod"`
}

func datFiles() []datInfo {
	var out []datInfo
	fs, _ := filepath.Glob(filepath.Join(assetDir(), "*.dat"))
	for _, f := range fs {
		if st, err := os.Stat(f); err == nil {
			out = append(out, datInfo{filepath.Base(f), st.Size(), st.ModTime().Unix()})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func cmdOut(timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "PATH=/opt/sbin:/opt/bin:/opt/usr/sbin:/opt/usr/bin:/usr/sbin:/usr/bin:/sbin:/bin")
	cmd.Stdin = strings.NewReader("\n\n\n") // на случай вопросов — ответ по умолчанию
	b, err := cmd.CombinedOutput()
	return stripANSI(string(b)), err
}

func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && !(s[j] >= '@' && s[j] <= '~') {
				j++
			}
			i = j
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func xrayVersion() string {
	out, _ := cmdOut(10*time.Second, *flagXray, "version")
	if l := strings.SplitN(out, "\n", 2)[0]; l != "" {
		return strings.TrimSpace(l)
	}
	return ""
}

func (a *app) handleMaint(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeJSON(w, map[string]any{"dat": datFiles(), "assetDir": assetDir(), "xray": xrayVersion(), "selector": version,
			"arch": runtime.GOARCH, "backups": listBackups(), "goodSnapshot": goodSnapshotTime()})
		return
	}
	var req struct {
		Action string `json:"action"`
	}
	jsonDecode(r, &req)
	if !maintMu.TryLock() {
		w.WriteHeader(409)
		writeJSON(w, map[string]any{"error": "уже выполняется другая команда"})
		return
	}
	defer maintMu.Unlock()
	switch req.Action {
	case "geo":
		if demoMode {
			w.WriteHeader(400)
			writeJSON(w, map[string]any{"error": "в учебном режиме базы не обновляются"})
			return
		}
		a.watch.mu.Lock()
		a.watch.addEvent("", "info", "обновляю базы geosite/geoip (xkeen -ug)")
		a.watch.pauseUntil = time.Now().Unix() + 120
		a.watch.mu.Unlock()
		restartMu.Lock()
		out, err := cmdOut(8*time.Minute, "xkeen", "-ug")
		restartMu.Unlock()
		go a.reapply()
		ok := a.waitXrayAPI(40 * time.Second)
		msg := "базы обновлены"
		if err != nil || !ok {
			msg = "обновление баз завершилось с ошибкой"
			if !ok {
				msg += "; Xray не отвечает — перезапускаю"
				a.restartXrayWait()
			}
		}
		a.watch.mu.Lock()
		a.watch.addEvent("", "info", msg)
		a.watch.mu.Unlock()
		writeJSON(w, map[string]any{"ok": err == nil && ok, "output": tail(out, 4000), "dat": datFiles()})
	case "snapshot":
		a.saveGoodSnapshot()
		writeJSON(w, map[string]any{"ok": true, "goodSnapshot": goodSnapshotTime()})
	default:
		w.WriteHeader(400)
		writeJSON(w, map[string]any{"error": "неизвестное действие"})
	}
}

func tail(s string, n int) string {
	if len(s) > n {
		return "…" + s[len(s)-n:]
	}
	return s
}

func goodSnapshotTime() string {
	b, _ := os.ReadFile(filepath.Join(backupRoot(), "good", "_what.txt"))
	return strings.TrimSpace(string(b))
}

// ---- обновление панели загрузкой архива ----

func binName() string {
	switch runtime.GOARCH {
	case "arm64":
		return "xray-selector-arm64"
	case "mipsle":
		return "xray-selector-mipsle"
	case "mips":
		return "xray-selector-mips"
	}
	return "xray-selector-" + runtime.GOARCH
}

func (a *app) handleSelfUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "POST only", 405)
		return
	}
	fail := func(err error) {
		w.WriteHeader(400)
		writeJSON(w, map[string]any{"error": err.Error()})
	}
	if demoMode {
		fail(errors.New("в учебном режиме панель не обновляется — обновите основную"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<20)
	if err := r.ParseMultipartForm(16 << 20); err != nil {
		fail(err)
		return
	}
	f, _, err := r.FormFile("file")
	if err != nil {
		fail(errors.New("файл не получен"))
		return
	}
	defer f.Close()
	newVer, err := installArchive(f)
	if err != nil {
		fail(err)
		return
	}
	a.watch.mu.Lock()
	a.watch.addEvent("", "info", "панель обновлена: "+version+" → "+newVer+", перезапускаюсь")
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

// installArchive — взять из архива xray-selector.tar.gz файл для этой архитектуры,
// проверить, что он запускается, и подменить текущий (старый — в .bak).
func installArchive(f io.Reader) (string, error) {
	fail := func(err error) (string, error) { return "", err }
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fail(errors.New("это не архив .tar.gz"))
	}
	tr := tar.NewReader(gz)
	want := binName()
	var bin []byte
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fail(err)
		}
		if filepath.Base(h.Name) == want && h.Typeflag == tar.TypeReg {
			bin, err = io.ReadAll(io.LimitReader(tr, 64<<20))
			if err != nil {
				return fail(err)
			}
		}
	}
	if len(bin) < 1<<20 {
		return fail(errors.New("в архиве нет файла " + want))
	}
	exe := "/opt/sbin/xray-selector"
	if p, err := os.Executable(); err == nil {
		exe = p
	}
	newp := exe + ".new"
	if err := os.WriteFile(newp, bin, 0755); err != nil {
		return fail(err)
	}
	out, err := cmdOut(10*time.Second, newp, "-v")
	if err != nil || !strings.Contains(out, "xray-selector") {
		os.Remove(newp)
		return fail(errors.New("новый файл не запускается на этом роутере: " + strings.TrimSpace(out)))
	}
	newVer := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(out), "xray-selector"))
	if b, err := os.ReadFile(exe); err == nil {
		os.WriteFile(exe+".bak", b, 0755)
	}
	if err := os.Rename(newp, exe); err != nil {
		return fail(err)
	}
	return newVer, nil
}

var _ = bytes.MinRead

func jsonDecode(r *http.Request, v any) error { return json.NewDecoder(r.Body).Decode(v) }
