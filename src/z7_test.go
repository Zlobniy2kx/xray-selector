package main

// Подписка на свежем XKeen: в /opt/etc/xray/configs лежат заготовки, где
// 04_outbounds.json — один комментарий, а массива outbounds нет ни в одном файле.
// Раньше planSub падал с «не нашёл файл с outbounds», и подписка не применялась.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testSubObs() []map[string]any {
	return []map[string]any{{
		"tag":      "🇩🇪 DE-1",
		"protocol": "vless",
		"settings": map[string]any{"vnext": []any{map[string]any{"address": "1.1.1.1", "port": 443}}},
	}}
}

type testOutbound struct {
	Tag      string `json:"tag"`
	Protocol string `json:"protocol"`
}

func planOutbounds(t *testing.T, content string) []testOutbound {
	t.Helper()
	var doc struct {
		Outbounds []testOutbound `json:"outbounds"`
	}
	if err := json.Unmarshal([]byte(content), &doc); err != nil {
		t.Fatalf("результат не разбирается: %v\n%s", err, content)
	}
	return doc.Outbounds
}

func hasOutbound(list []testOutbound, tag string) bool {
	for _, o := range list {
		if o.Tag == tag {
			return true
		}
	}
	return false
}

// Заготовка XKeen с одним комментарием — серверы из подписки должны лечь в неё,
// а direct/block появиться сами (на них ссылаются правила панели).
func TestPlanSubEmptyTemplate(t *testing.T) {
	dir := t.TempDir()
	*flagConfDir = dir
	*flagState = filepath.Join(dir, "st.json")
	os.WriteFile(filepath.Join(dir, "04_outbounds.json"), []byte("{\n// Создайте файл по ссылке https://example.com\n}\n"), 0644)

	plan, err := planSub(&subResult{Outbounds: testSubObs()})
	if err != nil {
		t.Fatalf("planSub: %v", err)
	}
	if plan.File != "04_outbounds.json" {
		t.Fatalf("файл: %q, ждали 04_outbounds.json", plan.File)
	}
	obs := planOutbounds(t, plan.content)
	if len(obs) != 3 {
		t.Fatalf("outbounds: %d, ждали 3 (сервер + direct + block): %s", len(obs), plan.content)
	}
	for _, tag := range []string{"🇩🇪 DE-1", "direct", "block"} {
		if !hasOutbound(obs, tag) {
			t.Fatalf("нет outbound %q: %s", tag, plan.content)
		}
	}
}

// Совсем нет 04_outbounds.json (папка конфигов есть) — файл должен создаться.
func TestPlanSubNoOutboundsFile(t *testing.T) {
	dir := t.TempDir()
	*flagConfDir = dir
	*flagState = filepath.Join(dir, "st.json")

	plan, err := planSub(&subResult{Outbounds: testSubObs()})
	if err != nil {
		t.Fatalf("planSub без 04_outbounds.json: %v", err)
	}
	if plan.File != "04_outbounds.json" {
		t.Fatalf("файл: %q, ждали 04_outbounds.json", plan.File)
	}
	obs := planOutbounds(t, plan.content)
	if !hasOutbound(obs, "🇩🇪 DE-1") || !hasOutbound(obs, "direct") || !hasOutbound(obs, "block") {
		t.Fatalf("не хватает outbound'ов: %s", plan.content)
	}
}

// Если direct/block уже есть — дублей быть не должно.
func TestPlanSubKeepsExistingServiceOutbounds(t *testing.T) {
	dir := t.TempDir()
	*flagConfDir = dir
	*flagState = filepath.Join(dir, "st.json")
	old := `{"outbounds":[{"tag":"direct","protocol":"freedom","settings":{}},{"tag":"block","protocol":"blackhole"}]}`
	os.WriteFile(filepath.Join(dir, "04_outbounds.json"), []byte(old), 0644)

	plan, err := planSub(&subResult{Outbounds: testSubObs()})
	if err != nil {
		t.Fatalf("planSub: %v", err)
	}
	obs := planOutbounds(t, plan.content)
	if len(obs) != 3 {
		t.Fatalf("outbounds: %d, ждали 3 без дублей: %s", len(obs), plan.content)
	}
	// заглушка не должна остаться в файле
	if strings.Contains(plan.content, "пример") {
		t.Fatalf("в файл попал старый комментарий: %s", plan.content)
	}
	// сервер идёт первым — Xray берёт первый outbound как основной
	if obs[0].Tag != "🇩🇪 DE-1" {
		t.Fatalf("первый outbound: %q, ждали сервер", obs[0].Tag)
	}
}
