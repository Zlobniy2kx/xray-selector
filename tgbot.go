package main

// Бот: команды, кнопки, закреплённый статус, «🆘 Не грузит», утренняя сводка,
// сигнал «VPN не работает 5+ минут».
//
// В группе: /status (ответ и вопрос удаляются через минуту), кнопка «🆘 Не грузит»
// под закреплённым статусом (ответ — всплывающим окном только нажавшему).
// В личке администратора: /status /servers /check /switch /restart /traffic /devices /help.
// Остальным бот не отвечает.

import (
	"context"
	"fmt"
	"html"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type tgChat struct {
	ID        int64  `json:"id"`
	Type      string `json:"type"`
	Title     string `json:"title"`
	FirstName string `json:"first_name"`
}

type tgUser struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name"`
	Username  string `json:"username"`
}

type tgMessage struct {
	MessageID int64  `json:"message_id"`
	Text      string `json:"text"`
	Chat      tgChat `json:"chat"`
	From      tgUser `json:"from"`
}

type tgUpdate struct {
	UpdateID     int64      `json:"update_id"`
	Message      *tgMessage `json:"message"`
	MyChatMember *struct {
		Chat tgChat `json:"chat"`
	} `json:"my_chat_member"`
	Callback *struct {
		ID      string     `json:"id"`
		From    tgUser     `json:"from"`
		Data    string     `json:"data"`
		Message *tgMessage `json:"message"`
	} `json:"callback_query"`
}

func (u tgUpdate) chatInfo() (id int64, name, typ string, from tgUser) {
	var c tgChat
	switch {
	case u.Message != nil:
		c, from = u.Message.Chat, u.Message.From
	case u.MyChatMember != nil:
		c = u.MyChatMember.Chat
	case u.Callback != nil && u.Callback.Message != nil:
		c, from = u.Callback.Message.Chat, u.Callback.From
	}
	name = c.Title
	if name == "" {
		name = c.FirstName
	}
	return c.ID, name, c.Type, from
}

var tgAdminCommands = []map[string]string{
	{"command": "status", "description": "Через какой сервер сейчас идёт VPN"},
	{"command": "servers", "description": "Серверы сторожа"},
	{"command": "switch", "description": "Сменить сервер"},
	{"command": "check", "description": "Проверить серверы сейчас"},
	{"command": "traffic", "description": "Трафик за сегодня"},
	{"command": "devices", "description": "Кто в сети"},
	{"command": "restart", "description": "Перезапустить Xray"},
	{"command": "help", "description": "Что умеет бот"},
}

var tgGroupCommands = []map[string]string{
	{"command": "status", "description": "Работает ли VPN"},
}

func (t *tgSender) pollLoop() {
	var offset int64
	var token, botUser string
	for {
		st := t.settings()
		if st.TgToken == "" {
			t.pmu.Lock()
			t.pollToken = ""
			t.pmu.Unlock()
			time.Sleep(5 * time.Second)
			continue
		}
		if st.TgToken != token {
			token, botUser, offset = st.TgToken, "", 0
		}
		if botUser == "" {
			var me struct {
				Username string `json:"username"`
			}
			if err := t.api("getMe", token, map[string]any{}, &me); err != nil {
				time.Sleep(20 * time.Second)
				continue
			}
			botUser = me.Username
			_ = t.api("setMyCommands", token, map[string]any{"commands": tgAdminCommands, "scope": map[string]string{"type": "all_private_chats"}}, nil)
			_ = t.api("setMyCommands", token, map[string]any{"commands": tgGroupCommands, "scope": map[string]string{"type": "all_group_chats"}}, nil)
			log.Printf("telegram: приём команд для @%s", botUser)
		}
		t.pmu.Lock()
		t.pollToken = token
		t.pmu.Unlock()

		var ups []tgUpdate
		err := t.apiT("getUpdates", token, map[string]any{
			"offset": offset, "timeout": 25, "allowed_updates": []string{"message", "my_chat_member", "callback_query"},
		}, &ups, 40*time.Second)
		if err != nil {
			if strings.Contains(err.Error(), "неверный токен") {
				botUser = ""
				time.Sleep(60 * time.Second)
			} else {
				time.Sleep(10 * time.Second)
			}
			continue
		}
		for _, u := range ups {
			if u.UpdateID >= offset {
				offset = u.UpdateID + 1
			}
			chat, name, typ, from := u.chatInfo()
			if chat == 0 {
				continue
			}
			t.pmu.Lock()
			if typ == "private" {
				t.lastPriv, t.lastPrivUID, t.lastPrivNm, t.lastPrivT = chat, from.ID, from.FirstName, time.Now()
			} else {
				t.lastChat, t.lastName, t.lastChatT = chat, name, time.Now()
			}
			t.pmu.Unlock()
			st = t.settings()
			isAdmin := st.TgAdminUID != 0 && from.ID == st.TgAdminUID
			if st.TgAdminID == 0 && st.TgChatID > 0 && chat == st.TgChatID { // личный чат вместо группы (2.3)
				isAdmin = true
			}
			if u.Callback != nil {
				go t.handleCallback(token, st, u, isAdmin)
				continue
			}
			if u.Message == nil || !strings.HasPrefix(u.Message.Text, "/") {
				continue
			}
			cmd := strings.Fields(u.Message.Text)[0]
			if i := strings.Index(cmd, "@"); i > 0 {
				if !strings.EqualFold(cmd[i+1:], botUser) {
					continue
				}
				cmd = cmd[:i]
			}
			cmd = strings.ToLower(cmd)
			switch {
			case chat == st.TgChatID && typ != "private":
				go t.handleGroupCommand(token, st, chat, u.Message.MessageID, cmd)
			case typ == "private" && isAdmin:
				go t.handleAdminCommand(token, chat, cmd, botUser)
			case typ == "private" && cmd == "/start":
				// незнакомцу молчим; админ подключается кнопкой в панели
			}
		}
	}
}

