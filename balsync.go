package main

// Синхронизация балансировщика с списком сторожа: состав группы (selector),
// observatory.subjectSelector и fallbackTag в 05_routing.json переписываются
// точечной правкой текста (комментарии и форматирование файла сохраняются).
// Xray подхватывает изменения только после перезапуска.

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// findRoutingFile — файл конфига, где описаны balancers.
func findRoutingFile(dir string) (string, error) {
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	sort.Strings(files)
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		if strings.Contains(string(stripJSONComments(b)), `"balancers"`) {
			return f, nil
		}
	}
	return "", errors.New("не найден файл с routing.balancers")
}

// skipString: s[i]=='"' → индекс после закрывающей кавычки.
func skipString(s string, i int) int {
	for j := i + 1; j < len(s); j++ {
		if s[j] == '\\' {
			j++
			continue
		}
		if s[j] == '"' {
			return j + 1
		}
	}
	return len(s)
}

// skipComment: если на позиции i начинается комментарий — индекс после него, иначе i.
func skipComment(s string, i int) int {
	if s[i] == '#' || (s[i] == '/' && i+1 < len(s) && s[i+1] == '/') {
		for i < len(s) && s[i] != '\n' {
			i++
		}
		return i
	}
	if s[i] == '/' && i+1 < len(s) && s[i+1] == '*' {
		if k := strings.Index(s[i+2:], "*/"); k >= 0 {
			return i + 2 + k + 2
		}
		return len(s)
	}
	return i
}

// matchClose: s[open] — '[' или '{'; вернуть индекс парной скобки.
func matchClose(s string, open int) int {
	depth := 0
	for i := open; i < len(s); {
		c := s[i]
		if c == '"' {
			i = skipString(s, i)
			continue
		}
		if j := skipComment(s, i); j != i {
			i = j
			continue
		}
		switch c {
		case '[', '{':
			depth++
		case ']', '}':
			depth--
			if depth == 0 {
				return i
			}
		}
		i++
	}
	return -1
}

// findKey ищет "key": вне строк/комментариев в s[from:to], возвращает индекс после ':'.
func findKey(s string, key string, from, to int) int {
	pat := `"` + key + `"`
	for i := from; i < to; {
		c := s[i]
		if c == '"' {
			end := skipString(s, i)
			if s[i:end] == pat {
				j := end
				for j < to && (s[j] == ' ' || s[j] == '\t' || s[j] == '\r' || s[j] == '\n') {
					j++
				}
				if j < to && s[j] == ':' {
					return j + 1
				}
			}
			i = end
			continue
		}
		if j := skipComment(s, i); j != i {
			i = j
			continue
		}
		i++
	}
	return -1
}

func skipWS(s string, i int) int {
	for i < len(s) {
		if j := skipComment(s, i); j != i {
			i = j
			continue
		}
		if s[i] == ' ' || s[i] == '\t' || s[i] == '\r' || s[i] == '\n' {
			i++
			continue
		}
		break
	}
	return i
}

func lineIndent(s string, pos int) string {
	ls := strings.LastIndex(s[:pos], "\n") + 1
	j := ls
	for j < len(s) && (s[j] == ' ' || s[j] == '\t') {
		j++
	}
	return s[ls:j]
}

func jsonStr(v string) string {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.Encode(v)
	return strings.TrimSpace(b.String())
}

