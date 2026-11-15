package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// типичная простая настройка XKeen: один сервер, правила через outboundTag
const xkOut = `{
  "outbounds": [
    { "tag": "vless-reality", "protocol": "vless", "settings": { "vnext": [ { "address": "1.2.3.4", "port": 443, "users": [ { "id": "x" } ] } ] }, "streamSettings": { "network": "tcp", "security": "reality" } },
    { "tag": "direct", "protocol": "freedom" },
    { "tag": "block", "protocol": "blackhole" }
  ]
}`
const xkRouting = `{
  // правила XKeen
  "routing": {
    "domainStrategy": "IPIfNonMatch",
    "rules": [
      { "inboundTag": ["redirect", "tproxy"], "outboundTag": "block", "type": "field", "network": "udp", "port": "135,137,138,139" },
      { "inboundTag": ["redirect", "tproxy"], "outboundTag": "vless-reality", "type": "field", "domain": ["ext:geosite_v2fly.dat:youtube"] },
      { "inboundTag": ["redirect", "tproxy"], "outboundTag" : "vless-reality", "type": "field", "ip": ["ext:geoip_v2fly.dat:telegram"] },
      { "inboundTag": ["redirect", "tproxy"], "outboundTag": "direct", "type": "field", "network": "tcp,udp" }
    ]
  }
}`

func xkDir(t *testing.T, extra map[string]string) string {
	dir := t.TempDir()
	st := filepath.Join(dir, "state.json")
	flagState = &st
	os.WriteFile(filepath.Join(dir, "04_outbounds.json"), []byte(xkOut), 0644)
	os.WriteFile(filepath.Join(dir, "05_routing.json"), []byte(xkRouting), 0644)
	os.WriteFile(filepath.Join(dir, "06_policy.json"), []byte(`{"policy":{"levels":{"0":{"handshake":4}}}}`), 0644)
	for k, v := range extra {
		os.WriteFile(filepath.Join(dir, k), []byte(v), 0644)
	}
	return dir
}

func applyChanges(t *testing.T, dir string, ch []cfgChange) {
	for _, c := range ch {
		if err := os.WriteFile(filepath.Join(dir, c.Name), []byte(c.Content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if len(readCfgFiles(dir)) != len(mustGlob(dir)) {
		t.Fatal("какой-то файл перестал разбираться как JSON")
	}
}

func mustGlob(dir string) []string { f, _ := filepath.Glob(filepath.Join(dir, "*.json")); return f }

func TestMakeBalancerNoAPI(t *testing.T) {
	dir := xkDir(t, nil)
	if err := checkXrayDeps(dir); err != nil {
		t.Fatalf("исходный конфиг: %v", err)
	}
	ch, err := planBalancer(dir, []string{"vless-reality"})
	if err != nil {
		t.Fatal(err)
	}
	applyChanges(t, dir, ch)
	if err := checkXrayDeps(dir); err != nil {
		t.Fatalf("после создания группы: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "05_routing.json"))
	s := string(b)
	if strings.Contains(s, `"outboundTag": "vless-reality"`) || strings.Contains(s, `"outboundTag" : "vless-reality"`) || strings.Count(s, `"balancerTag": "xsel"`)+strings.Count(s, `"balancerTag" : "xsel"`) != 2 {
		t.Fatalf("правила не переведены:\n%s", s)
	}
	if !strings.Contains(s, `"outboundTag": "direct"`) || !strings.Contains(s, `"outboundTag": "block"`) {
		t.Fatal("direct/block не должны меняться")
	}
	if !hasTopKeyIn(dir, "routing", "balancers") || !hasTopKey(dir, "observatory") || !hasTopKey(dir, "api") {
		t.Fatal("не хватает balancers/observatory/api")
	}
	// панель должна увидеть группу
	if !strings.Contains(s, `"tag": "xsel"`) {
		t.Fatal("нет группы xsel")
	}
	if _, err := planBalancer(dir, []string{"vless-reality"}); err == nil {
		t.Fatal("повторно создавать группу нельзя")
	}
}

func TestMakeBalancerExistingAPI(t *testing.T) {
	dir := xkDir(t, map[string]string{"07_api.json": `{"api":{"tag":"api","listen":"127.0.0.1:10085","services":["RoutingService"]}}`})
	ch, err := planBalancer(dir, []string{"vless-reality"})
	if err != nil {
		t.Fatal(err)
	}
	applyChanges(t, dir, ch)
	if err := checkXrayDeps(dir); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "07_api.json"))
	if !strings.Contains(string(b), "ObservatoryService") || !strings.Contains(string(b), "RoutingService") {
		t.Fatalf("api не дополнен: %s", b)
	}
}

func TestCheckXrayDeps(t *testing.T) {
	bad := map[string]string{
		"obs svc без observatory": `{"api":{"services":["RoutingService","ObservatoryService"]}}`,
		"stats svc без stats":     `{"api":{"services":["StatsService"]}}`,
		"policy stats без stats":  `{"policy":{"system":{"statsOutboundUplink":true}}}`,
		"leastPing без observ":    `{"routing":{"balancers":[{"tag":"b","selector":["v"],"strategy":{"type":"leastPing"}}],"rules":[]}}`,
	}
	for name, cfg := range bad {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "09_x.json"), []byte(cfg), 0644)
		if checkXrayDeps(dir) == nil {
			t.Errorf("%s: ошибка не найдена", name)
		}
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "1.json"), []byte(`{"api":{"services":["StatsService","ObservatoryService"]},"stats":{},"observatory":{"subjectSelector":["v"]},"policy":{"system":{"statsOutboundUplink":true}}}`), 0644)
	if err := checkXrayDeps(dir); err != nil {
		t.Errorf("хороший конфиг отклонён: %v", err)
	}
}

// конфиг с балансировщиком (как у автора) — проверки не должны мешать
func TestCheckXrayDepsBalancerConfig(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "05_routing.json"), []byte(`{"routing":{"balancers":[{"tag":"vless","selector":["a","b"],"strategy":{"type":"leastPing"}}],"rules":[{"type":"field","balancerTag":"vless","domain":["x.com"]}]},"observatory":{"subjectSelector":["a","b"]}}`), 0644)
	os.WriteFile(filepath.Join(dir, "07_api.json"), []byte(`{"api":{"services":["RoutingService","ObservatoryService"]}}`), 0644)
	if err := checkXrayDeps(dir); err != nil {
		t.Fatal(err)
	}
}