func (t *tgSender) handleGroupCommand(token string, st watchSettings, chat, msgID int64, cmd string) {
	var text string
	switch cmd {
	case "/status", "/start", "/help":
		text = t.a.tgShortStatus()
	default:
		return
	}
	id, err := t.sendOpt(token, chat, text, true, nil)
	if err == nil && st.TgCleanGroup {
		t.deleteLater(token, chat, id, time.Minute)
		t.deleteLater(token, chat, msgID, time.Minute)
	}
}

func (t *tgSender) handleAdminCommand(token string, chat int64, cmd, botUser string) {
	a := t.a
	reply := func(s string, kb any) { t.sendOpt(token, chat, s, false, kb) }
	switch cmd {
	case "/status":
		reply(a.tgStatusText(), tgMenu())
	case "/servers":
		reply(a.tgServersText(), nil)
	case "/check":
		reply("⏳ Проверяю серверы сторожа, до 30 секунд…", nil)
		a.watchRound(true)
		reply(a.tgServersText(), nil)
	case "/switch":
		reply("Через какой сервер пустить VPN?", a.tgSwitchKeyboard())
	case "/restart":
		reply("Перезапустить XKeen/Xray? Связь у всех устройств прервётся на 5–10 секунд.",
			map[string]any{"inline_keyboard": [][]map[string]string{{{"text": "♻ Да, перезапустить", "callback_data": "rs:yes"}, {"text": "Отмена", "callback_data": "rs:no"}}}})
	case "/traffic":
		reply(a.tgTrafficText(), nil)
	case "/devices":
		reply(a.tgDevicesText(), nil)
	case "/vpn", "/vpn1h", "/phone":
		kb := a.tgDeviceKeyboard()
		if kb == nil {
			reply("Не вижу устройств в сети. Дайте им имена в панели (Устройства).", nil)
		} else {
			reply("Какое устройство пустить на 1 час полностью через VPN?", kb)
		}
	case "/menu":
		reply("Что сделать?", tgMenu())
	default:
		reply("🤖 <b>Xray Selector</b>\n\n/status — через какой сервер идёт VPN\n/servers — серверы сторожа\n/switch — сменить сервер\n/vpn — устройство на 1 час всё через VPN\n/check — проверить сейчас\n/traffic — трафик за сегодня\n/devices — кто в сети\n/restart — перезапустить Xray\n\nВ группе бот показывает только закреплённый статус и пишет, если VPN не работает.", nil)
	}
}

func (a *app) tgSwitchKeyboard() any {
	w := a.watch
	w.mu.Lock()
	defer w.mu.Unlock()
	var rows [][]map[string]string
	for gtag, g := range w.groups {
		for i, s := range g.Servers {
			label := s
			if h := w.health[s]; h != nil && h.T > 0 {
				if h.OK {
					label = "🟢 " + s
				} else {
					label = "🔴 " + s
				}
			}
			rows = append(rows, []map[string]string{{"text": label, "callback_data": "sw:" + gtag + ":" + strconv.Itoa(i)}})
		}
		rows = append(rows, []map[string]string{{"text": "⟳ Авто (Xray сам)", "callback_data": "sw:" + gtag + ":auto"}})
	}
	return map[string]any{"inline_keyboard": rows}
}

