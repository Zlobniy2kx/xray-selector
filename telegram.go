package main

// Telegram (через рабочий VPN-сервер — Telegram в России режут).
//
// Кому что приходит:
//   • семейная группа — только «VPN не работает 5+ минут» и «VPN снова работает»,
//     плюс одно закреплённое «живое» сообщение со статусом и кнопкой «🆘 Не грузит»;
//   • личка администратора — всё техническое: переключения, кандидаты, перезапуски,
//     утренняя сводка. Без звука, события за несколько минут склеиваются в одно сообщение;
//   • тихие часы — ночью в группу ничего, в личку только важное и без звука.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

type tgMsg struct {
	kind, group, text string
	t                 time.Time
	short             string // короткий текст для Telegram (с флагами), если есть
}

type tgSender struct {
	a     *app
	queue chan tgMsg

	bmu   sync.Mutex
	batch []tgMsg // для лички: копим и отправляем одним сообщением

	pmu         sync.Mutex
	pollToken   string // токен, с которым сейчас работает приём команд
	lastChat    int64  // последний групповой чат, где боту писали
	lastName    string
	lastChatT   time.Time
	lastPriv    int64 // последний личный чат
	lastPrivUID int64
	lastPrivNm  string
	lastPrivT   time.Time
}

func newTgSender(a *app) *tgSender {
	return &tgSender{a: a, queue: make(chan tgMsg, 128)}
}

// enqueue вызывается из addEvent (под блокировкой сторожа) — только кладёт в очередь.
func (t *tgSender) enqueue(kind, group, text, short string) {
	if kind == "info" {
		return
	}
	select {
	case t.queue <- tgMsg{kind: kind, group: group, text: text, t: time.Now(), short: short}:
	default:
	}
}

func maskToken(tok string) string {
	if len(tok) < 12 {
		if tok == "" {
			return ""
		}
		return "…"
	}
	return tok[:6] + "…" + tok[len(tok)-4:]
}

func (t *tgSender) settings() watchSettings {
	w := t.a.watch
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.settings
}

func inQuiet(st watchSettings, now time.Time) bool {
	if !st.QuietOn || st.QuietFrom == st.QuietTo {
		return false
	}
	h := now.In(time.FixedZone("", st.TzOffsetMin*60)).Hour()
	if st.QuietFrom < st.QuietTo {
		return h >= st.QuietFrom && h < st.QuietTo
	}
	return h >= st.QuietFrom || h < st.QuietTo
}

func kindIcon(k string) string {
	switch k {
	case "switch":
		return "🔄"
	case "warn":
		return "⚠️"
	case "recover":
		return "✅"
	case "restart":
		return "♻️"
	case "auto":
		return "🧪"
	case "critical":
		return "🔴"
	case "restored":
		return "🟢"
	case "help":
		return "🆘"
	}
	return "ℹ️"
}

func (t *tgSender) fmtTime(st watchSettings, tm time.Time) string {
	return tm.In(time.FixedZone("", st.TzOffsetMin*60)).Format("15:04")
}

func (t *tgSender) loop() {
	tick := time.NewTicker(10 * time.Second)
	for {
		select {
		case m := <-t.queue:
			st := t.settings()
			if st.TgToken == "" || st.TgEvents == "off" {
				continue
			}
			if tgLevelOf(m.kind) > tgLevelMax(st.TgLevel) {
				continue // уровень «Только аварии» / «Важное» — подробности не шлём
			}
			quiet := inQuiet(st, m.t)
			group, admin := tgDests(st)
			if tgLevelOf(m.kind) == 0 {
				line := kindIcon(m.kind) + " " + html.EscapeString(m.text)
				if group != 0 && !quiet {
					go t.sendRetry(st.TgToken, group, line, false)
				}
				if admin != 0 {
					go t.sendRetry(st.TgToken, admin, line+"\n<i>"+t.fmtTime(st, m.t)+"</i>", quiet)
				}
				continue
			}
			t.bmu.Lock()
			t.batch = append(t.batch, m)
			t.bmu.Unlock()
		case <-tick.C:
			t.flushBatch(false)
		}
	}
}

