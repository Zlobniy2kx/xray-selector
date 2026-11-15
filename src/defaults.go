package main

// Настройки «по умолчанию», которые панель применяет сама, без вопросов пользователю:
//  1. Статистика трафика Xray (гигабайты по серверам) — включается в конфиге;
//     вступает в силу при следующем перезапуске Xray, отдельно Xray не перезапускаем.
//  2. Старое отдельное правило «адреса Telegram — через VPN» (из версий до 0.9.3)
//     убирается: Telegram теперь управляется только в «Правилах» (у сервиса Telegram
//     в каталоге есть и домены, и IP-адреса), иначе выбор «без VPN» не работал бы.
// Каждое изменение проверяется самим Xray; если он не принял — всё возвращается как было.

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (a *app) defaultsLoop() {
	if demoMode {
		return
	}
	dir := *flagConfDir
	// сначала — починка: службы API без нужных секций не дают Xray запуститься
	if fixed := repairAPIDeps(dir); fixed != "" {
		a.watch.mu.Lock()
		a.watch.addEvent("", "warn", "конфиг Xray исправлен: "+fixed+" — перезапускаю Xray")
		a.watch.mu.Unlock()
		a.restartXray()
	}
	time.Sleep(90 * time.Second)
	if !xrayStatsConfigured(dir) {
		if err := withRollback(dir, func() error {
			if err := enableXrayStats(dir); err != nil {
				return err
			}
			if !hasTopKey(dir, "stats") { // без секции stats Xray не запустится
				return writeChecked(filepath.Join(dir, "08_stats.json"), "{ \"stats\": {} }\n")
			}
			return nil
		}); err == nil {
			a.watch.mu.Lock()
			a.watch.addEvent("", "info", "включён подсчёт трафика по серверам — заработает после перезапуска Xray")
			a.watch.mu.Unlock()
		}
	}
	if tgRouteConfigured(dir) {
		if err := withRollback(dir, func() error { return removeTelegramRoute(dir) }); err == nil {
			a.watch.mu.Lock()
			a.watch.addEvent("", "info", "отдельное правило для Telegram убрано — Telegram настраивается в «Правилах»")
			a.watch.mu.Unlock()
		}
	}
}

// withRollback — запомнить все конфиги, выполнить правку, проверить Xray; при ошибке вернуть как было.
func withRollback(dir string, fn func() error) error {
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	saved := map[string][]byte{}
	for _, f := range files {
		if b, err := os.ReadFile(f); err == nil {
			saved[f] = b
		}
	}
	restore := func() {
		now, _ := filepath.Glob(filepath.Join(dir, "*.json"))
		for _, f := range now {
			if _, ok := saved[f]; !ok {
				os.Remove(f) // файл появился во время правки (например, 08_stats.json)
			}
		}
		for f, b := range saved {
			os.WriteFile(f, b, 0644)
		}
	}
	if err := fn(); err != nil {
		restore()
		return err
	}
	if err := checkXrayDeps(dir); err != nil {
		restore()
		return err
	}
	if err := xrayTest(dir); err != nil {
		restore()
		return err
	}
	return nil
}

// removeTelegramRoute — убрать правило, которое раньше добавляла кнопка «Telegram через VPN».
func removeTelegramRoute(dir string) error {
	file, err := findRoutingFile(dir)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	s := string(raw)
	rules, spans, err := parseRulesX(s)
	if err != nil {
		return err
	}
	for i := len(rules) - 1; i >= 0; i-- {
		r := rules[i]
		if len(r.Domain) > 0 || len(r.Source) > 0 || len(r.Port) > 0 || len(r.IP) != len(telegramCIDRs) {
			continue
		}
		has := false
		for _, x := range r.IP {
			if x == tgRuleMark {
				has = true
			}
		}
		if has {
			s = removeSpan(s, spans, i)
		}
	}
	s = strings.Replace(s, "// xray-selector: адреса Telegram — через VPN\n", "", 1)
	// строка комментария могла остаться с отступом — убираем целиком
	lines := strings.Split(s, "\n")
	out := lines[:0]
	for _, l := range lines {
		if strings.TrimSpace(l) == "// xray-selector: адреса Telegram — через VPN" {
			continue
		}
		out = append(out, l)
	}
	s = strings.Join(out, "\n")
	if s == string(raw) {
		return nil
	}
	backupOnce(file, string(raw))
	return writeChecked(file, s)
}

// hasTopKey — есть ли ключ верхнего уровня хотя бы в одном конфиге.
func hasTopKey(dir, key string) bool {
	for _, f := range readCfgFiles(dir) {
		if _, ok := f.doc[key]; ok {
			return true
		}
	}
	return false
}

// repairAPIDeps — убрать причины «not all dependencies are resolved»:
// ObservatoryService без observatory и статистику без секции stats.
func repairAPIDeps(dir string) string {
	var done []string
	if !hasTopKey(dir, "observatory") && !hasTopKey(dir, "burstObservatory") {
		for _, f := range readCfgFiles(dir) {
			if _, ok := f.doc["api"]; !ok || !strings.Contains(f.raw, `"ObservatoryService"`) {
				continue
			}
			ns := f.raw
			for _, pat := range []string{`, "ObservatoryService"`, `,"ObservatoryService"`, `"ObservatoryService", `, `"ObservatoryService",`} {
				ns = strings.Replace(ns, pat, "", 1)
			}
			if ns != f.raw && writeChecked(f.path, ns) == nil {
				backupOnce(f.path, f.raw)
				done = append(done, "убрана ObservatoryService (нет observatory)")
			}
		}
	}
	all := ""
	for _, f := range readCfgFiles(dir) {
		all += f.raw
	}
	if (strings.Contains(all, `"StatsService"`) || strings.Contains(all, "statsOutbound")) && !hasTopKey(dir, "stats") {
		if writeChecked(filepath.Join(dir, "08_stats.json"), "{ \"stats\": {} }\n") == nil {
			done = append(done, "добавлена секция stats")
		}
	}
	return strings.Join(done, ", ")
}