var helpLimiter = struct {
	sync.Mutex
	last map[int64]time.Time
}{last: map[int64]time.Time{}}

func (t *tgSender) handleCallback(token string, st watchSettings, u tgUpdate, isAdmin bool) {
	a := t.a
	cb := u.Callback
	answer := func(text string, alert bool) {
		t.api("answerCallbackQuery", token, map[string]any{"callback_query_id": cb.ID, "text": text, "show_alert": alert}, nil)
	}
	var cbChat int64
	if cb.Message != nil {
		cbChat = cb.Message.Chat.ID
	} else {
		_, cbChat = tgDests(st)
	}
	switch {
	case cb.Data == "help":
		helpLimiter.Lock()
		if time.Since(helpLimiter.last[cb.From.ID]) < time.Minute {
			helpLimiter.Unlock()
			answer("Уже проверяю, подождите минуту 🙂", false)
			return
		}
		helpLimiter.last[cb.From.ID] = time.Now()
		helpLimiter.Unlock()
		answer("⏳ Проверяю VPN, это займёт до 30 секунд…", false)
		res := a.helpCheck()
		// ответ — в личку нажавшему нельзя (бот не может первым), поэтому короткое сообщение
		// в группу без звука, которое само удалится через 2 минуты
		who := cb.From.FirstName
		msg := "🆘 " + html.EscapeString(who) + ": " + html.EscapeString(res)
		if id, err := t.sendOpt(token, st.TgChatID, msg, true, nil); err == nil {
			t.deleteLater(token, st.TgChatID, id, 2*time.Minute)
		}
		a.watch.mu.Lock()
		a.watch.addEvent("", "help", who+" нажал(а) «Не грузит»: "+res)
		a.watch.mu.Unlock()
		go a.updatePinnedStatus(false)
	case strings.HasPrefix(cb.Data, "m:") && isAdmin && cb.Message == nil:
		answer("Откройте /menu заново", true)
	case cb.Data == "m:status" && isAdmin:
		answer("", false)
		t.sendOpt(token, cbChat, a.tgStatusText(), false, tgMenu())
	case cb.Data == "m:switch" && isAdmin:
		answer("", false)
		t.sendOpt(token, cbChat, "Через какой сервер пустить VPN?", false, a.tgSwitchKeyboard())
	case cb.Data == "m:vpn" && isAdmin:
		answer("", false)
		if kb := a.tgDeviceKeyboard(); kb != nil {
			t.sendOpt(token, cbChat, "Какое устройство пустить на 1 час полностью через VPN?", false, kb)
		} else {
			t.sendOpt(token, cbChat, "Не вижу устройств в сети.", false, nil)
		}
	case strings.HasPrefix(cb.Data, "dv:") && isAdmin:
		ip := strings.TrimPrefix(cb.Data, "dv:")
		answer("⏳ Применяю, связь пропадёт на 5–10 секунд…", false)
		name, err := a.tempDeviceVPN(ip, 60)
		msg := "✅ " + html.EscapeString(name) + ": 1 час всё через VPN. Потом вернётся само."
		if err != nil {
			msg = "Не получилось: " + html.EscapeString(err.Error())
		}
		t.sendOpt(token, cbChat, msg, false, nil)
	case strings.HasPrefix(cb.Data, "sw:") && isAdmin:
		p := strings.SplitN(cb.Data, ":", 3)
		if len(p) != 3 {
			return
		}
		target := ""
		if p[2] != "auto" {
			i, _ := strconv.Atoi(p[2])
			a.watch.mu.Lock()
			if g := a.watch.groups[p[1]]; g != nil && i < len(g.Servers) {
				target = g.Servers[i]
			}
			a.watch.mu.Unlock()
			if target == "" {
				answer("Список изменился — откройте /switch заново", true)
				return
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		err := a.api.OverrideBalancer(ctx, p[1], target)
		cancel()
		if err != nil {
			answer("Не получилось: "+err.Error(), true)
			return
		}
		a.manualSelected(p[1], target)
		a.mu.Lock()
		if target == "" {
			delete(a.desired, p[1])
		} else {
			a.desired[p[1]] = target
		}
		a.saveStateLocked()
		a.mu.Unlock()
		if target == "" {
			target = "Авто"
		}
		answer("Готово: "+target, false)
		t.sendOpt(token, cbChat, "✅ VPN теперь через <b>"+html.EscapeString(target)+"</b>", true, nil)
		go a.updatePinnedStatus(false)
	case cb.Data == "rs:yes" && isAdmin:
		answer("Перезапускаю…", false)
		a.watch.mu.Lock()
		a.watch.pauseUntil = time.Now().Unix() + 40
		a.watch.addEvent("", "info", "перезапуск XKeen/Xray из Telegram")
		a.watch.mu.Unlock()
		err := a.restartXray()
		for _, d := range []time.Duration{2, 4, 8} {
			time.Sleep(d * time.Second)
			a.applyOnce()
		}
		if err != nil {
			t.sendOpt(token, cbChat, "⚠️ Перезапуск не удался: "+html.EscapeString(err.Error()), false, nil)
		} else {
			t.sendOpt(token, cbChat, "♻ Xray перезапущен.", true, nil)
		}
	case cb.Data == "rs:no":
		answer("Отменено", false)
	default:
		answer("Эта кнопка только для администратора", false)
	}
}

// helpCheck — «Не грузит»: срочная проверка и, если надо, переключение.
func (a *app) helpCheck() string {
	g, before := a.firstWatchGroup()
	if g == "" {
		return "сторож не настроен — сообщил администратору"
	}
	a.watchRound(true)
	a.watchRound(false) // вторая проверка: переключение после 2 сбоев подряд
	_, after := a.firstWatchGroup()
	a.watch.mu.Lock()
	h := a.watch.health[after]
	a.watch.mu.Unlock()
	switch {
	case after != before && after != "":
		return "переключил VPN на " + after + " — попробуйте ещё раз"
	case h != nil && h.OK:
		return "VPN работает (" + after + "). Перезапустите приложение или выключите-включите Wi-Fi"
	default:
		return "серверы сбоят, администратор уже знает"
	}
}

func (a *app) firstWatchGroup() (string, string) {
	a.watch.mu.Lock()
	var tags []string
	for k, g := range a.watch.groups {
		if g.Enabled {
			tags = append(tags, k)
		}
	}
	a.watch.mu.Unlock()
	if len(tags) == 0 {
		return "", ""
	}
	sort.Strings(tags)
	a.mu.Lock()
	defer a.mu.Unlock()
	return tags[0], a.desired[tags[0]]
}

// ---- закреплённый «живой» статус в группе ----

var pinMu sync.Mutex
var pinLastText string

func (a *app) updatePinnedStatus(force bool) error {
	pinMu.Lock()
	defer pinMu.Unlock()
	st := a.tg.settings()
	if st.TgToken == "" || st.TgChatID == 0 || !st.TgPinStatus {
		return nil
	}
	text := a.tgPinnedText()
	if !force && text == pinLastText && st.TgPinID != 0 {
		return nil
	}
	kb := map[string]any{"inline_keyboard": [][]map[string]string{{{"text": "🆘 Не грузит", "callback_data": "help"}}}}
	if st.TgPinID != 0 {
		err := a.tg.api("editMessageText", st.TgToken, map[string]any{"chat_id": st.TgChatID, "message_id": st.TgPinID,
			"text": text, "parse_mode": "HTML", "reply_markup": kb}, nil)
		if err == nil || strings.Contains(err.Error(), "not modified") {
			pinLastText = text
			return nil
		}
		if !strings.Contains(err.Error(), "not found") && !strings.Contains(err.Error(), "can't be edited") {
			return err
		}
	}
	id, err := a.tg.sendOpt(st.TgToken, st.TgChatID, text, true, kb)
	if err != nil {
		return err
	}
	perr := a.tg.api("pinChatMessage", st.TgToken, map[string]any{"chat_id": st.TgChatID, "message_id": id, "disable_notification": true}, nil)
	a.watch.mu.Lock()
	a.watch.settings.TgPinID = id
	if perr != nil {
		a.watch.addEvent("", "info", "не удалось закрепить статус в группе — сделайте бота администратором с правом «Закреплять сообщения»")
	}
	a.watch.mu.Unlock()
	a.mu.Lock()
	a.saveStateLocked()
	a.mu.Unlock()
	pinLastText = text
	if perr != nil {
		return fmt.Errorf("сообщение отправлено, но закрепить не вышло: сделайте бота администратором группы")
	}
	return nil
}

func (a *app) tgPinnedText() string {
	st := a.tg.settings()
	_, cur := a.firstWatchGroup()
	a.watch.mu.Lock()
	h := a.watch.health[cur]
	var hc srvHealth
	if h != nil {
		hc = *h
	}
	down := a.vpnDownSince
	a.watch.mu.Unlock()
	now := time.Now().In(time.FixedZone("", st.TzOffsetMin*60)).Format("15:04")
	if cur == "" {
		return "⚪ <b>VPN</b>: сторож не настроен\n<i>обновлено " + now + "</i>"
	}
	icon, state := "🟢", "работает"
	switch {
	case down > 0:
		icon, state = "🔴", "не работает с "+time.Unix(down, 0).In(time.FixedZone("", st.TzOffsetMin*60)).Format("15:04")
	case h != nil && !hc.OK:
		icon, state = "🟡", "есть сбои, сторож разбирается"
	}
	s := fmt.Sprintf("%s <b>VPN %s</b>\nСервер: %s", icon, state, html.EscapeString(cur))
	if h != nil && hc.SpeedT > 0 && hc.SpeedErr == "" {
		s += " · " + fmtMbps(hc.SpeedKbps)
	}
	s += "\n<i>обновлено " + now + " · если что-то не грузится — нажмите кнопку</i>"
	return s
}

func (a *app) pinLoop() {
	time.Sleep(30 * time.Second)
	for {
		if err := a.updatePinnedStatus(false); err != nil {
			log.Printf("telegram: статус в группе: %v", err)
		}
		time.Sleep(time.Minute)
	}
}

// ---- «VPN не работает» (критично) ----

func (a *app) vpnDownLoop() {
	var badSince int64
	alerted := false
	for {
		time.Sleep(20 * time.Second)
		g, cur := a.firstWatchGroup()
		if g == "" || cur == "" {
			badSince, alerted = 0, false
			continue
		}
		w := a.watch
		w.mu.Lock()
		bad := true
		if h := w.health[cur]; h == nil || h.T == 0 || h.OK {
			bad = false
		}
		if bad { // плохо, только если нет ни одного здорового в списке
			for _, s := range w.groups[g].Servers {
				if hh := w.health[s]; hh != nil && hh.OK {
					bad = false
				}
			}
		}
		if w.allDown["*"] { // пропал интернет у самого роутера — это тоже «не работает»
			bad = true
		}
		now := time.Now().Unix()
		switch {
		case bad && badSince == 0:
			badSince = now
		case bad && !alerted && now-badSince >= 300:
			alerted = true
			a.vpnDownSince = badSince
			w.addEvent("", "critical", "VPN не работает уже 5 минут. Сторож перебирает серверы, администратор в курсе.")
		case !bad && alerted:
			mins := (now - badSince) / 60
			alerted, badSince, a.vpnDownSince = false, 0, 0
			w.addEvent("", "restored", fmt.Sprintf("VPN снова работает (через %s). Перерыв был около %d мин.", cur, mins))
		case !bad:
			badSince = 0
		}
		w.mu.Unlock()
	}
}

// ---- утренняя сводка (в личку) ----

func (a *app) digestLoop() {
	sent := ""
	for {
		time.Sleep(time.Minute)
		st := a.tg.settings()
		if st.TgToken == "" || st.TgAdminID == 0 || st.DigestHour < 0 {
			continue
		}
		loc := time.FixedZone("", st.TzOffsetMin*60)
		now := time.Now().In(loc)
		day := now.Format("2006-01-02")
		if now.Hour() != st.DigestHour || sent == day {
			continue
		}
		sent = day
		go a.tg.sendRetry(st.TgToken, st.TgAdminID, a.digestText(), true)
	}
}

func (a *app) digestText() string {
	now := time.Now()
	since := now.Add(-24 * time.Hour).Unix()
	w := a.watch
	w.mu.Lock()
	var switches, fails, restarts, crit int
	var cands []string
	for _, e := range w.events {
		if e.T < since {
			continue
		}
		switch e.Kind {
		case "switch":
			switches++
		case "warn":
			fails++
		case "restart":
			restarts++
		case "critical":
			crit++
		case "auto":
			cands = append(cands, e.Text)
		}
	}
	scores := w.scoresLocked(24 * 3600)
	w.mu.Unlock()
	type sc struct {
		t string
		s scoreInfo
	}
	var list []sc
	for t, s := range scores {
		if s.N >= 10 {
			list = append(list, sc{t, s})
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].s.Score > list[j].s.Score })
	var b strings.Builder
	b.WriteString("☀️ <b>Сводка VPN за сутки</b>\n")
	fmt.Fprintf(&b, "Переключений: %d · перезапусков: %d", switches, restarts)
	if crit > 0 {
		fmt.Fprintf(&b, " · <b>VPN не работал: %d раз(а)</b>", crit)
	}
	b.WriteString("\n")
	if len(list) > 0 {
		fmt.Fprintf(&b, "🏆 Лучший: %s — %d баллов, %.1f Мбит/с\n", html.EscapeString(list[0].t), list[0].s.Score, list[0].s.Mbps)
		for i := len(list) - 1; i >= 0 && i >= len(list)-2; i-- {
			if list[i].s.Score < 60 {
				fmt.Fprintf(&b, "⚠️ Слабый: %s — %d баллов, сервисы открывались %d%%\n", html.EscapeString(list[i].t), list[i].s.Score, int(list[i].s.Up*100))
			}
		}
	}
	if t := a.trafficTodayLine(now.Add(-24 * time.Hour)); t != "" {
		b.WriteString(t + "\n")
	}
	if len(cands) > 0 {
		b.WriteString("🧪 " + html.EscapeString(cands[len(cands)-1]) + "\n")
	}
	return strings.TrimSpace(b.String())
}

// ---- тексты ----

func fmtMbps(k int) string {
	if k >= 10000 {
		return fmt.Sprintf("%.0f Мбит/с", float64(k)/1000)
	}
	return fmt.Sprintf("%.1f Мбит/с", float64(k)/1000)
}

func fmtBytes(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1f ГБ", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.0f МБ", float64(b)/(1<<20))
	default:
		return fmt.Sprintf("%.0f КБ", float64(b)/1024)
	}
}

