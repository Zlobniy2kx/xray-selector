package main

// Безопасное применение изменений конфигов Xray:
//   1) копия всех конфигов во временную папку + новые версии файлов;
//   2) проверка самим Xray (xray run -test) — не прошла → ничего не трогаем;
//   3) резервная копия текущих файлов (хранятся последние 20 правок);
//   4) атомарная запись и перезапуск XKeen;
//   5) если Xray не поднялся за 30 с — возврат резервной копии и снова перезапуск.
// Плюс «последний рабочий набор» конфигов для самовосстановления.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

var applyMu sync.Mutex

func backupRoot() string { return filepath.Join(filepath.Dir(*flagState), "xray-selector-backups") }

type cfgChange struct {
	Name    string // имя файла в папке конфигов (05_routing.json)
	Content string // новое содержимое; "" и Delete — удалить файл
	Delete  bool
}

// diffLines — простое сравнение построчно: какие строки ушли, какие появились.
func diffLines(a, b string) (removed, added []string) {
	ca := map[string]int{}
	for _, l := range strings.Split(a, "\n") {
		ca[strings.TrimSpace(l)]++
	}
	cb := map[string]int{}
	for _, l := range strings.Split(b, "\n") {
		cb[strings.TrimSpace(l)]++
	}
	for _, l := range strings.Split(a, "\n") {
		t := strings.TrimSpace(l)
		if t != "" && cb[t] < ca[t] {
			removed = append(removed, t)
			ca[t]--
		}
	}
	ca2 := map[string]int{}
	for _, l := range strings.Split(a, "\n") {
		ca2[strings.TrimSpace(l)]++
	}
	for _, l := range strings.Split(b, "\n") {
		t := strings.TrimSpace(l)
		if t != "" && ca2[t] < cb[t] {
			added = append(added, t)
			cb[t]--
		}
	}
	return
}

// testChanges — проверить набор изменений Xray'ем во временной копии конфигов.
func testChanges(changes []cfgChange) error {
	dir := *flagConfDir
	tmp, err := os.MkdirTemp("", "xsel-test-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		os.WriteFile(filepath.Join(tmp, filepath.Base(f)), b, 0644)
	}
	for _, c := range changes {
		p := filepath.Join(tmp, c.Name)
		if c.Delete {
			os.Remove(p)
		} else if err := os.WriteFile(p, []byte(c.Content), 0644); err != nil {
			return err
		}
	}
	if err := checkXrayDeps(tmp); err != nil {
		return err
	}
	return xrayTest(tmp)
}

// applyConfig — проверить, сохранить копию, записать, перезапустить, убедиться, что Xray жив.
func (a *app) applyConfig(desc string, changes []cfgChange) error {
	if len(changes) == 0 {
		return nil
	}
	applyMu.Lock()
	defer applyMu.Unlock()
	if err := testChanges(changes); err != nil {
		return errors.New("Xray не принял изменения, ничего не записано: " + err.Error())
	}
	dir := *flagConfDir
	// резервная копия всех конфигов
	ts := time.Now().Format("20060102-150405.000")
	bdir := filepath.Join(backupRoot(), ts)
	if err := os.MkdirAll(bdir, 0755); err != nil {
		return err
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	for _, f := range files {
		if b, err := os.ReadFile(f); err == nil {
			os.WriteFile(filepath.Join(bdir, filepath.Base(f)), b, 0644)
		}
	}
	os.WriteFile(filepath.Join(bdir, "_what.txt"), []byte(desc+"\n"), 0644)
	pruneBackups(20)
	// запись
	for _, c := range changes {
		p := filepath.Join(dir, c.Name)
		if c.Delete {
			os.Remove(p)
			continue
		}
		if err := os.WriteFile(p+".tmp", []byte(c.Content), 0644); err != nil {
			return err
		}
		if err := os.Rename(p+".tmp", p); err != nil {
			return err
		}
	}
	a.watch.mu.Lock()
	a.watch.pauseUntil = time.Now().Unix() + 45
	a.watch.addEvent("", "info", desc+" — применяю, перезапускаю Xray (связь пропадёт на 5–10 с)")
	a.watch.mu.Unlock()
	rerr := a.restartXrayWait()
	ok := rerr == nil && (demoMode || a.waitXrayAPI(30*time.Second)) // учебная копия Xray не перезапускает
	if !ok {
		// откат
		for _, c := range changes {
			p := filepath.Join(dir, c.Name)
			if b, err := os.ReadFile(filepath.Join(bdir, c.Name)); err == nil {
				os.WriteFile(p, b, 0644)
			} else {
				os.Remove(p) // файла раньше не было
			}
		}
		a.restartXrayWait()
		a.waitXrayAPI(30 * time.Second)
		go a.reapply()
		a.watch.mu.Lock()
		a.watch.addEvent("", "warn", desc+": после изменения Xray не заработал — вернул как было")
		a.watch.mu.Unlock()
		if rerr != nil {
			return errors.New("Xray не заработал, изменения отменены: " + rerr.Error())
		}
		return errors.New("Xray не заработал после изменения, всё возвращено как было")
	}
	go a.reapply()
	a.watch.mu.Lock()
	a.watch.addEvent("", "info", desc+" — готово")
	a.watch.mu.Unlock()
	a.saveGoodSnapshot()
	return nil
}

func (a *app) reapply() {
	for _, d := range []time.Duration{1, 3, 6} {
		time.Sleep(d * time.Second)
		a.applyOnce()
	}
}

// waitXrayAPI — ждать, пока основной Xray запустится и ответит по API.
func (a *app) waitXrayAPI(d time.Duration) bool {
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		if a.mainXrayPid() != "" {
			if a.apiAlive() {
				return true
			}
		}
		time.Sleep(time.Second)
	}
	return false
}

