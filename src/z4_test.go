package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const routingSample = `{
  // мои правила
  "routing": {
    "rules": [
      { "inboundTag": ["api"], "outboundTag": "api" },
      { "ip": ["192.168.0.0/16"], "outboundTag": "direct" },
      { "domain": ["domain:youtube.com", "domain:googlevideo.com", "domain:mysite.org"], "balancerTag": "auto" },
      { "domain": ["domain:gosuslugi.ru"], "outboundTag": "direct" },
      { "domain": ["domain:instagram.com"], "balancerTag": "auto" },
      { "network": "tcp,udp", "outboundTag": "direct" }
    ],
    "balancers": [ { "tag": "auto", "selector": ["🇩🇪"] } ]
  }
}`

func validJSON(t *testing.T, s string) []ruleX {
	var v any
	if err := json.Unmarshal(stripJSONComments([]byte(s)), &v); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, s)
	}
	r, _, err := parseRulesX(s)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestManagedRules(t *testing.T) {
	now := time.Now()
	st := &rulesState{Mode: 1, VPN: []string{"chatgpt", "claude"}, NoVPN: []string{"sber"}, QUICOff: true, Devices: map[string]*devRule{
		"192.168.1.50": {Mode: "novpn"}, "192.168.1.60": {Mode: "1", Temp: "3", Until: now.Unix() + 3600}}}
	s1, err := applyManagedText(routingSample, st, "auto", now)
	if err != nil {
		t.Fatal(err)
	}
	r := validJSON(t, s1)
	if r[0].OutboundTag != "api" || r[1].Xsel != "" || r[2].Xsel != "quic" {
		t.Fatalf("order wrong: %s", s1)
	}
	// идемпотентность
	s2, _ := applyManagedText(s1, st, "auto", now)
	if s2 != s1 {
		t.Fatalf("not idempotent:\n%s\n----\n%s", s1, s2)
	}
	// режим 2: хвост перед «всё остальное» пользователя
	st.Mode = 2
	s3, _ := applyManagedText(s1, st, "auto", now)
	r = validJSON(t, s3)
	last := r[len(r)-1]
	prev := r[len(r)-2]
	if !isCatchAll(last) || last.Xsel != "" || prev.Xsel != "rest" {
		t.Fatalf("mode 2 tail wrong:\n%s", s3)
	}
	// режим 3
	st.Mode = 3
	s4, _ := applyManagedText(s3, st, "auto", now)
	validJSON(t, s4)
	if strings.Contains(s4, `"rest"`) || !strings.Contains(s4, `"_xsel":"all"`) {
		t.Fatalf("mode 3 wrong:\n%s", s4)
	}
	// снять всё
	empty := &rulesState{Mode: 1, Devices: map[string]*devRule{}}
	s5, _ := applyManagedText(s4, empty, "auto", now)
	validJSON(t, s5)
	if strings.Contains(s5, "_xsel") {
		t.Fatalf("managed not removed:\n%s", s5)
	}
}

func TestAbsorb(t *testing.T) {
	dir := t.TempDir()
	*flagConfDir = dir
	*flagState = filepath.Join(dir, "st.json")
	os.WriteFile(filepath.Join(dir, "04_outbounds.json"), []byte(`{"outbounds":[{"tag":"🇩🇪 DE-1","protocol":"vless","settings":{"vnext":[{"address":"1.1.1.1","port":443}]}},{"tag":"direct","protocol":"freedom"},{"tag":"block","protocol":"blackhole"}]}`), 0644)
	os.WriteFile(filepath.Join(dir, "05_routing.json"), []byte(routingSample), 0644)
	a := &app{}
	a.mu.Lock()
	a.mu.Unlock()
	_ = a
	s, p, err := (&app{}).absorbTextWith(routingSample, map[string]bool{"🇩🇪 DE-1": true})
	if err != nil {
		t.Fatal(err)
	}
	r := validJSON(t, s)
	if len(p.VPN) != 2 || len(p.NoVPN) != 1 {
		t.Fatalf("plan %+v", p)
	}
	// instagram-правило опустело — удалено; youtube-правило осталось с mysite.org
	for _, x := range r {
		if x.Xsel == "" && len(x.Domain) == 0 && len(x.IP) == 0 && x.BalancerTag == "auto" {
			t.Fatalf("empty vpn rule left (would be catch-all):\n%s", s)
		}
	}
	if !strings.Contains(s, "mysite.org") || strings.Contains(s, "gosuslugi") {
		t.Fatalf("absorb wrong:\n%s", s)
	}
}

func TestShowManaged(t *testing.T) {
	if os.Getenv("SHOW") == "" {
		t.Skip()
	}
	st := &rulesState{Mode: 2, VPN: []string{"claude"}, NoVPN: []string{"sber"}, QUICOff: true, Devices: map[string]*devRule{"192.168.1.50": {Mode: "novpn"}}}
	s, _ := applyManagedText(routingSample, st, "auto", time.Now())
	t.Log("\n" + s)
}