func since(t int64) string {
	if t == 0 {
		return ""
	}
	d := time.Since(time.Unix(t, 0))
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d с", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d мин", int(d.Minutes()))
	default:
		return fmt.Sprintf("%d ч %d мин", int(d.Hours()), int(d.Minutes())%60)
	}
}

func healthLine(st watchSettings, h *srvHealth) string {
	if h == nil || h.T == 0 {
		return "ещё не проверялся"
	}
	var ok, bad []string
	for _, tg := range st.Targets {
		if r, has := h.Targets[tg.Name]; has && r.OK {
			ok = append(ok, tg.Name)
		} else if has {
			bad = append(bad, tg.Name)
		}
	}
	s := fmt.Sprintf("сервисы ✓ %d/%d", len(ok), len(ok)+len(bad))
	if len(bad) > 0 {
		s += " (✕ " + strings.Join(bad, ", ") + ")"
	}
	if h.SpeedT > 0 {
		if h.SpeedErr != "" {
			if strings.Contains(h.SpeedErr, "зависло") {
				s += " · поток завис"
			} else {
				s += " · скорость ✕"
			}
		} else {
			s += " · ⬇ " + fmtMbps(h.SpeedKbps)
		}
	}
	return s
}

func (a *app) tgShortStatus() string {
	return a.tgPinnedText()
}

