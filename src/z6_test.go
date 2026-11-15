package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoveTelegramRoute(t *testing.T) {
	dir := t.TempDir()
	st := filepath.Join(dir, "state.json")
	flagState = &st
	cfg := `{
  "routing": {
    "balancers": [{"tag": "vless", "selector": ["a"]}],
    "rules": [
      // xray-selector: адреса Telegram — через VPN
      { "type": "field", "ip": ["91.105.192.0/23","91.108.4.0/22","91.108.8.0/22","91.108.12.0/22","91.108.16.0/22","91.108.20.0/22","91.108.56.0/22","95.161.64.0/20","149.154.160.0/20","185.76.151.0/24"], "balancerTag": "vless" },
      { "type": "field", "domain": ["example.com"], "outboundTag": "direct" }
    ]
  }
}`
	f := filepath.Join(dir, "05_routing.json")
	os.WriteFile(f, []byte(cfg), 0644)
	if err := removeTelegramRoute(dir); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(f)
	s := string(b)
	if strings.Contains(s, tgRuleMark) || strings.Contains(s, "адреса Telegram") || !strings.Contains(s, "example.com") {
		t.Fatalf("wrong result:\n%s", s)
	}
	if tgRouteConfigured(dir) {
		t.Fatal("still configured")
	}
}

func TestRepairAPIDeps(t *testing.T) {
	dir := t.TempDir()
	st := filepath.Join(dir, "state.json")
	flagState = &st
	os.WriteFile(filepath.Join(dir, "06_policy.json"), []byte(`{"policy":{"system":{"statsOutboundUplink":true,"statsOutboundDownlink":true}}}`), 0644)
	os.WriteFile(filepath.Join(dir, "07_api.json"), []byte(`{"api":{"tag":"api","services": ["RoutingService", "ObservatoryService", "StatsService"]}}`), 0644)
	r := repairAPIDeps(dir)
	b, _ := os.ReadFile(filepath.Join(dir, "07_api.json"))
	if strings.Contains(string(b), "ObservatoryService") || !strings.Contains(string(b), "StatsService") {
		t.Fatalf("api not fixed: %s", b)
	}
	if !hasTopKey(dir, "stats") {
		t.Fatalf("stats not added (%s)", r)
	}
	if repairAPIDeps(dir) != "" {
		t.Fatal("second run should do nothing")
	}
}
