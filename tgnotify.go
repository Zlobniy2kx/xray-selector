package main

// 2.3, п. 7: доставка в Telegram.
//   • уровни: crit — только аварии; important — важное (по умолчанию); all — всё, включая кандидатов;
//   • короткие сообщения с флагами: «🇫🇷 Франция упала → 🇵🇱 Польша»;
//   • личный чат вместо группы: если «группа» — это личка (id > 0), туда же идёт всё, что шло админу;
//   • «Отправить тест» и последняя ошибка отправки.

import (
	"strings"
	"sync"
	"time"
)

var countryRU = map[string]string{
	"AE": "ОАЭ", "AL": "Албания", "AM": "Армения", "AR": "Аргентина", "AT": "Австрия", "AU": "Австралия", "AZ": "Азербайджан",
	"BE": "Бельгия", "BG": "Болгария", "BR": "Бразилия", "BY": "Беларусь", "CA": "Канада", "CH": "Швейцария", "CL": "Чили",
	"CN": "Китай", "CY": "Кипр", "CZ": "Чехия", "DE": "Германия", "DK": "Дания", "EE": "Эстония", "ES": "Испания",
	"FI": "Финляндия", "FR": "Франция", "GB": "Великобритания", "GE": "Грузия", "GR": "Греция", "HK": "Гонконг",
	"HR": "Хорватия", "HU": "Венгрия", "ID": "Индонезия", "IE": "Ирландия", "IL": "Израиль", "IN": "Индия", "IS": "Исландия",
	"IT": "Италия", "JP": "Япония", "KG": "Киргизия", "KR": "Корея", "KZ": "Казахстан", "LT": "Литва", "LU": "Люксембург",
	"LV": "Латвия", "MD": "Молдова", "MX": "Мексика", "MY": "Малайзия", "NL": "Нидерланды", "NO": "Норвегия", "NZ": "Новая Зеландия",
	"PL": "Польша", "PT": "Португалия", "RO": "Румыния", "RS": "Сербия", "RU": "Россия", "SE": "Швеция", "SG": "Сингапур",
	"SI": "Словения", "SK": "Словакия", "TH": "Таиланд", "TR": "Турция", "TW": "Тайвань", "UA": "Украина", "US": "США",
	"UZ": "Узбекистан", "VN": "Вьетнам", "ZA": "ЮАР", "ME": "Черногория", "MK": "Македония", "BA": "Босния",
}

// flagCC — флаг и код страны из начала тега («🇫🇷 FR-1 (vless)» → 🇫🇷, FR, «FR-1 (vless)»).
func flagCC(tag string) (flag, cc, rest string) {
	r := []rune(tag)
	if len(r) >= 2 && r[0] >= 0x1F1E6 && r[0] <= 0x1F1FF && r[1] >= 0x1F1E6 && r[1] <= 0x1F1FF {
		return string(r[:2]), string([]rune{r[0] - 0x1F1E6 + 'A', r[1] - 0x1F1E6 + 'A'}), strings.TrimSpace(string(r[2:]))
	}
	return "", "", tag
}

// shortSrv — «🇫🇷 Франция» или «🇫🇷 Франция (FR-2)», если нужно отличить от other.
func shortSrv(tag, other string) string {
	if strings.HasPrefix(tag, reservePrefix) {
		return "🛟 " + strings.TrimSpace(strings.TrimPrefix(tag, reservePrefix))
	}
	f, cc, rest := flagCC(tag)
	if i := strings.Index(rest, "("); i > 0 {
		rest = strings.TrimSpace(rest[:i])
	}
	name := countryRU[cc]
	if name == "" {
		if f == "" {
			return rest
		}
		return f + " " + rest
	}
	_, cc2, _ := flagCC(other)
	if cc2 == cc && rest != "" {
		return f + " " + name + " (" + rest + ")"
	}
	return f + " " + name
}

// уровень события: 0 — авария, 1 — важное, 2 — подробности
func tgLevelOf(kind string) int {
	switch kind {
	case "critical", "restored", "help":
		return 0
	case "auto":
		return 2
	}
	return 1
}

func tgLevelMax(level string) int {
	switch level {
	case "crit":
		return 0
	case "all":
		return 2
	}
	return 1 // important
}

type tgStatus struct {
	mu      sync.Mutex
	lastErr string
	errT    int64
	okT     int64
}

func (s *tgStatus) note(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.lastErr, s.errT = err.Error(), time.Now().Unix()
	} else {
		s.okT = time.Now().Unix()
	}
}

func (s *tgStatus) snap() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return map[string]any{"lastErr": s.lastErr, "errT": s.errT, "okT": s.okT}
}

var tgStat = &tgStatus{}

// tgDests — куда слать: группа (только аварии) и админ (всё остальное по уровню).
// Если «группа» на деле личный чат (id > 0) и админ не привязан — админ = этот чат.
func tgDests(st watchSettings) (group, admin int64) {
	group, admin = st.TgChatID, st.TgAdminID
	if admin == 0 && group > 0 {
		admin = group
	}
	if group == admin {
		group = 0 // не дублировать
	}
	return
}