func (a *app) tgStatusText() string {
	c := a.reload()
	w := a.watch
	w.mu.Lock()
	st := w.settings
	groups := map[string]watchGroup{}
	for k, v := range w.groups {
		groups[k] = *v
	}
	health := map[string]srvHealth{}
	for k, v := range w.health {
		health[k] = *v
	}
	w.mu.Unlock()
	var b strings.Builder
	if len(c.groups) == 0 {
		return "В конфиге Xray нет групп (balancer)."
	}
	for _, g := range c.groups {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		info, err := a.api.GetBalancerInfo(ctx, g.Tag)
		cancel()
		b.WriteString("<b>" + html.EscapeString(g.Tag) + "</b>\n")
		if err != nil {
			b.WriteString("⚠️ нет связи с Xray: " + html.EscapeString(err.Error()) + "\n\n")
			continue
		}
		wg := groups[g.Tag]
		cur := info.Override
		switch {
		case cur != "" && wg.Enabled:
			b.WriteString("Режим: 🛡 сторож\n")
		case cur != "":
			b.WriteString("Режим: вручную\n")
		default:
			b.WriteString("Режим: авто (Xray, leastPing)\n")
			if len(info.Principle) > 0 {
				cur = info.Principle[0]
			}
		}
		if cur == "" {
			b.WriteString("Сервер: нет живых, работает запасной " + html.EscapeString(g.Fallback) + "\n\n")
			continue
		}
		b.WriteString("Сервер: <b>" + html.EscapeString(cur) + "</b>\n")
		if h, ok := health[cur]; ok && wg.Enabled {
			state := "✅ работает"
			if !h.OK {
				state = fmt.Sprintf("⚠️ сбоит (%d раз подряд): %s", h.ConsecFail, h.Reason)
			} else if h.OkSince > 0 {
				state += " без сбоев " + since(h.OkSince)
			}
			b.WriteString(html.EscapeString(state) + "\n")
			b.WriteString(html.EscapeString(healthLine(st, &h)) + "\n")
		}
		b.WriteString("\n")
	}
	if t := a.trafficTodayLine(time.Now()); t != "" {
		b.WriteString(t)
	}
	return strings.TrimSpace(b.String())
}