// replaceArray заменяет значение-массив по ключу внутри s[from:to].
func replaceArray(s, key string, from, to int, items []string) (string, bool) {
	k := findKey(s, key, from, to)
	if k < 0 {
		return s, false
	}
	open := skipWS(s, k)
	if open >= len(s) || s[open] != '[' {
		return s, false
	}
	cl := matchClose(s, open)
	if cl < 0 {
		return s, false
	}
	ind := lineIndent(s, open)
	var b strings.Builder
	b.WriteString("[\n")
	for i, it := range items {
		b.WriteString(ind + "  " + jsonStr(it))
		if i < len(items)-1 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString(ind + "]")
	return s[:open] + b.String() + s[cl+1:], true
}

// replaceString заменяет строковое значение по ключу; если ключа нет — false.
func replaceString(s, key string, from, to int, val string) (string, bool) {
	k := findKey(s, key, from, to)
	if k < 0 {
		return s, false
	}
	q := skipWS(s, k)
	if q >= len(s) || s[q] != '"' {
		return s, false
	}
	end := skipString(s, q)
	return s[:q] + jsonStr(val) + s[end:], true
}

// balancerObject: границы объекта балансировщика с данным тегом.
func balancerObject(s, tag string) (int, int, error) {
	bk := findKey(s, "balancers", 0, len(s))
	if bk < 0 {
		return 0, 0, errors.New("нет routing.balancers")
	}
	arr := skipWS(s, bk)
	if arr >= len(s) || s[arr] != '[' {
		return 0, 0, errors.New("balancers — не массив")
	}
	arrEnd := matchClose(s, arr)
	for i := arr + 1; i < arrEnd; {
		i = skipWS(s, i)
		if i >= arrEnd {
			break
		}
		if s[i] == '{' {
			end := matchClose(s, i)
			if end < 0 {
				break
			}
			if tk := findKey(s, "tag", i+1, end); tk >= 0 {
				q := skipWS(s, tk)
				if q < end && s[q] == '"' {
					var v string
					if json.Unmarshal([]byte(s[q:skipString(s, q)]), &v) == nil && v == tag {
						return i, end, nil
					}
				}
			}
			i = end + 1
			continue
		}
		i++
	}
	return 0, 0, fmt.Errorf("балансировщик %q не найден", tag)
}

type syncResult struct {
	File    string   `json:"file"`
	Changed bool     `json:"changed"`
	Members []string `json:"members"`
}

// syncBalancer переписывает группу tag так, чтобы её состав совпадал со списком.
func syncBalancer(dir, tag string, list []string) (*syncResult, error) {
	if len(list) == 0 {
		return nil, errors.New("список пуст — балансировщик не трогаю")
	}
	file, err := findRoutingFile(dir)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	s := string(raw)
	orig := s

	start, end, err := balancerObject(s, tag)
	if err != nil {
		return nil, err
	}
	var ok bool
	if s, ok = replaceArray(s, "selector", start+1, end, list); !ok {
		return nil, errors.New("у балансировщика нет selector")
	}
	start, end, _ = balancerObject(s, tag)
	if ns, ok := replaceString(s, "fallbackTag", start+1, end, list[0]); ok {
		s = ns
	}

	// observatory / burstObservatory: subjectSelector = объединение selector всех балансировщиков
	var doc struct {
		Routing struct {
			Balancers []struct {
				Tag      string   `json:"tag"`
				Selector []string `json:"selector"`
			} `json:"balancers"`
		} `json:"routing"`
	}
	if err := json.Unmarshal(stripJSONComments([]byte(s)), &doc); err != nil {
		return nil, fmt.Errorf("после правки файл не разбирается (%v) — ничего не записано", err)
	}
	seen := map[string]bool{}
	var union []string
	for _, b := range doc.Routing.Balancers {
		for _, x := range b.Selector {
			if !seen[x] {
				seen[x] = true
				union = append(union, x)
			}
		}
	}
	for _, obsKey := range []string{"observatory", "burstObservatory"} {
		ok := findKey(s, obsKey, 0, len(s))
		if ok < 0 {
			continue
		}
		o := skipWS(s, ok)
		if o < len(s) && s[o] == '{' {
			oe := matchClose(s, o)
			if ns, done := replaceArray(s, "subjectSelector", o+1, oe, union); done {
				s = ns
			}
		}
	}
	var check any
	if err := json.Unmarshal(stripJSONComments([]byte(s)), &check); err != nil {
		return nil, fmt.Errorf("после правки файл не разбирается (%v) — ничего не записано", err)
	}
	res := &syncResult{File: file, Members: list}
	if s == orig {
		return res, nil
	}
	// резервная копия — вне папки конфигов, чтобы Xray/XKeen её не подхватили
	bak := filepath.Join(filepath.Dir(*flagState), "xray-selector-"+filepath.Base(file)+".bak")
	if _, err := os.Stat(bak); err != nil { // одна резервная копия — исходная, до первой правки
		os.WriteFile(bak, raw, 0644)
	}
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, []byte(s), 0644); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, file); err != nil {
		return nil, err
	}
	res.Changed = true
	return res, nil
}

// xrayStartTime — когда запущен основной Xray (не наш тестовый).
func (a *app) xrayStartTime() time.Time {
	ents, _ := os.ReadDir("/proc")
	var best time.Time
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || isProbeProc(pid) || pid == os.Getpid() {
			continue
		}
		comm, err := os.ReadFile("/proc/" + e.Name() + "/comm")
		if err != nil || strings.TrimSpace(string(comm)) != "xray" {
			continue
		}
		st, err := os.Stat("/proc/" + e.Name())
		if err == nil && (best.IsZero() || st.ModTime().Before(best)) {
			best = st.ModTime()
		}
	}
	return best
}

// balancerSyncState — для панели: совпадает ли группа в конфиге со списком и применена ли.
func (a *app) balancerSyncState(c *xrayConf) map[string]any {
	out := map[string]any{}
	file, err := findRoutingFile(*flagConfDir)
	if err != nil {
		return out
	}
	st, err := os.Stat(file)
	if err != nil {
		return out
	}
	xs := a.xrayStartTime()
	pending := !xs.IsZero() && st.ModTime().After(xs.Add(2*time.Second))
	w := a.watch
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, g := range c.groups {
		wg := w.groups[g.Tag]
		if wg == nil || len(wg.Servers) == 0 {
			continue
		}
		want := wg.syncList()
		same := len(g.Selector) == len(want)
		if same {
			for i := range g.Selector {
				if g.Selector[i] != want[i] {
					same = false
				}
			}
		}
		out[g.Tag] = map[string]any{"inSync": same, "pending": pending && same, "file": filepath.Base(file)}
	}
	return out
}

var restartMu sync.Mutex

