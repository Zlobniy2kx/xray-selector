package main

// Включение статистики трафика Xray (байты по каждому серверу):
//   • в api.services добавляется StatsService;
//   • добавляется "stats": {};
//   • в policy.system — statsOutboundUplink/Downlink: true.
// Правка точечная, с резервной копией исходных файлов в /opt/etc.

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type cfgFile struct {
	path string
	raw  string
	doc  map[string]json.RawMessage
}

func readCfgFiles(dir string) []cfgFile {
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	sort.Strings(files)
	var out []cfgFile
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var doc map[string]json.RawMessage
		if json.Unmarshal(stripJSONComments(b), &doc) != nil {
			continue
		}
		out = append(out, cfgFile{f, string(b), doc})
	}
	return out
}

func xrayStatsConfigured(dir string) bool {
	hasStats, hasSvc, hasPol := false, false, false
	for _, f := range readCfgFiles(dir) {
		if _, ok := f.doc["stats"]; ok {
			hasStats = true
		}
		if raw, ok := f.doc["api"]; ok {
			var api struct {
				Services []string `json:"services"`
			}
			json.Unmarshal(raw, &api)
			hasSvc = false
			for _, s := range api.Services {
				if strings.EqualFold(s, "StatsService") {
					hasSvc = true
				}
			}
		}
		if raw, ok := f.doc["policy"]; ok {
			var p struct {
				System map[string]bool `json:"system"`
			}
			json.Unmarshal(raw, &p)
			hasPol = p.System["statsOutboundUplink"] && p.System["statsOutboundDownlink"]
		}
	}
	return hasStats && hasSvc && hasPol
}

func backupOnce(path, raw string) {
	bak := filepath.Join(filepath.Dir(*flagState), "xray-selector-"+filepath.Base(path)+".bak")
	if _, err := os.Stat(bak); err != nil {
		os.WriteFile(bak, []byte(raw), 0644)
	}
}

func writeChecked(path, s string) error {
	var check any
	if err := json.Unmarshal(stripJSONComments([]byte(s)), &check); err != nil {
		return errors.New(filepath.Base(path) + ": после правки не разбирается — ничего не записано")
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(s), 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// insertAfterBrace вставляет текст сразу после открывающей скобки объекта по ключу key.
func insertIntoObject(s, key, text string) (string, bool) {
	k := findKey(s, key, 0, len(s))
	if k < 0 {
		return s, false
	}
	o := skipWS(s, k)
	if o >= len(s) || s[o] != '{' {
		return s, false
	}
	cl := matchClose(s, o)
	inner := strings.TrimSpace(s[o+1 : cl])
	ind := lineIndent(s, o) + "  "
	sep := ","
	if inner == "" {
		sep = ""
	}
	return s[:o+1] + "\n" + ind + text + sep + s[o+1:], true
}

func enableXrayStats(dir string) error {
	files := readCfgFiles(dir)
	if len(files) == 0 {
		return errors.New("конфиги Xray не найдены")
	}
	sysJSON := `"system": {"statsOutboundUplink": true, "statsOutboundDownlink": true}`
	statsDone, apiDone, polDone := false, false, false
	for _, f := range files {
		s := f.raw
		changed := false
		if _, ok := f.doc["stats"]; ok {
			statsDone = true
		}
		if raw, ok := f.doc["api"]; ok {
			var api struct {
				Services []string `json:"services"`
			}
			json.Unmarshal(raw, &api)
			has := false
			for _, x := range api.Services {
				if strings.EqualFold(x, "StatsService") {
					has = true
				}
			}
			if !has {
				a := findKey(s, "api", 0, len(s))
				o := skipWS(s, a)
				if o < len(s) && s[o] == '{' {
					ns, ok := replaceArray(s, "services", o+1, matchClose(s, o), append(api.Services, "StatsService"))
					if !ok {
						return errors.New(filepath.Base(f.path) + ": не нашёл api.services")
					}
					s, changed = ns, true
				}
			}
			apiDone = true
			if !statsDone { // "stats" — рядом с api, в начало файла
				if ns, ok := insertTopLevel(s, `"stats": {}`); ok {
					s, changed, statsDone = ns, true, true
				}
			}
		}
		if raw, ok := f.doc["policy"]; ok {
			var p struct {
				System map[string]any `json:"system"`
			}
			json.Unmarshal(raw, &p)
			if p.System == nil {
				ns, ok := insertIntoObject(s, "policy", sysJSON)
				if !ok {
					return errors.New(filepath.Base(f.path) + ": не нашёл policy")
				}
				s, changed = ns, true
			} else if p.System["statsOutboundUplink"] != true || p.System["statsOutboundDownlink"] != true {
				pk := findKey(s, "policy", 0, len(s))
				po := skipWS(s, pk)
				sk := findKey(s, "system", po+1, matchClose(s, po))
				if sk < 0 {
					return errors.New(filepath.Base(f.path) + ": не разобрал policy.system")
				}
				so := skipWS(s, sk)
				if s[so] != '{' {
					return errors.New(filepath.Base(f.path) + ": не разобрал policy.system")
				}
				p.System["statsOutboundUplink"], p.System["statsOutboundDownlink"] = true, true
				b, _ := json.Marshal(p.System)
				s, changed = s[:so]+string(b)+s[matchClose(s, so)+1:], true
			}
			polDone = true
		}
		if changed {
			backupOnce(f.path, f.raw)
			if err := writeChecked(f.path, s); err != nil {
				return err
			}
		}
	}
	if !apiDone {
		return errors.New("в конфигах нет секции api — сначала включите API (07_api.json)")
	}
	if !statsDone || !polDone { // чего не хватило — отдельным файлом
		extra := map[string]any{}
		if !statsDone {
			extra["stats"] = map[string]any{}
		}
		if !polDone {
			extra["policy"] = map[string]any{"system": map[string]bool{"statsOutboundUplink": true, "statsOutboundDownlink": true}}
		}
		b, _ := json.MarshalIndent(extra, "", "  ")
		return writeChecked(filepath.Join(dir, "08_stats.json"), string(b))
	}
	return nil
}

// insertTopLevel — вставить пару в корневой объект файла.
func insertTopLevel(s, text string) (string, bool) {
	o := skipWS(s, 0)
	if o >= len(s) || s[o] != '{' {
		return s, false
	}
	return s[:o+1] + "\n  " + text + "," + s[o+1:], true
}

func (a *app) handleXrayStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeJSON(w, map[string]any{"configured": xrayStatsConfigured(*flagConfDir)})
		return
	}
	if err := enableXrayStats(*flagConfDir); err != nil {
		w.WriteHeader(400)
		writeJSON(w, map[string]any{"error": err.Error()})
		return
	}
	a.watch.mu.Lock()
	a.watch.addEvent("", "info", "статистика трафика Xray включена в конфиге — применится после перезапуска Xray")
	a.watch.mu.Unlock()
	writeJSON(w, map[string]any{"ok": true, "configured": xrayStatsConfigured(*flagConfDir)})
}