func (a *app) tgServersText() string {
	a.mu.Lock()
	desired := map[string]string{}
	for k, v := range a.desired {
		desired[k] = v
	}
	a.mu.Unlock()
	w := a.watch
	w.mu.Lock()
	st := w.settings
	scores := w.scoresLocked(scoreWindow)
	var b strings.Builder
	found := false
	for gtag, g := range w.groups {
		if len(g.Servers) == 0 {
			continue
		}
		found = true
		state := "выключен"
		if g.Enabled {
			state = "включён"
		}
		b.WriteString("<b>" + html.EscapeString(gtag) + "</b> · сторож " + state + "\n")
		pin := map[string]bool{}
		for _, p := range g.Pinned {
			pin[p] = true
		}
		for i, s := range g.Servers {
			var h *srvHealth
			if x := w.health[s]; x != nil {
				cp := *x
				h = &cp
			}
			icon := "⚪"
			if h != nil && h.T > 0 {
				if h.OK {
					icon = "🟢"
				} else {
					icon = "🔴"
				}
			}
			mark := ""
			if desired[gtag] == s {
				mark = " ← <b>активный</b>"
			}
			role := ""
			if g.Auto && !pin[s] {
				role = " 🧪"
			}
			sc := ""
			if x, ok := scores[s]; ok && x.N > 0 {
				sc = fmt.Sprintf(" · %d б.", x.Score)
			}
			fmt.Fprintf(&b, "%s %d. %s%s%s%s\n", icon, i+1, html.EscapeString(s), role, sc, mark)
			b.WriteString("    " + html.EscapeString(healthLine(st, h)) + "\n")
		}
		b.WriteString("\n")
	}
	w.mu.Unlock()
	if !found {
		return "В сторож пока не добавлено ни одного сервера."
	}
	return strings.TrimSpace(b.String())
}

