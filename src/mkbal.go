package main

// Создание группы серверов (balancer) для простой настройки XKeen, где правила
// маршрутизации ведут на один сервер через outboundTag. Панель работает с группой,
// поэтому мастер предлагает создать её сам:
//   - routing.balancers: группа "xsel" из всех серверов (leastPing);
//   - правила с outboundTag=<сервер> переводятся на balancerTag="xsel";
//   - observatory: проверка серверов группы (нужна для leastPing и ObservatoryService);
//   - api: RoutingService + ObservatoryService (если api нет — создаётся 07_api.json).
// Применение — через applyConfig: проверка Xray, резервная копия, перезапуск,
// ожидание API; если Xray не поднялся — откат.

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"regexp"
	"strings"
)

const autoBalancerTag = "xsel"

// findRoutingAny — файл с секцией routing (с балансировщиком или без).
func findRoutingAny(dir string) (cfgFile, bool) {
	for _, f := range readCfgFiles(dir) {
		if _, ok := f.doc["routing"]; ok {
			return f, true
		}
	}
	return cfgFile{}, false
}

// planBalancer — изменения конфигов для создания группы. servers — теги серверов.
func planBalancer(dir string, servers []string) ([]cfgChange, error) {
	if len(servers) == 0 {
		return nil, errors.New("в конфиге Xray нет серверов — сначала добавьте серверы (шаг «Серверы»)")
	}
	if hasTopKeyIn(dir, "routing", "balancers") {
		return nil, errors.New("группа серверов уже есть")
	}
	rf, ok := findRoutingAny(dir)
	if !ok {
		return nil, errors.New("в конфигах Xray нет секции routing")
	}
	sel, _ := json.Marshal(servers)
	s := rf.raw
	// 1) правила: outboundTag=<сервер> → balancerTag=xsel
	moved := 0
	for _, t := range servers {
		re := regexp.MustCompile(`"outboundTag"(\s*):(\s*)"` + regexp.QuoteMeta(t) + `"`)
		n := len(re.FindAllStringIndex(s, -1))
		if n > 0 {
			s = re.ReplaceAllString(s, `"balancerTag"$1:$2"`+autoBalancerTag+`"`)
			moved += n
		}
	}
	// 2) balancers внутрь routing
	bal := `"balancers": [ { "tag": "` + autoBalancerTag + `", "selector": ` + string(sel) + `, "strategy": { "type": "leastPing" } } ]`
	ns, ok := insertIntoObject(s, "routing", bal)
	if !ok {
		return nil, errors.New(filepath.Base(rf.path) + ": не удалось добавить группу в routing")
	}
	changes := []cfgChange{{Name: filepath.Base(rf.path), Content: ns}}
	// 3) observatory (если нет ни observatory, ни burstObservatory)
	if !hasTopKey(dir, "observatory") && !hasTopKey(dir, "burstObservatory") {
		obs := map[string]any{"observatory": map[string]any{
			"subjectSelector":   servers,
			"probeUrl":          "https://www.gstatic.com/generate_204",
			"probeInterval":     "1m",
			"enableConcurrency": true,
		}}
		b, _ := json.MarshalIndent(obs, "", "  ")
		changes = append(changes, cfgChange{Name: "09_observatory.json", Content: string(b) + "\n"})
	}
	// 4) api со службами Routing + Observatory
	apiFile := ""
	for _, f := range readCfgFiles(dir) {
		if _, ok := f.doc["api"]; ok {
			apiFile = f.path
			var api struct {
				Services []string `json:"services"`
			}
			json.Unmarshal(f.doc["api"], &api)
			need := []string{}
			has := map[string]bool{}
			for _, x := range api.Services {
				has[strings.ToLower(x)] = true
			}
			for _, x := range []string{"RoutingService", "ObservatoryService"} {
				if !has[strings.ToLower(x)] {
					need = append(need, x)
				}
			}
			if len(need) > 0 {
				raw := f.raw
				for _, c := range changes { // файл api может совпадать с файлом routing
					if c.Name == filepath.Base(f.path) {
						raw = c.Content
					}
				}
				k := findKey(raw, "api", 0, len(raw))
				o := skipWS(raw, k)
				if k < 0 || o >= len(raw) || raw[o] != '{' {
					return nil, errors.New(filepath.Base(f.path) + ": не разобрал секцию api")
				}
				var nr string
				if findKey(raw, "services", o+1, matchClose(raw, o)) >= 0 {
					nr, ok = replaceArray(raw, "services", o+1, matchClose(raw, o), append(api.Services, need...))
				} else {
					sv, _ := json.Marshal(need)
					nr, ok = insertIntoObject(raw, "api", `"services": `+string(sv))
				}
				if !ok {
					return nil, errors.New(filepath.Base(f.path) + ": не удалось дополнить api.services")
				}
				replaced := false
				for i := range changes {
					if changes[i].Name == filepath.Base(f.path) {
						changes[i].Content, replaced = nr, true
					}
				}
				if !replaced {
					changes = append(changes, cfgChange{Name: filepath.Base(f.path), Content: nr})
				}
			}
			break
		}
	}
	if apiFile == "" {
		changes = append(changes, cfgChange{Name: "07_api.json", Content: `{
  "api": {
    "tag": "api",
    "listen": "127.0.0.1:10085",
    "services": ["RoutingService", "ObservatoryService"]
  }
}
`})
	}
	if moved == 0 {
		// правил на сервер нет — группа создаётся, а куда направлять трафик, решат «Правила» панели
		_ = moved
	}
	return changes, nil
}

// hasTopKeyIn — есть ли ключ sub внутри секции top хотя бы в одном конфиге.
func hasTopKeyIn(dir, top, sub string) bool {
	for _, f := range readCfgFiles(dir) {
		raw, ok := f.doc[top]
		if !ok {
			continue
		}
		var m map[string]json.RawMessage
		if json.Unmarshal(raw, &m) == nil {
			if _, ok := m[sub]; ok {
				return true
			}
		}
	}
	return false
}

func (a *app) makeBalancer() (int, error) {
	if demoMode {
		return 0, errors.New("в учебном режиме конфиги XKeen не меняются")
	}
	c := a.reload()
	var tags []string
	for _, s := range c.servers {
		tags = append(tags, s.Tag)
	}
	ch, err := planBalancer(*flagConfDir, tags)
	if err != nil {
		return 0, err
	}
	if err := a.applyConfig("создана группа серверов «"+autoBalancerTag+"» ("+itoa(int64(len(tags)))+" серверов)", ch); err != nil {
		return 0, err
	}
	return len(tags), nil
}