// restartXray — одновременно идёт только один перезапуск: повторное нажатие,
// пока первый не закончился, пропускается (раньше два перезапуска подряд
// оставляли Xray выключенным). После перезапуска проверяем, что Xray поднялся.
func (a *app) restartXray() error {
	if !restartMu.TryLock() {
		return nil
	}
	defer restartMu.Unlock()
	return a.doRestart()
}

// restartXrayWait — перезапуск, который обязательно выполнится (ждёт окончания текущего).
func (a *app) restartXrayWait() error {
	restartMu.Lock()
	defer restartMu.Unlock()
	return a.doRestart()
}

// 0.9: время последнего перезапуска из панели — сторож Xray в это окно не вмешивается
var lastPanelRestart atomic.Int64

func (a *app) doRestart() error {
	lastPanelRestart.Store(time.Now().Unix())
	defer func() { lastPanelRestart.Store(time.Now().Unix()) }()
	if demoMode {
		return nil // учебная копия: настоящий XKeen не перезапускаем
	}
	err := a.xkeenCmd("-restart")
	for i := 0; i < 10 && a.mainXrayPid() == ""; i++ {
		time.Sleep(2 * time.Second)
	}
	if a.mainXrayPid() == "" { // не поднялся — ещё одна попытка
		log.Printf("Xray не поднялся после перезапуска — запускаю ещё раз")
		a.xkeenCmd("-restart")
		for i := 0; i < 10 && a.mainXrayPid() == ""; i++ {
			time.Sleep(2 * time.Second)
		}
		if a.mainXrayPid() == "" && err == nil {
			err = errors.New("Xray не запустился")
			if terr := xrayTest(*flagConfDir); terr != nil {
				err = errors.New("Xray не запустился — ошибка в конфиге: " + terr.Error())
			}
		}
	}
	return err
}

// xrayWatchdog — если основной Xray не работает дольше минуты (и его никто не
// перезапускает), запускаем его сам, не чаще раза в 5 минут.
func (a *app) xrayWatchdog() {
	time.Sleep(90 * time.Second)
	down, lastTry := 0, time.Time{}
	for range time.Tick(20 * time.Second) {
		if a.mainXrayPid() != "" || time.Now().Unix()-lastPanelRestart.Load() < 150 {
			down = 0 // работает — или панель сама только что перезапускала XKeen (применение правил, кнопка)
			continue
		}
		down++
		if down < 3 || time.Since(lastTry) < 5*time.Minute || !restartMu.TryLock() {
			continue
		}
		lastTry = time.Now()
		a.watch.mu.Lock()
		a.watch.addEvent("", "warn", "основной Xray не работает больше минуты — перезапускаю XKeen")
		a.watch.mu.Unlock()
		kerr := a.xkeenCmd("-restart")
		for i := 0; i < 10 && a.mainXrayPid() == ""; i++ {
			time.Sleep(2 * time.Second)
		}
		ok := a.mainXrayPid() != ""
		restartMu.Unlock()
		terr := error(nil)
		if !ok {
			terr = xrayTest(*flagConfDir)
			if terr != nil && a.selfHeal("Xray не запускается из-за ошибки в конфиге") {
				continue
			}
		}
		a.watch.mu.Lock()
		if ok {
			a.watch.addEvent("", "recover", "Xray снова запущен")
		} else {
			msg := "Xray не запускается"
			if terr != nil {
				msg += " — ошибка в конфиге: " + terr.Error()
			} else if kerr != nil {
				msg += " — XKeen ответил: " + kerr.Error()
			} else {
				msg += " (конфиг в порядке; повторю через 5 минут)"
			}
			a.watch.addEvent("", "warn", msg)
		}
		a.watch.mu.Unlock()
		if ok {
			for _, d := range []time.Duration{2, 4, 8} {
				time.Sleep(d * time.Second)
				a.applyOnce()
			}
		}
	}
}

func (a *app) xkeenCmd(arg string) error {
	if demoMode {
		log.Printf("учебный режим: xkeen %s пропущен", arg)
		return nil
	}
	bin := "xkeen"
	for _, p := range []string{"/opt/sbin/xkeen", "/opt/bin/xkeen", "/opt/usr/sbin/xkeen"} {
		if _, err := os.Stat(p); err == nil {
			bin = p
			break
		}
	}
	cmd := exec.Command(bin, arg)
	cmd.Env = append(os.Environ(), "PATH=/opt/sbin:/opt/bin:/opt/usr/sbin:/opt/usr/bin:/usr/sbin:/usr/bin:/sbin:/bin")
	out, err := cmd.CombinedOutput()
	// в журнал программы — последние строки ответа XKeen (без сотни предупреждений Xray)
	var keep []string
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if !strings.Contains(l, "[Warning]") && !strings.Contains(l, "[Info]") {
			keep = append(keep, strings.TrimSpace(l))
		}
	}
	if len(keep) > 6 {
		keep = keep[len(keep)-6:]
	}
	log.Printf("xkeen %s: %s", arg, strings.Join(keep, " | "))
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.Join(keep, " | "))
	}
	return nil
}