func (a *app) trafficTodayLine(t time.Time) string {
	tr := a.trf
	tr.mu.Lock()
	defer tr.mu.Unlock()
	d := tr.days[a.dayKey(t)]
	if d == nil {
		return ""
	}
	var total bytesPair
	for _, v := range d.Servers {
		total.add(*v)
	}
	if total.Up+total.Down == 0 {
		for _, v := range d.DevVPN {
			total.add(*v)
		}
	}
	if total.Up+total.Down == 0 {
		return ""
	}
	return "📶 Через VPN за день: " + fmtBytes(total.Up+total.Down)
}

func (a *app) tgTrafficText() string {
	tr := a.trf
	tr.mu.Lock()
	d := cloneDay(tr.days[a.dayKey(time.Now())])
	tr.mu.Unlock()
	names := a.deviceLabels()
	var b strings.Builder
	b.WriteString("📶 <b>Трафик за сегодня</b>\n")
	type kv struct {
		k string
		v int64
	}
	top := func(m map[string]*bytesPair, n int, label func(string) string) {
		var l []kv
		for k, v := range m {
			if v.Up+v.Down > 0 {
				l = append(l, kv{k, v.Up + v.Down})
			}
		}
		sort.Slice(l, func(i, j int) bool { return l[i].v > l[j].v })
		for i, x := range l {
			if i >= n {
				break
			}
			fmt.Fprintf(&b, "  %s — %s\n", html.EscapeString(label(x.k)), fmtBytes(x.v))
		}
		if len(l) == 0 {
			b.WriteString("  пока нет данных\n")
		}
	}
	b.WriteString("<b>По серверам:</b>\n")
	top(d.Servers, 6, func(s string) string { return s })
	b.WriteString("<b>По устройствам (через VPN):</b>\n")
	top(d.DevVPN, 8, func(s string) string {
		if n := names[s]; n != "" {
			return n
		}
		return s
	})
	b.WriteString("<b>По сервисам:</b>\n")
	top(d.Services, 6, func(s string) string { return s })
	return strings.TrimSpace(b.String())
}