func (a *app) apiAlive() bool {
	c := a.reload()
	if len(c.groups) == 0 {
		return a.mainXrayPid() != ""
	}
	ctx, cancel := ctxTimeout(3 * time.Second)
	defer cancel()
	_, err := a.api.GetBalancerInfo(ctx, c.groups[0].Tag)
	return err == nil
}

func pruneBackups(keep int) {
	ents, _ := os.ReadDir(backupRoot())
	var names []string
	for _, e := range ents {
		if e.IsDir() && e.Name() != "good" {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for len(names) > keep {
		os.RemoveAll(filepath.Join(backupRoot(), names[0]))
		names = names[1:]
	}
}

type backupInfo struct {
	ID    string   `json:"id"`
	What  string   `json:"what"`
	Files []string `json:"files"`
}

func listBackups() []backupInfo {
	ents, _ := os.ReadDir(backupRoot())
	var out []backupInfo
	for _, e := range ents {
		if !e.IsDir() || e.Name() == "good" {
			continue
		}
		b, _ := os.ReadFile(filepath.Join(backupRoot(), e.Name(), "_what.txt"))
		fs, _ := filepath.Glob(filepath.Join(backupRoot(), e.Name(), "*.json"))
		var names []string
		for _, f := range fs {
			names = append(names, filepath.Base(f))
		}
		out = append(out, backupInfo{e.Name(), strings.TrimSpace(string(b)), names})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out
}

// restoreBackup — вернуть конфиги из резервной копии (тоже через проверку и откат).
func (a *app) restoreBackup(id string) error {
	if strings.Contains(id, "/") || strings.Contains(id, "..") {
		return errors.New("неверная копия")
	}
	return a.restoreFromDir(filepath.Join(backupRoot(), id), "возврат конфигов из копии "+id)
}

func (a *app) restoreFromDir(src, desc string) error {
	fs, _ := filepath.Glob(filepath.Join(src, "*.json"))
	if len(fs) == 0 {
		return errors.New("в копии нет файлов")
	}
	have := map[string]bool{}
	var ch []cfgChange
	for _, f := range fs {
		b, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		have[filepath.Base(f)] = true
		ch = append(ch, cfgChange{Name: filepath.Base(f), Content: string(b)})
	}
	// файлы, которых в копии не было, — убрать
	cur, _ := filepath.Glob(filepath.Join(*flagConfDir, "*.json"))
	for _, f := range cur {
		if !have[filepath.Base(f)] {
			ch = append(ch, cfgChange{Name: filepath.Base(f), Delete: true})
		}
	}
	return a.applyConfig(desc, ch)
}

// ---- самовосстановление ----

// saveGoodSnapshot — запомнить текущие конфиги как «рабочие» (Xray с ними запущен и отвечает).
func (a *app) saveGoodSnapshot() {
	g := filepath.Join(backupRoot(), "good")
	os.MkdirAll(g, 0755)
	old, _ := filepath.Glob(filepath.Join(g, "*.json"))
	for _, f := range old {
		os.Remove(f)
	}
	files, _ := filepath.Glob(filepath.Join(*flagConfDir, "*.json"))
	for _, f := range files {
		if b, err := os.ReadFile(f); err == nil {
			os.WriteFile(filepath.Join(g, filepath.Base(f)), b, 0644)
		}
	}
	os.WriteFile(filepath.Join(g, "_what.txt"), []byte(time.Now().Format("2006-01-02 15:04")+"\n"), 0644)
}

func sameDir(a, b string) bool {
	fa, _ := filepath.Glob(filepath.Join(a, "*.json"))
	fb, _ := filepath.Glob(filepath.Join(b, "*.json"))
	if len(fa) != len(fb) {
		return false
	}
	for _, f := range fa {
		x, _ := os.ReadFile(f)
		y, err := os.ReadFile(filepath.Join(b, filepath.Base(f)))
		if err != nil || string(x) != string(y) {
			return false
		}
	}
	return true
}

// goodSnapshotLoop — раз в 10 минут: если Xray работает и отвечает, а конфиги изменились —
// запомнить их как рабочие.
func (a *app) goodSnapshotLoop() {
	time.Sleep(3 * time.Minute)
	for {
		if !applyMu.TryLock() {
			time.Sleep(time.Minute)
			continue
		}
		if a.mainXrayPid() != "" && a.apiAlive() && !sameDir(*flagConfDir, filepath.Join(backupRoot(), "good")) {
			a.saveGoodSnapshot()
		}
		applyMu.Unlock()
		time.Sleep(10 * time.Minute)
	}
}

// selfHeal — Xray не запускается и конфиг не проходит проверку: вернуть последний рабочий набор.
func (a *app) selfHeal(reason string) bool {
	g := filepath.Join(backupRoot(), "good")
	if fs, _ := filepath.Glob(filepath.Join(g, "*.json")); len(fs) == 0 {
		return false
	}
	if sameDir(*flagConfDir, g) {
		return false
	}
	err := a.restoreFromDir(g, "самовосстановление: "+reason+" — возвращаю последний рабочий набор конфигов")
	return err == nil
}

func fmtErr(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprint(err)
}

func ctxTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}
