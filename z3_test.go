package main

import (
	"net/http"
	"testing"
	"time"
)

func TestInformersAndMeta(t *testing.T) {
	mk := func(tag, addr string, port float64) map[string]any {
		return map[string]any{"tag": tag, "protocol": "vless", "settings": map[string]any{"vnext": []any{map[string]any{"address": addr, "port": port}}}}
	}
	obs := []map[string]any{mk("🇩🇪 Germany", "5.5.5.5", 443), mk("⏳ Осталось 12 дней", "0.0.0.0", 1), mk("Подписка до 01.12.2026", "5.5.5.6", 443), mk("🇫🇮 Finland days club", "5.5.5.7", 443)}
	srv, inf := splitInformers(obs)
	if len(srv) != 2 || len(inf) != 2 {
		t.Fatalf("servers=%d informers=%v", len(srv), inf)
	}
	exp, _ := infoFromInformers(inf, time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC))
	if exp == 0 {
		t.Fatal("no expire from informers")
	}
	h := http.Header{}
	h.Set("profile-title", "base64:0JzQvtC5IFZQTg==")
	h.Set("support-url", "https://t.me/support")
	h.Set("profile-update-interval", "24")
	m := parseSubMeta(h)
	if m.Title != "Мой VPN" || m.Support == "" || m.UpdateH != 24 {
		t.Fatalf("meta %+v", m)
	}
	if s := shortSrv("🇫🇷 FR-1 (vless)", "🇵🇱 PL-2"); s != "🇫🇷 Франция" {
		t.Fatal(s)
	}
	if s := shortSrv("🇩🇪 DE-1 (vless)", "🇩🇪 DE-2"); s != "🇩🇪 Германия (DE-1)" {
		t.Fatal(s)
	}
}
