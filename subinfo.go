package main

// 2.3, п. 18: срок и трафик подписки.
//   • заголовки: subscription-userinfo, profile-title, support-url, profile-update-interval;
//   • «информеры» — фиктивные серверы, в названии которых провайдер пишет срок/трафик
//     («⏳ Осталось 12 дней», «Трафик: 50 GB», «до 01.12.2026»). Это не серверы:
//     в конфиг Xray они не попадают, но из названий берём срок/трафик, если заголовков нет;
//   • ручное «Оплачено до» — если провайдер ничего не сообщает.

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type subMeta struct {
	Title     string
	Support   string
	WebPage   string
	UpdateH   int
	Informers []string
}

func decodeMaybeB64(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(strings.ToLower(s), "base64:") {
		if b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s[7:])); err == nil {
			return string(b)
		}
		if b, err := base64.RawStdEncoding.DecodeString(strings.TrimSpace(s[7:])); err == nil {
			return string(b)
		}
	}
	return s
}

func parseSubMeta(h http.Header) subMeta {
	m := subMeta{Title: decodeMaybeB64(h.Get("profile-title")), Support: strings.TrimSpace(h.Get("support-url")),
		WebPage: strings.TrimSpace(h.Get("profile-web-page-url"))}
	if n, err := strconv.Atoi(strings.TrimSpace(h.Get("profile-update-interval"))); err == nil && n > 0 && n <= 24*30 {
		m.UpdateH = n
	}
	if !strings.HasPrefix(m.Support, "http") && !strings.HasPrefix(m.Support, "tg:") {
		m.Support = ""
	}
	if !strings.HasPrefix(m.WebPage, "http") {
		m.WebPage = ""
	}
	return m
}

var (
	reInformerWord = regexp.MustCompile(`(?i)(осталось|остаток|истека|истечёт|истечет|действ|оплачен|подписк|трафик|traffic|expire|remaining|left|дн(ей|я|\.)|days?\b|\bГБ\b|\bGB\b|до \d{1,2}\.\d{1,2})`)
	reDate         = regexp.MustCompile(`(\d{1,2})[./](\d{1,2})[./](\d{2,4})|(\d{4})-(\d{2})-(\d{2})`)
	reDaysLeft     = regexp.MustCompile(`(?i)(\d{1,4})\s*(дн|день|дня|days?|d\b)`)
	reGB           = regexp.MustCompile(`(?i)(\d+(?:[.,]\d+)?)\s*(ГБ|GB|ТБ|TB|МБ|MB)`)
)

// isInformer — сервер-«информер»: адрес-заглушка или название про срок/трафик.
func isInformer(ob map[string]any) bool {
	tag, _ := ob["tag"].(string)
	addr, port := obAddr(ob)
	fake := addr == "" || addr == "0.0.0.0" || addr == "127.0.0.1" || addr == "localhost" || addr == "1.1.1.1" || addr == "8.8.8.8" || port <= 1
	if fake && tag != "" {
		return true
	}
	return reInformerWord.MatchString(tag) && (fake || reDate.MatchString(tag) || reDaysLeft.MatchString(tag))
}

func obAddr(ob map[string]any) (string, int) {
	st, _ := ob["settings"].(map[string]any)
	for _, k := range []string{"vnext", "servers"} {
		if arr, ok := st[k].([]any); ok && len(arr) > 0 {
			if m, ok := arr[0].(map[string]any); ok {
				a, _ := m["address"].(string)
				p, _ := m["port"].(float64)
				if p == 0 {
					if pi, ok := m["port"].(int); ok {
						p = float64(pi)
					}
				}
				return a, int(p)
			}
		}
	}
	if a, ok := st["address"].(string); ok {
		p, _ := st["port"].(float64)
		return a, int(p)
	}
	return "?", 443 // неизвестный формат — не считаем заглушкой
}

// splitInformers — убрать информеры из списка серверов.
func splitInformers(obs []map[string]any) (servers []map[string]any, informers []string) {
	for _, ob := range obs {
		if isInformer(ob) {
			t, _ := ob["tag"].(string)
			informers = append(informers, strings.TrimSpace(t))
			continue
		}
		servers = append(servers, ob)
	}
	if len(servers) == 0 { // всё оказалось «информерами» — значит, ошиблись; ничего не выкидываем
		return obs, nil
	}
	return
}

// infoFromInformers — срок и трафик из названий информеров (если нет заголовка).
func infoFromInformers(names []string, now time.Time) (expire, totalLeft int64) {
	for _, n := range names {
		if m := reDate.FindStringSubmatch(n); m != nil && expire == 0 {
			var d, mo, y int
			if m[4] != "" {
				y, _ = strconv.Atoi(m[4])
				mo, _ = strconv.Atoi(m[5])
				d, _ = strconv.Atoi(m[6])
			} else {
				d, _ = strconv.Atoi(m[1])
				mo, _ = strconv.Atoi(m[2])
				y, _ = strconv.Atoi(m[3])
				if y < 100 {
					y += 2000
				}
			}
			if mo >= 1 && mo <= 12 && d >= 1 && d <= 31 && y >= 2020 && y < 2100 {
				expire = time.Date(y, time.Month(mo), d, 23, 59, 0, 0, now.Location()).Unix()
			}
		}
		if expire == 0 {
			if m := reDaysLeft.FindStringSubmatch(n); m != nil {
				if dd, _ := strconv.Atoi(m[1]); dd > 0 && dd < 3000 {
					expire = now.Add(time.Duration(dd) * 24 * time.Hour).Unix()
				}
			}
		}
		if m := reGB.FindStringSubmatch(n); m != nil && totalLeft == 0 && regexp.MustCompile(`(?i)(осталось|остаток|left|remaining)`).MatchString(n) {
			v, _ := strconv.ParseFloat(strings.ReplaceAll(m[1], ",", "."), 64)
			mul := float64(1 << 30)
			switch strings.ToUpper(m[2]) {
			case "ТБ", "TB":
				mul = 1 << 40
			case "МБ", "MB":
				mul = 1 << 20
			}
			totalLeft = int64(v * mul)
		}
	}
	return
}

// subEffective — итоговые срок и трафик: заголовок → информеры → ручное «Оплачено до».
func subEffective(s watchSettings) (expire int64, src string) {
	switch {
	case s.SubExpire > 0:
		return s.SubExpire, "провайдер"
	case s.SubInfExpire > 0:
		return s.SubInfExpire, "из названий серверов"
	case s.SubPaidUntil > 0:
		return s.SubPaidUntil, "вручную"
	}
	return 0, ""
}

func fmtLeft(sec int64) string {
	if sec <= 0 {
		return "истекла"
	}
	if sec < 86400 {
		return fmt.Sprintf("%d ч", sec/3600)
	}
	return fmt.Sprintf("%d дн", sec/86400)
}
