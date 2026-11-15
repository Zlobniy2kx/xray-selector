package main

// Адреса Telegram — через VPN. Telegram подключается не только по доменам, но и
// напрямую по IP; если этих адресов нет в правилах XKeen, такие соединения уходят
// «напрямую», провайдер их режет, и приложение бесконечно переподключается.
// Добавляем отдельное правило первым в routing.rules: ip Telegram → группа сторожа.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var telegramCIDRs = []string{
	"91.105.192.0/23", "91.108.4.0/22", "91.108.8.0/22", "91.108.12.0/22", "91.108.16.0/22",
	"91.108.20.0/22", "91.108.56.0/22", "95.161.64.0/20", "149.154.160.0/20", "185.76.151.0/24",
}

const tgRuleMark = "91.105.192.0/23"

func tgRouteConfigured(dir string) bool {
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	for _, f := range files {
		if b, err := os.ReadFile(f); err == nil && strings.Contains(string(b), tgRuleMark) {
			return true
		}
	}
	return false
}

// xrayTest — проверить конфиг тем же Xray, не запуская его.
func xrayTest(dir string) error {
	cmd := exec.Command(*flagXray, "run", "-test", "-confdir", dir)
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "XRAY_LOCATION_CONFDIR=") && !strings.HasPrefix(kv, "XRAY_LOCATION_CONFIG=") {
			env = append(env, kv)
		}
	}
	cmd.Env = append(env, xrayAssetEnv()...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		s := strings.TrimSpace(string(out))
		if len(s) > 400 {
			s = s[len(s)-400:]
		}
		return fmt.Errorf("%v: %s", err, s)
	}
	return nil
}

func addTelegramRoute(dir, balancer string) error {
	if tgRouteConfigured(dir) {
		return nil
	}
	file, err := findRoutingFile(dir)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	s := string(raw)
	rk := findKey(s, "rules", 0, len(s))
	if rk < 0 {
		return errors.New(filepath.Base(file) + ": не нашёл routing.rules")
	}
	o := skipWS(s, rk)
	if o >= len(s) || s[o] != '[' {
		return errors.New(filepath.Base(file) + ": routing.rules — не список")
	}
	ips, _ := json.Marshal(telegramCIDRs)
	ind := lineIndent(s, o) + "  "
	rule := fmt.Sprintf(`{ "type": "field", "ip": %s, "balancerTag": %q }`, string(ips), balancer)
	inner := strings.TrimSpace(s[o+1 : matchClose(s, o)])
	sep := ","
	if inner == "" {
		sep = ""
	}
	ns := s[:o+1] + "\n" + ind + "// xray-selector: адреса Telegram — через VPN\n" + ind + rule + sep + s[o+1:]
	backupOnce(file, s)
	if err := writeChecked(file, ns); err != nil {
		return err
	}
	if err := xrayTest(dir); err != nil { // Xray не принял — вернуть как было
		os.WriteFile(file, raw, 0644)
		return errors.New("Xray не принял правило, всё возвращено как было: " + err.Error())
	}
	return nil
}

func (a *app) handleTgRoute(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeJSON(w, map[string]any{"configured": tgRouteConfigured(*flagConfDir)})
		return
	}
	bal := ""
	a.watch.mu.Lock()
	for g, wg := range a.watch.groups {
		if wg.Enabled && (bal == "" || g == "auto") {
			bal = g
		}
	}
	a.watch.mu.Unlock()
	if bal == "" {
		c := a.reload()
		if len(c.groups) > 0 {
			bal = c.groups[0].Tag
		}
	}
	if bal == "" {
		w.WriteHeader(400)
		writeJSON(w, map[string]any{"error": "не нашёл группу (балансировщик) для VPN"})
		return
	}
	if err := addTelegramRoute(*flagConfDir, bal); err != nil {
		w.WriteHeader(400)
		writeJSON(w, map[string]any{"error": err.Error()})
		return
	}
	a.watch.mu.Lock()
	a.watch.addEvent("", "info", "адреса Telegram добавлены в правила XKeen (через VPN, группа «"+bal+"») — перезапускаю Xray")
	a.watch.pauseUntil = time.Now().Unix() + 40
	a.watch.mu.Unlock()
	go func() {
		a.restartXray()
		a.applyOnce()
	}()
	writeJSON(w, map[string]any{"ok": true, "configured": true})
}

// xrayAssetEnv — где лежат geosite/geoip: берём у работающего Xray (XKeen задаёт
// XRAY_LOCATION_ASSET), иначе стандартная папка XKeen.
func xrayAssetEnv() []string {
	ents, _ := os.ReadDir("/proc")
	for _, e := range ents {
		pid := e.Name()
		if pid[0] < '0' || pid[0] > '9' {
			continue
		}
		comm, err := os.ReadFile("/proc/" + pid + "/comm")
		if err != nil || strings.TrimSpace(string(comm)) != "xray" {
			continue
		}
		env, err := os.ReadFile("/proc/" + pid + "/environ")
		if err != nil {
			continue
		}
		var out []string
		for _, kv := range strings.Split(string(env), "\x00") {
			if strings.HasPrefix(kv, "XRAY_LOCATION_ASSET=") {
				out = append(out, kv)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	for _, d := range []string{"/opt/etc/xray/dat", "/opt/etc/xkeen/dat", "/opt/share/xray"} {
		if _, err := os.Stat(filepath.Join(d, "geosite_zkeen.dat")); err == nil {
			return []string{"XRAY_LOCATION_ASSET=" + d}
		}
	}
	for _, d := range []string{"/opt/etc/xray/dat", "/opt/etc/xkeen/dat"} {
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			return []string{"XRAY_LOCATION_ASSET=" + d}
		}
	}
	return nil
}

// /api/xray-test — проверить конфиг Xray, не запуская его.
func (a *app) handleXrayTest(w http.ResponseWriter, r *http.Request) {
	if err := xrayTest(*flagConfDir); err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}