func (a *app) deviceLabels() map[string]string {
	tr := a.trf
	tr.mu.Lock()
	defer tr.mu.Unlock()
	out := map[string]string{}
	for k, d := range tr.devices {
		n := devLabel(d)
		if n == "" {
			n = d.IP
			if d.Vendor != "" {
				n += " (" + d.Vendor + ")"
			}
		}
		out[k] = n
	}
	return out
}

func (a *app) tgDevicesText() string {
	devs := a.devicesSnapshot()
	var b strings.Builder
	b.WriteString("📱 <b>Устройства (за последние 10 минут)</b>\n")
	n := 0
	for _, d := range devs {
		if time.Since(time.Unix(d["lastSeen"].(int64), 0)) > 10*time.Minute || d["mergeInto"].(string) != "" {
			continue
		}
		n++
		name, _ := d["name"].(string)
		if name == "" {
			name = d["ip"].(string)
		}
		line := "• " + html.EscapeString(name)
		if v, ok := d["vpn"].(bytesPair); ok && v.Up+v.Down > 0 {
			line += " — через VPN сегодня " + fmtBytes(v.Up+v.Down)
		}
		b.WriteString(line + "\n")
	}
	if n == 0 {
		b.WriteString("никого не видно")
	}
	return strings.TrimSpace(b.String())
}

// 2.3, п. 22: кнопки управления в личке.
func tgMenu() any {
	return map[string]any{"inline_keyboard": [][]map[string]string{
		{{"text": "📊 Статус", "callback_data": "m:status"}, {"text": "🔄 Сменить сервер", "callback_data": "m:switch"}},
		{{"text": "📱 Устройство — 1 час всё через VPN", "callback_data": "m:vpn"}},
	}}
}

// tgDeviceKeyboard — устройства, которые были в сети последние 30 минут (с именами — первыми).
func (a *app) tgDeviceKeyboard() any {
	tr := a.trf
	tr.mu.Lock()
	type dv struct {
		name, ip string
		named    bool
		seen     int64
	}
	var l []dv
	now := time.Now().Unix()
	for _, d := range tr.devices {
		if d.MergeInto != "" || d.IP == "" || now-d.LastSeen > 1800 {
			continue
		}
		n := d.Name
		if n == "" {
			n = d.KName
		}
		named := n != ""
		if n == "" {
			n = d.IP
		}
		l = append(l, dv{n, d.IP, named, d.LastSeen})
	}
	tr.mu.Unlock()
	if len(l) == 0 {
		return nil
	}
	sort.Slice(l, func(i, j int) bool {
		if l[i].named != l[j].named {
			return l[i].named
		}
		return l[i].name < l[j].name
	})
	if len(l) > 16 {
		l = l[:16]
	}
	var rows [][]map[string]string
	for _, d := range l {
		rows = append(rows, []map[string]string{{"text": d.name, "callback_data": "dv:" + d.ip}})
	}
	return map[string]any{"inline_keyboard": rows}
}

// tempDeviceVPN — временный режим «всё через VPN» для устройства (как кнопка в панели).
func (a *app) tempDeviceVPN(ip string, minutes int) (string, error) {
	rulesMu.Lock()
	defer rulesMu.Unlock()
	st := loadRulesState()
	dv := st.Devices[ip]
	if dv == nil {
		dv = &devRule{}
		st.Devices[ip] = dv
	}
	name := ip
	tr := a.trf
	tr.mu.Lock()
	for _, d := range tr.devices {
		if d.IP == ip && d.MergeInto == "" {
			name = firstNonEmpty(d.Name, d.KName, ip)
		}
	}
	tr.mu.Unlock()
	dv.Name = name
	dv.Temp, dv.Until = "3", time.Now().Unix()+int64(minutes)*60
	if _, err := a.applyRules(st, "Telegram: "+name+" на "+strconv.Itoa(minutes)+" мин всё через VPN", false); err != nil {
		return name, err
	}
	return name, saveRulesState(st)
}
