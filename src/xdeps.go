package main

// checkXrayDeps — проверка того, что «xray run -test» не ловит, а при запуске Xray
// падает с «core: not all dependencies are resolved»: службы и настройки, которым
// нужна отсутствующая секция конфига.

import (
	"encoding/json"
	"fmt"
	"strings"
)

func checkXrayDeps(dir string) error {
	top := map[string]json.RawMessage{}
	var rulesBal []string
	balTags := map[string]bool{}
	needObs := false
	for _, f := range readCfgFiles(dir) {
		for k, v := range f.doc {
			top[k] = v
		}
		if raw, ok := f.doc["routing"]; ok {
			var r struct {
				Rules []struct {
					BalancerTag string `json:"balancerTag"`
				} `json:"rules"`
				Balancers []struct {
					Tag      string `json:"tag"`
					Strategy struct {
						Type string `json:"type"`
					} `json:"strategy"`
				} `json:"balancers"`
			}
			json.Unmarshal(stripJSONComments(raw), &r)
			for _, x := range r.Rules {
				if x.BalancerTag != "" {
					rulesBal = append(rulesBal, x.BalancerTag)
				}
			}
			for _, b := range r.Balancers {
				balTags[b.Tag] = true
				if t := strings.ToLower(b.Strategy.Type); t == "leastping" || t == "leastload" {
					needObs = true
				}
			}
		}
	}
	_, hasObs := top["observatory"]
	_, hasBurst := top["burstObservatory"]
	_, hasStats := top["stats"]
	if raw, ok := top["api"]; ok {
		var api struct {
			Services []string `json:"services"`
		}
		json.Unmarshal(raw, &api)
		for _, s := range api.Services {
			switch strings.ToLower(s) {
			case "observatoryservice":
				if !hasObs && !hasBurst {
					return fmt.Errorf("в api включена ObservatoryService, но нет секции observatory")
				}
			case "statsservice":
				if !hasStats {
					return fmt.Errorf("в api включена StatsService, но нет секции stats")
				}
			}
		}
	}
	if raw, ok := top["policy"]; ok && !hasStats && strings.Contains(string(raw), `"stats`) && strings.Contains(string(raw), "true") {
		var p struct {
			System map[string]bool            `json:"system"`
			Levels map[string]map[string]any `json:"levels"`
		}
		json.Unmarshal(raw, &p)
		for k, v := range p.System {
			if strings.HasPrefix(k, "stats") && v {
				return fmt.Errorf("в policy включена статистика (%s), но нет секции stats", k)
			}
		}
		for _, lv := range p.Levels {
			for k, v := range lv {
				if b, ok := v.(bool); ok && b && strings.HasPrefix(k, "stats") {
					return fmt.Errorf("в policy включена статистика (%s), но нет секции stats", k)
				}
			}
		}
	}
	for _, t := range rulesBal {
		if !balTags[t] {
			return fmt.Errorf("правило ссылается на группу %q, которой нет в routing.balancers", t)
		}
	}
	if needObs && !hasObs && !hasBurst {
		return fmt.Errorf("группе со стратегией leastPing/leastLoad нужна секция observatory")
	}
	return nil
}