// flushBatch — отправить накопленное админу одним сообщением: если минуту было тихо
// или первое событие ждёт уже 5 минут.
func (t *tgSender) flushBatch(force bool) {
	t.bmu.Lock()
	if len(t.batch) == 0 {
		t.bmu.Unlock()
		return
	}
	first, last := t.batch[0].t, t.batch[len(t.batch)-1].t
	if !force && time.Since(last) < time.Minute && time.Since(first) < 5*time.Minute {
		t.bmu.Unlock()
		return
	}
	msgs := t.batch
	t.batch = nil
	t.bmu.Unlock()
	st := t.settings()
	_, dest := tgDests(st)
	if dest == 0 {
		return // лички нет — техническое только в панели (группу не засоряем)
	}
	var b strings.Builder
	if len(msgs) > 1 {
		fmt.Fprintf(&b, "<b>События VPN</b> (%d)\n", len(msgs))
	}
	for _, m := range msgs {
		if len(msgs) > 1 {
			b.WriteString("<i>" + t.fmtTime(st, m.t) + "</i> ")
		}
		if m.short != "" { // коротко, с флагами: «🇫🇷 Франция упала → 🇵🇱 Польша»
			b.WriteString(html.EscapeString(m.short) + "\n")
			continue
		}
		b.WriteString(kindIcon(m.kind) + " ")
		if m.group != "" && len(msgs) == 1 && len(st.TgLevel) > 0 && st.TgLevel == "all" {
			b.WriteString("<b>" + html.EscapeString(m.group) + "</b>: ")
		}
		b.WriteString(html.EscapeString(m.text) + "\n")
	}
	if len(msgs) == 1 {
		b.WriteString("<i>" + t.fmtTime(st, msgs[0].t) + "</i>")
	}
	go t.sendRetry(st.TgToken, dest, strings.TrimSpace(b.String()), true)
}

func (t *tgSender) sendRetry(token string, chat int64, text string, silent bool) {
	var err error
	for attempt := 0; attempt < 8; attempt++ {
		if _, err = t.sendOpt(token, chat, text, silent, nil); err == nil {
			return
		}
		var te *tgError
		if errors.As(err, &te) {
			break
		}
		time.Sleep(time.Duration(15+attempt*15) * time.Second)
	}
	log.Printf("telegram: не удалось отправить: %v", err)
}

// candidatePorts — через какие серверы пробовать: сначала активные, потом здоровые
// из списков сторожа, потом остальные из списков.
func (t *tgSender) candidatePorts() (map[string]int, []string, error) {
	a := t.a
	c := a.reload()
	ports, err := a.wprobe.ensure(c, a.watchProbeTags())
	if err != nil {
		return nil, nil, err
	}
	a.mu.Lock()
	var order []string
	seen := map[string]bool{}
	add := func(s string) {
		if s != "" && !seen[s] {
			if _, ok := ports[s]; ok {
				seen[s] = true
				order = append(order, s)
			}
		}
	}
	for _, v := range a.desired {
		add(v)
	}
	a.mu.Unlock()
	w := a.watch
	w.mu.Lock()
	var rest []string
	for _, g := range w.groups {
		for _, s := range g.Servers {
			if h := w.health[s]; h != nil && h.OK {
				add(s)
			} else {
				rest = append(rest, s)
			}
		}
	}
	w.mu.Unlock()
	for _, s := range rest {
		add(s)
	}
	if len(order) == 0 {
		for _, s := range c.servers {
			if len(order) >= 5 {
				break
			}
			add(s.Tag)
		}
	}
	if len(order) > 6 {
		order = order[:6]
	}
	return ports, order, nil
}

func (t *tgSender) api(method string, token string, body any, out any) error {
	return t.apiT(method, token, body, out, 12*time.Second)
}

func (t *tgSender) apiT(method string, token string, body any, out any, timeout time.Duration) error {
	ports, order, err := t.candidatePorts()
	if err != nil {
		return err
	}
	if len(order) == 0 {
		return errors.New("нет серверов, через которые можно отправить")
	}
	b, _ := json.Marshal(body)
	var lastErr error
	for _, tag := range order {
		lastErr = tgCall(ports[tag], token, method, b, out, timeout)
		if lastErr == nil {
			return nil
		}
		var te *tgError
		if errors.As(lastErr, &te) {
			return lastErr
		}
	}
	return lastErr
}

type tgError struct{ msg string }

func (e *tgError) Error() string { return e.msg }

func tgCall(port int, token, method string, body []byte, out any, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cl, tr := socksClient(port)
	defer tr.CloseIdleConnections()
	req, _ := http.NewRequestWithContext(ctx, "POST", *flagTgAPI+"/bot"+token+"/"+method, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := cl.Do(req)
	if err != nil {
		return cleanErr(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var r struct {
		OK          bool            `json:"ok"`
		Description string          `json:"description"`
		Result      json.RawMessage `json:"result"`
	}
	if json.Unmarshal(data, &r) != nil {
		return fmt.Errorf("непонятный ответ Telegram (HTTP %d)", resp.StatusCode)
	}
	if !r.OK {
		d := r.Description
		if resp.StatusCode == 401 || resp.StatusCode == 404 {
			d = "неверный токен бота"
		}
		return &tgError{"Telegram: " + d}
	}
	if out != nil {
		return json.Unmarshal(r.Result, out)
	}
	return nil
}

func (t *tgSender) send(token string, chat int64, text string) error {
	_, err := t.sendOpt(token, chat, text, false, nil)
	return err
}

// sendOpt — отправить сообщение; вернуть его номер (для закрепления/удаления).
func (t *tgSender) sendOpt(token string, chat int64, text string, silent bool, markup any) (int64, error) {
	body := map[string]any{"chat_id": chat, "text": text, "parse_mode": "HTML", "disable_web_page_preview": true}
	if silent {
		body["disable_notification"] = true
	}
	if markup != nil {
		body["reply_markup"] = markup
	}
	var res struct {
		MessageID int64 `json:"message_id"`
	}
	err := t.api("sendMessage", token, body, &res)
	tgStat.note(err)
	return res.MessageID, err
}

func (t *tgSender) deleteLater(token string, chat, msg int64, after time.Duration) {
	go func() {
		time.Sleep(after)
		_ = t.api("deleteMessage", token, map[string]any{"chat_id": chat, "message_id": msg}, nil)
	}()
}

// handleTelegram: POST {token, action}
//
//	test  — сохранить токен, найти группу по последнему сообщению, прислать приветствие;
//	admin — подключить личку администратора (кто последним написал боту /start в личке);
//	pin   — создать/пересоздать закреплённый статус в группе;
//	clear — отключить бота.
func (a *app) handleTelegram(rw http.ResponseWriter, r *http.Request) {
	var req struct {
		Token  string `json:"token"`
		Action string `json:"action"`
	}
	if r.Method == http.MethodGet { // состояние доставки (2.3)
		st := a.tg.settings()
		g, ad := tgDests(st)
		m := tgStat.snap()
		m["group"], m["admin"], m["privateAsGroup"] = g != 0, ad != 0, st.TgChatID > 0
		writeJSON(rw, m)
		return
	}
	if r.Method != "POST" || json.NewDecoder(r.Body).Decode(&req) != nil {
		http.Error(rw, "bad request", 400)
		return
	}
	fail := func(msg string) {
		rw.WriteHeader(400)
		writeJSON(rw, map[string]any{"error": msg})
	}
	w := a.watch
	save := func() {
		a.mu.Lock()
		a.saveStateLocked()
		a.mu.Unlock()
	}
	switch req.Action {
	case "clear":
		w.mu.Lock()
		w.settings.TgToken, w.settings.TgChatID, w.settings.TgAdminID, w.settings.TgPinID = "", 0, 0, 0
		w.mu.Unlock()
		save()
		writeJSON(rw, map[string]any{"ok": true})
		return
	case "admin":
		st := a.tg.settings()
		if st.TgToken == "" {
			fail("Сначала подключите бота к группе (токен).")
			return
		}
		a.tg.pmu.Lock()
		id, uid, nm, at := a.tg.lastPriv, a.tg.lastPrivUID, a.tg.lastPrivNm, a.tg.lastPrivT
		a.tg.pmu.Unlock()
		if id == 0 || time.Since(at) > 15*time.Minute {
			fail("Откройте бота в Telegram (личный чат, не группа), нажмите «Запустить» или напишите /start, затем снова нажмите эту кнопку.")
			return
		}
		if _, err := a.tg.sendOpt(st.TgToken, id, "🔐 Это ваш личный канал Xray Selector: сюда — технические события, утренняя сводка и управление (/help).", false, nil); err != nil {
			fail("Не удалось написать в личку: " + err.Error())
			return
		}
		w.mu.Lock()
		w.settings.TgAdminID, w.settings.TgAdminUID, w.settings.TgAdminName = id, uid, nm
		w.mu.Unlock()
		save()
		writeJSON(rw, map[string]any{"ok": true, "name": nm})
		return
	case "sample": // 0.9: пример настоящего уведомления — тем же путём, что и реальные (очередь → пачка → отправка)
		a.tg.enqueue("switch", "", "пример уведомления из панели", "🧪 Пример: ❌ 🇵🇱 Польша не работает → ✅ 🇩🇪 Германия (не открывались: Telegram, WhatsApp)")
		writeJSON(rw, map[string]any{"ok": true, "note": "придёт примерно через минуту — так же, как настоящие"})
		return
	case "sendtest":
		st := a.tg.settings()
		group, admin := tgDests(st)
		if st.TgToken == "" || (group == 0 && admin == 0) {
			fail("Telegram не подключён: вставьте токен и нажмите «Подключить».")
			return
		}
		res := map[string]any{"ok": true}
		text := "🧪 Проверка связи от Xray Selector. Если вы это видите — уведомления доходят."
		if admin != 0 {
			if _, err := a.tg.sendOpt(st.TgToken, admin, text, false, nil); err != nil {
				res["ok"], res["adminErr"] = false, err.Error()
			} else {
				res["admin"] = "доставлено"
			}
		}
		if group != 0 {
			if _, err := a.tg.sendOpt(st.TgToken, group, text, true, nil); err != nil {
				res["ok"], res["groupErr"] = false, err.Error()
			} else {
				res["group"] = "доставлено"
			}
		}
		writeJSON(rw, res)
		return
	case "pin":
		w.mu.Lock()
		w.settings.TgPinID = 0
		w.mu.Unlock()
		if err := a.updatePinnedStatus(true); err != nil {
			fail(err.Error())
			return
		}
		writeJSON(rw, map[string]any{"ok": true})
		return
	}
	// test — подключение группы
	tok := strings.TrimSpace(req.Token)
	w.mu.Lock()
	if tok == "" || strings.Contains(tok, "…") {
		tok = w.settings.TgToken
	}
	w.mu.Unlock()
	if tok == "" || !strings.Contains(tok, ":") {
		fail("Вставьте токен бота от @BotFather (вида 123456789:AA…)")
		return
	}
	var chat int64
	var name string
	a.tg.pmu.Lock()
	polling := a.tg.pollToken == tok
	if polling && a.tg.lastChat != 0 {
		chat, name = a.tg.lastChat, a.tg.lastName
	}
	a.tg.pmu.Unlock()
	if !polling {
		var ups []tgUpdate
		if err := a.tg.api("getUpdates", tok, map[string]any{"limit": 100, "timeout": 0}, &ups); err != nil {
			fail("Не удалось связаться с Telegram: " + err.Error())
			return
		}
		var priv int64
		var privName string
		for _, u := range ups {
			if c, n, typ, _ := u.chatInfo(); c != 0 && typ != "private" {
				chat, name = c, n
			} else if c != 0 && typ == "private" {
				priv, privName = c, n
			}
		}
		if chat == 0 && priv != 0 { // группы нет, но боту писали в личку — шлём туда всё
			chat, name = priv, privName
		}
	}
	w.mu.Lock()
	if chat == 0 {
		chat = w.settings.TgChatID
	}
	w.mu.Unlock()
	if chat == 0 {
		var me struct {
			Username string `json:"username"`
			Name     string `json:"first_name"`
		}
		who := ""
		if a.tg.api("getMe", tok, map[string]any{}, &me) == nil && me.Username != "" {
			who = fmt.Sprintf(" Этот токен — от бота «%s» (@%s): убедитесь, что в группу добавлен именно он, и напишите там /start@%s.", me.Name, me.Username, me.Username)
		}
		fail("Бот пока не видит сообщений из группы. Добавьте его в семейную группу и напишите там /start@имя_бота, затем снова нажмите «Проверить»." + who)
		return
	}
	hello := "✅ <b>Xray Selector</b> подключён. В этой группе будут только редкие важные сообщения: «VPN не работает» и «VPN снова работает», а также закреплённый статус."
	if chat > 0 {
		hello = "✅ <b>Xray Selector</b> подключён к этому личному чату. Сюда придут аварии и важные события (уровень — в настройках панели)."
	}
	if err := a.tg.send(tok, chat, hello); err != nil {
		fail("Группа найдена, но отправить сообщение не удалось: " + err.Error())
		return
	}
	w.mu.Lock()
	if w.settings.TgChatID != chat {
		w.settings.TgPinID = 0
	}
	w.settings.TgToken, w.settings.TgChatID = tok, chat
	w.mu.Unlock()
	save()
	log.Printf("telegram: подключена группа %d (%s)", chat, name)
	go a.updatePinnedStatus(true)
	writeJSON(rw, map[string]any{"ok": true, "chat": name, "chatID": chat})
}
