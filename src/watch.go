package main

// «Сторож»: часто проверяет короткий список серверов на реальных сервисах
// (Instagram, YouTube…) и скорость скачивания. Если активный сервер перестал
// справляться — переключает группу на следующий здоровый из списка.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type watchTarget struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type watchSettings struct {
	Targets         []watchTarget `json:"targets"`
	IntervalSec     int           `json:"intervalSec"`     // как часто проверять
	TimeoutMs       int           `json:"timeoutMs"`       // таймаут одного запроса
	FailsToSwitch   int           `json:"failsToSwitch"`   // сколько неудач подряд до переключения
	SpeedURL        string        `json:"speedURL"`        // пусто — без теста скорости
	SpeedEverySec   int           `json:"speedEverySec"`   // как часто мерить скорость запасных серверов
	SpeedActiveSec  int           `json:"speedActiveSec"`  // как часто мерить скорость активного сервера
	SpeedMinKbps    int           `json:"speedMinKbps"`    // ниже — сервер считается плохим
	ReturnToFirst   bool          `json:"returnToFirst"`   // возвращаться на более приоритетный
	ReturnAfterSec  int           `json:"returnAfterSec"`  // ...если он здоров столько секунд подряд
	ReserveEverySec int           `json:"reserveEverySec"` // как часто проверять запасные (активный — каждые IntervalSec)

	NoSmartSpeed bool `json:"noSmartSpeed"` // не переходить на заметно более быстрый сервер
	SmartRatio   int  `json:"smartRatio"`   // во сколько раз быстрее должен быть другой сервер
	SmartQuietKb int  `json:"smartQuietKb"` // «тихо» — интернет загружен меньше N кбит/с (последние 3 мин)

	NoBalancerSync bool `json:"noBalancerSync"` // не переписывать состав группы в конфиге Xray

	CandSlots    int `json:"candSlots"`    // сколько мест в списке под кандидатов на проверку
	MainSlots    int `json:"mainSlots"`    // основных (📌)
	ReserveSlots int `json:"reserveSlots"` // запасных (🟡)
	TrialMin     int `json:"trialMin"`     // сколько минут проверять кандидата
	PassScore    int `json:"passScore"`    // сколько баллов нужно, чтобы пройти проверку

	RestartOnSwitch    bool `json:"restartOnSwitch"`    // перезапускать XKeen после переключения из-за сбоя
	RestartCooldownMin int  `json:"restartCooldownMin"` // не чаще, чем раз в N минут

	TgToken     string `json:"tgToken"`
	TgChatID    int64  `json:"tgChatID"`
	TgEvents    string `json:"tgEvents"`    // all | switch | off (off — пауза)
	TgLevel     string `json:"tgLevel"`     // 2.3: crit | important | all
	WizardDone  bool   `json:"wizardDone"`  // 2.3: мастер первого запуска пройден
	TzOffsetMin int    `json:"tzOffsetMin"` // смещение часового пояса для времени в сообщениях

	TgAdminID    int64  `json:"tgAdminID"`  // личный чат администратора
	TgAdminUID   int64  `json:"tgAdminUID"` // его аккаунт (только ему — управление)
	TgAdminName  string `json:"tgAdminName"`
	TgPinStatus  bool   `json:"tgPinStatus"` // закреплённый «живой» статус в группе
	TgPinID      int64  `json:"tgPinID"`
	TgCleanGroup bool   `json:"tgCleanGroup"` // удалять в группе ответы на /status через минуту
	QuietOn      bool   `json:"quietOn"`
	QuietFrom    int    `json:"quietFrom"` // час начала тихих часов
	QuietTo      int    `json:"quietTo"`
	DigestHour   int    `json:"digestHour"` // утренняя сводка в личку (-1 — не присылать)

	SubURL     string `json:"subURL"`     // ссылка на подписку
	SubUA      string `json:"subUA"`      // User-Agent для запроса подписки
	SubAuto    bool   `json:"subAuto"`    // обновлять серверы автоматически
	SubEveryH  int    `json:"subEveryH"`  // как часто, часов
	SubUp      int64  `json:"subUp"`      // из заголовка subscription-userinfo
	SubDown    int64  `json:"subDown"`    //
	SubTotal   int64  `json:"subTotal"`   //
	SubExpire  int64  `json:"subExpire"`  //
	SubChecked int64  `json:"subChecked"` // когда последний раз скачивали
	SubApplied int64  `json:"subApplied"` // когда последний раз применяли изменения
	SubErr     string `json:"subErr"`     //
	SubWarned  int64  `json:"subWarned"`  // когда предупреждали о конце подписки
	// 2.3: сведения провайдера (заголовки profile-*, support-url) и «информеры»
	SubTitle     string   `json:"subTitle,omitempty"`
	SubSupport   string   `json:"subSupport,omitempty"`
	SubWebPage   string   `json:"subWebPage,omitempty"`
	SubUpdateH   int      `json:"subUpdateH,omitempty"`
	SubInformers []string `json:"subInformers,omitempty"`
	SubInfExpire int64    `json:"subInfExpire,omitempty"`
	SubInfLeft   int64    `json:"subInfLeft,omitempty"`
	SubPaidUntil int64    `json:"subPaidUntil,omitempty"` // ручное «Оплачено до»

	TunOff      bool `json:"tunOff"`      // не проверять подключения Keenetic
	TunEverySec int  `json:"tunEverySec"` // как часто их проверять
	ReserveAuto bool `json:"reserveAuto"` // резерв: подхватывать новые туннели Keenetic сами

	PassHash string `json:"passHash,omitempty"` // пароль панели (sha256 с солью), пусто — без пароля
	PassSalt string `json:"passSalt,omitempty"`
	Ver      int    `json:"ver"`
}

func defaultWatchSettings() watchSettings {
	return watchSettings{
		Targets: []watchTarget{
			{"Instagram", "https://www.instagram.com/"},
			{"IG видео", "https://scontent.cdninstagram.com/"},
			{"YouTube", "https://www.youtube.com/generate_204"},
			{"YT видео", "https://redirector.googlevideo.com/report_mapping"},
			{"Telegram", "https://web.telegram.org/"},
			{"TG медиа", "https://cdn4.telesco.pe/"},
			{"WhatsApp", "https://web.whatsapp.com/"},
			{"WA медиа", "https://mmg.whatsapp.net/"},
		},
		IntervalSec:    15,
		TimeoutMs:      6000,
		FailsToSwitch:  2,
		SpeedURL:       "https://speed.cloudflare.com/__down?bytes=300000",
		SpeedEverySec:  150,
		SpeedActiveSec: 30,
		TgEvents:       "all",
		TgPinStatus:    true,
		TgCleanGroup:   true,
		QuietOn:        true,
		QuietFrom:      23,
		QuietTo:        8,
		DigestHour:     9,
		Ver:            2,
		TzOffsetMin:    180,
		SpeedMinKbps:   1500,
		ReturnToFirst:  true,
		ReturnAfterSec: 600,
		CandSlots:      5,
		MainSlots:      6,
		SubUA:          "v2rayN/7.10",
		SubEveryH:      12,
		ReserveSlots:   3,
		TrialMin:       120,
		PassScore:      70,
	}
}

func (s *watchSettings) normalize() {
	d := defaultWatchSettings()
	if s.IntervalSec < 5 {
		s.IntervalSec = d.IntervalSec
	}
	if s.TimeoutMs < 1000 {
		s.TimeoutMs = d.TimeoutMs
	}
	if s.FailsToSwitch < 1 {
		s.FailsToSwitch = d.FailsToSwitch
	}
	// настройки от версии 1.2 (без speedActiveSec) — переводим на новые значения по умолчанию
	if s.SpeedActiveSec == 0 {
		if s.IntervalSec == 30 {
			s.IntervalSec = d.IntervalSec
		}
		if s.SpeedURL == "https://speed.cloudflare.com/__down?bytes=1000000" {
			s.SpeedURL = d.SpeedURL
		}
		if s.SpeedEverySec == 180 {
			s.SpeedEverySec = d.SpeedEverySec
		}
		s.SpeedActiveSec = d.SpeedActiveSec
		if s.TzOffsetMin == 0 {
			s.TzOffsetMin = d.TzOffsetMin
		}
	}
	if s.SpeedEverySec < 30 {
		s.SpeedEverySec = d.SpeedEverySec
	}
	if s.SpeedActiveSec < 10 {
		s.SpeedActiveSec = 10
	}
	if s.Ver < 2 { // настройки до 2.0
		s.TgPinStatus, s.TgCleanGroup, s.QuietOn, s.QuietFrom, s.QuietTo, s.DigestHour = true, true, true, 23, 8, 9
		s.Ver = 2
	}
	if s.QuietFrom < 0 || s.QuietFrom > 23 {
		s.QuietFrom = 23
	}
	if s.QuietTo < 0 || s.QuietTo > 23 {
		s.QuietTo = 8
	}
	if s.DigestHour < -1 || s.DigestHour > 23 {
		s.DigestHour = 9
	}
	switch s.TgEvents {
	case "all", "switch", "off":
	default:
		s.TgEvents = "all"
	}
	switch s.TgLevel {
	case "crit", "important", "all":
	default:
		s.TgLevel = "important"
	}
	s.TgToken = strings.TrimSpace(s.TgToken)
	if s.SpeedMinKbps < 0 {
		s.SpeedMinKbps = 0
	}
	if s.CandSlots < 0 {
		s.CandSlots = 0
	}
	if s.CandSlots == 0 && s.TrialMin == 0 { // настройки старых версий
		s.CandSlots = d.CandSlots
	}
	if s.CandSlots > 10 {
		s.CandSlots = 10
	}
	if s.MainSlots <= 0 && s.ReserveSlots <= 0 { // до 2.0.6
		s.MainSlots, s.ReserveSlots, s.CandSlots = 6, 3, 5
	}
	if s.MainSlots < 1 {
		s.MainSlots = 1
	}
	if s.MainSlots > 12 {
		s.MainSlots = 12
	}
	if s.ReserveSlots < 0 {
		s.ReserveSlots = 0
	}
	if s.ReserveSlots > 6 {
		s.ReserveSlots = 6
	}
	if s.TrialMin < 30 {
		s.TrialMin = d.TrialMin
	}
	if s.PassScore <= 0 || s.PassScore > 100 {
		s.PassScore = d.PassScore
	}
	// добавить Telegram и WhatsApp к старому набору проверок по умолчанию
	if len(s.Targets) == 4 && s.Targets[0].Name == "Instagram" && s.Targets[3].Name == "YT видео" {
		s.Targets = append(s.Targets, d.Targets[4:]...)
	}
	if s.TunEverySec < 30 {
		s.TunEverySec = 60
	}
	if s.SubEveryH <= 0 {
		s.SubEveryH = 12
	}
	if s.SubUA == "" {
		s.SubUA = "v2rayN/7.10"
	}
	s.SubURL = strings.TrimSpace(s.SubURL)
	if s.RestartCooldownMin < 5 {
		s.RestartCooldownMin = 15
	}
	if s.ReturnAfterSec < 60 {
		s.ReturnAfterSec = d.ReturnAfterSec
	}
	if s.ReserveEverySec <= 0 {
		s.ReserveEverySec = 60
	}
	if s.ReserveEverySec < s.IntervalSec {
		s.ReserveEverySec = s.IntervalSec
	}
	if s.SmartRatio < 2 {
		s.SmartRatio = 3
	}
	if s.SmartQuietKb <= 0 {
		s.SmartQuietKb = 1000
	}
	var t []watchTarget
	for _, x := range s.Targets {
		x.Name, x.URL = strings.TrimSpace(x.Name), strings.TrimSpace(x.URL)
		if x.URL != "" && (strings.HasPrefix(x.URL, "http://") || strings.HasPrefix(x.URL, "https://")) {
			if x.Name == "" {
				x.Name = x.URL
			}
			t = append(t, x)
		}
	}
	s.Targets = t
}

type watchGroup struct {
	Enabled  bool     `json:"enabled"`
	Servers  []string `json:"servers"`            // порядок = приоритет
	Auto     bool     `json:"auto"`               // автоподбор лучших по баллам
	Pinned   []string `json:"pinned,omitempty"`   // автоподбор их не убирает
	Excluded []string `json:"excluded,omitempty"` // автоподбор их не добавляет

	Trials map[string]int64 `json:"trials,omitempty"` // кандидат → когда начали проверять
	Tried  map[string]int64 `json:"tried,omitempty"`  // отбракованные → когда (сутки не берём снова)
	Passed map[string]bool  `json:"passed,omitempty"` // кандидаты, прошедшие проверку

	Reserve     []string `json:"reserve,omitempty"` // запасные: прошли проверку, ждут места в основных
	Locked      []string `json:"locked,omitempty"`  // 🔒 — автоматика не понижает и не убирает
	LastPromote int64    `json:"lastPromote,omitempty"`
	LastSort    int64    `json:"lastSort,omitempty"`

	ManualCand map[string]int64 `json:"manualCand,omitempty"` // добавлены на проверку вручную — не вытеснять

	Best string `json:"best,omitempty"` // последний лучший: стабилен 30 мин и самый быстрый — к нему подключаемся сразу после запуска
}

type targetRes struct {
	OK   bool   `json:"ok"`
	Ms   int64  `json:"ms"`
	Code int    `json:"code,omitempty"`
	Err  string `json:"err,omitempty"`
}

type srvHealth struct {
	Targets    map[string]targetRes `json:"targets"`
	SpeedKbps  int                  `json:"speedKbps"`
	SpeedErr   string               `json:"speedErr,omitempty"`
	SpeedT     int64                `json:"speedT"`
	OK         bool                 `json:"ok"`
	Reason     string               `json:"reason,omitempty"`
	ConsecFail int                  `json:"consecFail"`
	ConsecOK   int                  `json:"consecOK"`
	OkSince    int64                `json:"okSince"`
	T          int64                `json:"t"`
	Checks     int                  `json:"checks"`
	Fails      int                  `json:"fails"`
}

type watchEvent struct {
	ID    int64  `json:"id"`
	T     int64  `json:"t"`
	Group string `json:"group"`
	Text  string `json:"text"`
	Kind  string `json:"kind"` // switch | warn | info
}

type watcher struct {
	mu        sync.Mutex
	settings  watchSettings
	groups    map[string]*watchGroup
	health    map[string]*srvHealth
	events    []watchEvent
	lastRound int64
	lastSpeed int64
	running   bool
	kick      chan struct{}
	allDown   map[string]bool
	notify    func(kind, group, text, short string)
	rtNoted   string // п. 21: уже сообщили, что ждём конца игры/звонка
	// п. 29: почасовые итоги до 7 дней
	hourly      map[string][]hourAgg
	runs        map[string]*srvRun
	lastActiveT int64

	lastRestart    int64 // когда последний раз перезапускали XKeen
	nextEvID       int64
	lastReadEv     int64 // колокольчик: до какого события прочитано
	lastAutoTop    int64 // когда последний раз работал автоподбор
	whist          map[string][]wcheck
	lastHistSave   int64
	pauseUntil     int64  // во время перезапуска проверки не делаем
	lastSmart      int64  // когда последний раз переключались ради скорости
	smartWaitEv    string // на какой сервер ждём «тишины»
	smartWaitSince int64
	lastSyncErr    string
	freshUntil     int64 // до этого момента считаем, что только что запустились
	bestChanged    bool
}

// medianSpeedLocked — медиана удачных замеров скорости сервера с момента since, кбит/с (0 — мало данных).
func (w *watcher) medianSpeedLocked(tag string, since int64) int {
	var v []int
	for _, c := range w.whist[tag] {
		if c.T >= since && c.HasSpd && c.SpdOK && c.Kbps > 0 {
			v = append(v, int(c.Kbps))
		}
	}
	if len(v) < 3 {
		return 0
	}
	sort.Ints(v)
	return v[len(v)/2]
}

// stableLocked — с момента since не было ни одной неудачной проверки (и проверок было достаточно).
func (w *watcher) stableLocked(tag string, since int64) bool {
	n := 0
	for _, c := range w.whist[tag] {
		if c.T >= since {
			if !c.OK || (c.HasSpd && !c.SpdOK) {
				return false
			}
			n++
		}
	}
	return n >= 10
}

// quietNow — интернет последние 3 минуты загружен меньше limitKb кбит/с (вход+выход в среднем).
func (a *app) quietNow(limitKb int) bool {
	m := a.met
	m.mu.Lock()
	defer m.mu.Unlock()
	cut := time.Now().Unix() - 180
	sum, n := 0.0, 0
	for i := len(m.points) - 1; i >= 0 && m.points[i].T >= cut; i-- {
		sum += m.points[i].Rx + m.points[i].Tx
		n++
	}
	if n < 30 {
		return false
	}
	return sum/float64(n)*1000 < float64(limitKb)
}

func newWatcher() *watcher {
	return &watcher{
		settings: defaultWatchSettings(),
		groups:   map[string]*watchGroup{},
		health:   map[string]*srvHealth{},
		kick:     make(chan struct{}, 1),
		allDown:  map[string]bool{},
		whist:    map[string][]wcheck{},
	}
}

func (w *watcher) addEvent(group, kind, text string) { w.addEventShort(group, kind, text, "") }

// addEventShort — то же, плюс короткий текст для Telegram («🇫🇷 Франция упала → 🇵🇱 Польша»).
func (w *watcher) addEventShort(group, kind, text, short string) {
	log.Printf("[сторож %s] %s", group, text)
	w.nextEvID++
	w.events = append(w.events, watchEvent{ID: w.nextEvID, T: time.Now().Unix(), Group: group, Text: text, Kind: kind})
	if w.notify != nil {
		w.notify(kind, group, text, short)
	}
	if len(w.events) > 400 {
		w.events = w.events[len(w.events)-400:]
	}
}

// ---- проверки через тестовый xray ----

func socksClient(port int) (*http.Client, *http.Transport) {
	addr := "127.0.0.1:" + strconv.Itoa(port)
	tr := &http.Transport{
		Proxy:               nil,
		DialContext:         func(ctx context.Context, _, a string) (net.Conn, error) { return socks5Dial(ctx, addr, a) },
		TLSHandshakeTimeout: 0,
		DisableKeepAlives:   true,
	}
	return &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, tr
}

// checkURL: сервис считается доступным, если вообще ответил по HTTP (кроме 5xx).
func checkURL(port int, url string, timeout time.Duration) targetRes {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cl, tr := socksClient(port)
	defer tr.CloseIdleConnections()
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36")
	start := time.Now()
	resp, err := cl.Do(req)
	if err != nil {
		return targetRes{Err: cleanErr(err).Error()}
	}
	ms := time.Since(start).Milliseconds()
	// немного читаем тело — убедиться, что данные действительно идут
	io.CopyN(io.Discard, resp.Body, 16*1024)
	resp.Body.Close()
	if resp.StatusCode >= 500 {
		return targetRes{Ms: ms, Code: resp.StatusCode, Err: fmt.Sprintf("HTTP %d", resp.StatusCode)}
	}
	return targetRes{OK: true, Ms: max(ms, 1), Code: resp.StatusCode}
}

// speedTest: скорость скачивания после получения заголовков, кбит/с.
func speedTest(port int, url string, timeout time.Duration) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cl, tr := socksClient(port)
	defer tr.CloseIdleConnections()
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	resp, err := cl.Do(req)
	if err != nil {
		return 0, cleanErr(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return 0, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	start := time.Now()
	n, err := io.Copy(io.Discard, resp.Body)
	el := time.Since(start)
	if n < 64*1024 {
		if err != nil && n > 0 {
			return 0, fmt.Errorf("зависло: пришло %d КБ и поток встал", n/1024)
		}
		if err != nil {
			return 0, cleanErr(err)
		}
		return 0, fmt.Errorf("получено всего %d КБ", n/1024)
	}
	if el < time.Millisecond {
		el = time.Millisecond
	}
	kbps := int(float64(n*8) / el.Seconds() / 1000)
	if err != nil { // оборвалось по таймауту — считаем по тому, что успели
		return kbps, nil
	}
	return kbps, nil
}

// ---- основной цикл ----

func (a *app) watchLoop() {
	time.Sleep(5 * time.Second)
	for {
		w := a.watch
		w.mu.Lock()
		interval := time.Duration(w.settings.IntervalSec) * time.Second
		switch guard.level() { // роутер занят — проверяем реже (п. 9)
		case 1:
			interval *= 2
		case 2:
			if interval < time.Minute {
				interval = time.Minute
			}
		}
		due := time.Since(time.Unix(w.lastRound, 0)) >= interval
		active := false
		for _, g := range w.groups {
			if g.Enabled && len(g.Servers) > 0 {
				active = true
			}
		}
		paused := time.Now().Unix() < w.pauseUntil
		w.mu.Unlock()
		if active && due && !paused {
			a.watchRound(false)
		}
		select {
		case <-w.kick:
			a.watchRound(true)
		case <-time.After(2 * time.Second):
		}
	}
}

func (a *app) watchRound(forceSpeed bool) {
	w := a.watch
	a.mu.Lock()
	desiredCopy := map[string]string{}
	for k, v := range a.desired {
		desiredCopy[k] = v
	}
	a.mu.Unlock()
	w.mu.Lock()
	if w.running {
		w.mu.Unlock()
		return
	}
	w.running = true
	st := w.settings
	tagSet := map[string]bool{}
	var tags []string
	for _, g := range w.groups {
		if !g.Enabled {
			continue
		}
		for _, s := range g.Servers {
			if !tagSet[s] {
				tagSet[s] = true
				tags = append(tags, s)
			}
		}
	}
	// какие серверы сейчас активны (выбраны в группах со сторожем)
	activeSet := map[string]bool{}
	for g, t := range desiredCopy {
		if wg := w.groups[g]; wg != nil && wg.Enabled && t != "" {
			activeSet[t] = true
		}
	}
	nowT := time.Now().Unix()
	// экономия: активный проверяем каждый заход, запасные — раз в ReserveEverySec.
	// Если с активным что-то не так (или его нет в списке) — проверяем всех сразу,
	// чтобы решение о переключении принималось по свежим данным.
	checkAll := forceSpeed || len(activeSet) == 0 || w.allDown["*"]
	for t := range activeSet {
		if h := w.health[t]; h == nil || !h.OK || !tagSet[t] {
			checkAll = true
		}
	}
	if !checkAll {
		var sel []string
		for _, t := range tags {
			h := w.health[t]
			if activeSet[t] || h == nil || h.T == 0 || nowT-h.T >= int64(st.ReserveEverySec)-3 {
				sel = append(sel, t)
			}
		}
		tags = sel
		tagSet = map[string]bool{}
		for _, t := range tags {
			tagSet[t] = true
		}
	}
	var speedTags []string
	if st.SpeedURL != "" {
		for _, t := range tags { // активные — первыми
			if activeSet[t] {
				speedTags = append(speedTags, t)
			}
		}
		for _, t := range tags {
			if !activeSet[t] {
				speedTags = append(speedTags, t)
			}
		}
		var due []string
		for _, t := range speedTags {
			if !tagSet[t] {
				continue
			}
			every := int64(st.SpeedEverySec)
			if activeSet[t] {
				every = int64(st.SpeedActiveSec)
			}
			h := w.health[t]
			if forceSpeed || h == nil || h.SpeedT == 0 || nowT-h.SpeedT >= every-2 {
				due = append(due, t)
			}
		}
		speedTags = due
	}
	doSpeed := len(speedTags) > 0
	w.mu.Unlock()
	defer func() {
		w.mu.Lock()
		w.running = false
		w.lastRound = time.Now().Unix()
		w.mu.Unlock()
	}()
	if len(tags) == 0 {
		return
	}

	c := a.reload()
	ports, err := a.wprobe.ensure(c, a.watchProbeTags())
	if err != nil {
		w.mu.Lock()
		w.addEvent("", "warn", "не удалось запустить тестовый xray: "+err.Error())
		w.mu.Unlock()
		return
	}
	timeout := time.Duration(st.TimeoutMs) * time.Millisecond

	// 1) проверка сервисов — все серверы и все адреса параллельно
	type res struct {
		tag string
		m   map[string]targetRes
	}
	out := make(chan res, len(tags))
	for _, tag := range tags {
		go func(tag string) {
			m := map[string]targetRes{}
			port, ok := ports[tag]
			if !ok {
				for _, t := range st.Targets {
					m[t.Name] = targetRes{Err: "сервера нет в конфиге"}
				}
				out <- res{tag, m}
				return
			}
			var mu sync.Mutex
			var wg sync.WaitGroup
			for _, t := range st.Targets {
				wg.Add(1)
				go func(t watchTarget) {
					defer wg.Done()
					checkSem <- struct{}{} // не больше 12 запросов одновременно — роутер не захлёбывается
					defer func() { <-checkSem }()
					r := checkURL(port, t.URL, timeout)
					mu.Lock()
					m[t.Name] = r
					mu.Unlock()
				}(t)
			}
			wg.Wait()
			out <- res{tag, m}
		}(tag)
	}
	results := map[string]map[string]targetRes{}
	for range tags {
		r := <-out
		results[r.tag] = r.m
	}

	// 2) скорость — по очереди, чтобы замеры не мешали друг другу
	speeds := map[string][2]any{}
	if doSpeed {
		for _, tag := range speedTags {
			port, ok := ports[tag]
			if !ok {
				continue
			}
			kbps, err := speedTest(port, st.SpeedURL, 8*time.Second)
			e := ""
			if err != nil {
				e = err.Error()
			}
			speeds[tag] = [2]any{kbps, e}
		}
	}

	now := time.Now().Unix()
	a.wprobe.mu.Lock()
	a.wprobe.lastUse = time.Now()
	a.wprobe.mu.Unlock()

	// 3) итоги по серверам
	w.mu.Lock()
	if doSpeed {
		w.lastSpeed = now
	}
	anyOK := false
	for _, tag := range tags {
		h := w.health[tag]
		if h == nil {
			h = &srvHealth{}
			w.health[tag] = h
		}
		h.Targets = results[tag]
		h.T = now
		wc := wcheck{T: now, OK: true}
		msSum, msN := 0, 0
		for _, t := range st.Targets {
			if r := h.Targets[t.Name]; r.OK {
				msSum += int(r.Ms)
				msN++
			} else {
				wc.OK = false
			}
		}
		if msN > 0 {
			wc.Ms = int32(msSum / msN)
		}
		if sp, ok := speeds[tag]; ok {
			h.SpeedKbps, h.SpeedErr, h.SpeedT = sp[0].(int), sp[1].(string), now
			wc.HasSpd, wc.SpdOK, wc.Kbps = true, h.SpeedErr == "", int32(h.SpeedKbps)
		}
		if !(len(tags) >= 2 && !anyServiceOK(results)) { // без интернета у роутера — не в статистику
			w.recordWatchLocked(tag, wc)
		}
		var bad []string
		for _, t := range st.Targets {
			if r := h.Targets[t.Name]; !r.OK {
				bad = append(bad, t.Name+" ("+r.Err+")")
			}
		}
		if st.SpeedURL != "" && h.SpeedT > 0 {
			if h.SpeedErr != "" {
				bad = append(bad, "скорость ("+h.SpeedErr+")")
			} else if st.SpeedMinKbps > 0 && h.SpeedKbps < st.SpeedMinKbps {
				bad = append(bad, fmt.Sprintf("скорость %.1f Мбит/с < %.1f", float64(h.SpeedKbps)/1000, float64(st.SpeedMinKbps)/1000))
			}
		}
		h.Checks++
		if len(bad) == 0 {
			if !h.OK || h.OkSince == 0 {
				h.OkSince = now
			}
			h.OK, h.Reason, h.ConsecFail = true, "", 0
			h.ConsecOK++
			anyOK = true
		} else {
			h.OK, h.Reason, h.OkSince = false, strings.Join(bad, ", "), 0
			h.ConsecFail++
			h.ConsecOK = 0
			h.Fails++
		}
	}
	w.mu.Unlock()

	// если не ответил вообще никто — скорее всего пропал интернет у роутера, не дёргаемся
	if !anyOK && len(tags) >= 2 {
		allTargetsDead := true
		for _, m := range results {
			for _, r := range m {
				if r.OK {
					allTargetsDead = false
				}
			}
		}
		if allTargetsDead && a.ispOK() {
			// напрямую интернет есть, а через VPN — ничего: провайдер режет серверы списка
			w.mu.Lock()
			var gs []string
			for gt, g := range w.groups {
				if g.Enabled {
					gs = append(gs, gt)
				}
			}
			w.mu.Unlock()
			for _, gt := range gs {
				a.startEmergency(gt)
			}
			return
		}
		if allTargetsDead {
			w.mu.Lock()
			if !w.allDown["*"] {
				w.addEvent("", "warn", "ни один сервер не открыл ни один сервис — похоже, пропал интернет у роутера; переключения отложены")
				w.allDown["*"] = true
			}
			w.mu.Unlock()
			return
		}
	}
	w.mu.Lock()
	if w.allDown["*"] {
		w.addEvent("", "recover", "интернет снова есть, проверки идут нормально")
		delete(w.allDown, "*")
	}
	w.mu.Unlock()

	a.watchDecide()
	w.mu.Lock()
	bc := w.bestChanged
	w.bestChanged = false
	w.mu.Unlock()
	if bc {
		a.mu.Lock()
		a.saveStateLocked()
		a.mu.Unlock()
	}
	w.mu.Lock()
	needSave := time.Now().Unix()-w.lastHistSave >= 300
	if needSave {
		w.lastHistSave = time.Now().Unix()
	}
	w.mu.Unlock()
	if needSave {
		a.saveWatchHistory()
	}
}

// watchDecide решает, нужно ли переключать группы.
func (a *app) watchDecide() {
	w := a.watch
	now := time.Now().Unix()
	type sw struct {
		group, target, why string
		failure            bool
	}
	var todo []sw
	var emerg []string
	defer func() {
		for _, g := range emerg {
			a.startEmergency(g)
		}
	}()

	a.mu.Lock()
	desired := map[string]string{}
	for k, v := range a.desired {
		desired[k] = v
	}
	a.mu.Unlock()

	w.mu.Lock()
	st := w.settings
	fresh := int64(st.ReserveEverySec*2 + 30)
	healthy := func(t string) bool { h := w.health[t]; return h != nil && h.OK && h.T > 0 && now-h.T <= fresh }
	for gt, cur := range desired { // п. 29: часы работы основным
		if g := w.groups[gt]; g != nil && g.Enabled {
			w.noteActiveLocked(cur, now)
			break
		}
	}
	for gtag, g := range w.groups {
		if !g.Enabled || len(g.Servers) == 0 {
			continue
		}
		cur := desired[gtag]
		inList := -1
		for i, s := range g.Servers {
			if s == cur {
				inList = i
			}
		}
		// сначала — стабильные (здоровы 2+ проверки подряд), по приоритету; потом любые здоровые
		pickHealthy := func(except string) string {
			if !st.NoSmartSpeed { // из стабильных здоровых — с наибольшими баллами
				sc := w.scoresLocked(scoreWindow)
				for _, pool := range g.tiers() { // основные → запасные → кандидаты
					best, bs := "", -1
					for _, s := range pool {
						if v := sc[s].Score - w.hourPenaltyLocked(s, now); s != except && healthy(s) && w.health[s].ConsecOK >= 2 && v > bs {
							best, bs = s, v
						}
					}
					if best != "" {
						return best
					}
				}
			}
			for _, s := range g.Servers {
				if s != except && healthy(s) && w.health[s].ConsecOK >= 2 {
					return s
				}
			}
			for _, s := range g.Servers {
				if s != except && healthy(s) {
					return s
				}
			}
			return ""
		}
		if inList < 0 {
			// активен сервер не из списка (или «Авто») — ставим лучший из списка
			if t := pickHealthy(""); t != "" {
				todo = append(todo, sw{gtag, t, "сторож включён — беру первый здоровый из списка", false})
			} else {
				if !w.allDown[gtag] {
					w.addEvent(gtag, "warn", "в списке нет ни одного здорового сервера — ищу рабочий среди остальных")
					w.allDown[gtag] = true
				}
				emerg = append(emerg, gtag)
			}
			continue
		}
		h := w.health[cur]
		if h != nil && h.ConsecFail >= st.FailsToSwitch {
			if t := pickHealthy(cur); t != "" {
				delete(w.allDown, gtag)
				todo = append(todo, sw{gtag, t, fmt.Sprintf("%s не справляется %d раз подряд: %s", cur, h.ConsecFail, h.Reason), true})
			} else {
				if !w.allDown[gtag] {
					w.addEvent(gtag, "warn", fmt.Sprintf("%s не справляется (%s), и остальные из списка тоже — ищу рабочий среди всех серверов", cur, h.Reason))
					w.allDown[gtag] = true
				}
				emerg = append(emerg, gtag)
			}
			continue
		}
		if h != nil && h.OK && w.allDown[gtag] {
			w.addEvent(gtag, "recover", cur+" снова работает нормально")
			delete(w.allDown, gtag)
		}
		justStarted := now < w.freshUntil
		if !st.NoSmartSpeed {
			// умный выбор: держимся сервера с наибольшими баллами (за scoreWindow — те же баллы, что на карточках)
			sc := w.scoresLocked(scoreWindow)
			best, bestSc := "", -1
			for _, pool := range g.tiers() { // берём лучший из самого высокого уровня, где есть здоровые
				for _, s := range pool {
					if !healthy(s) || w.health[s].ConsecOK < 2 || !sc[s].Eligible {
						continue
					}
					if v := sc[s].Score - w.hourPenaltyLocked(s, now); v > bestSc {
						best, bestSc = s, v
					}
				}
				if best != "" {
					break
				}
			}
			if best != "" && best != g.Best {
				g.Best = best
				w.bestChanged = true
			}
			curSc := sc[cur].Score - w.hourPenaltyLocked(cur, now)
			if best != "" && best != cur && (bestSc >= curSc+5 || !sc[cur].Eligible) {
				hb := w.health[best]
				settled := now-hb.OkSince >= 300 || justStarted
				calm := now-w.lastSmart >= 1200 || justStarted
				if settled && calm {
					if w.smartWaitSince == 0 || w.smartWaitEv != best {
						w.smartWaitSince, w.smartWaitEv = now, best
					}
					// переключаемся, когда интернетом почти не пользуются; если тихо так и не стало — через 10 минут всё равно.
					// Но если идёт игра или звонок через VPN — ждём, пока закончится (сервер ведь работает, просто хуже). п. 21
					rt, rtWho := a.realtimeActive()
					if rt && !justStarted {
						if w.rtNoted != best {
							w.rtNoted = best
							w.addEvent(gtag, "info", "есть сервер получше ("+best+"), но идёт игра или звонок ("+rtWho+") — переключу, когда закончится")
						}
					} else if justStarted || a.quietNow(st.SmartQuietKb) || now-w.smartWaitSince >= 600 {
						w.rtNoted = ""
						w.lastSmart, w.smartWaitSince, w.smartWaitEv = now, 0, ""
						why := fmt.Sprintf("%s лучше по баллам (%d против %d)", best, bestSc, curSc)
						if justStarted {
							why = "после запуска — сразу на лучший по баллам (" + strconv.Itoa(bestSc) + ")"
						}
						todo = append(todo, sw{gtag, best, why, false})
					}
				}
			} else {
				w.smartWaitSince, w.smartWaitEv = 0, ""
			}
			continue
		}
		// умный выбор выключен — старое правило: возврат на более приоритетный по порядку списка
		if st.ReturnToFirst {
			for i := 0; i < inList; i++ {
				s := g.Servers[i]
				if rt, _ := a.realtimeActive(); rt {
					break // игра/звонок — возврат подождёт
				}
				if hh := w.health[s]; healthy(s) && hh.OkSince > 0 && now-hh.OkSince >= int64(st.ReturnAfterSec) {
					todo = append(todo, sw{gtag, s, fmt.Sprintf("%s снова стабилен %d мин — возвращаюсь на более приоритетный", s, (now-hh.OkSince)/60), false})
					break
				}
			}
		}
	}
	w.mu.Unlock()

	needRestart := false
	for _, x := range todo {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		err := a.api.OverrideBalancer(ctx, x.group, x.target)
		cancel()
		w.mu.Lock()
		if err != nil {
			w.addEvent(x.group, "warn", "не удалось переключить на "+x.target+": "+err.Error())
		} else {
			from := desired[x.group]
			if g := w.groups[x.group]; g != nil && x.failure && g.Best == from {
				g.Best = "" // упавший сервер больше не «лучший» (иначе после перезапуска Xray вернёмся на него)
			}
			short := "🔄 " + shortSrv(x.target, from)
			if from != "" && from != x.target {
				if x.failure {
					why := ""
					if h := w.health[from]; h != nil {
						var bad []string
						for n, r := range h.Targets {
							if !r.OK {
								bad = append(bad, n)
							}
						}
						sort.Strings(bad)
						if len(bad) > 0 {
							why = " (не открывались: " + strings.Join(firstN(bad, 3), ", ") + ")"
						}
					}
					short = "❌ " + shortSrv(from, x.target) + " не работает → ✅ " + shortSrv(x.target, from) + why
				} else {
					short = "🔄 " + shortSrv(from, x.target) + " → " + shortSrv(x.target, from) + " (лучше по баллам)"
				}
			}
			w.addEventShort(x.group, "switch", x.why+" → "+x.target, short)
		}
		w.mu.Unlock()
		if err == nil {
			a.mu.Lock()
			a.desired[x.group] = x.target
			a.saveStateLocked()
			a.mu.Unlock()
			if x.failure {
				needRestart = true
			}
		}
	}
	if needRestart {
		a.maybeRestartAfterSwitch()
	}
}

// maybeRestartAfterSwitch: после переключения из-за сбоя перезапускает XKeen,
// чтобы оборвать «зависшие» соединения через старый сервер — приложения
// переподключатся уже через новый. Не чаще, чем раз в RestartCooldownMin.
func (a *app) maybeRestartAfterSwitch() {
	w := a.watch
	w.mu.Lock()
	st := w.settings
	now := time.Now().Unix()
	if !st.RestartOnSwitch {
		w.mu.Unlock()
		return
	}
	if w.lastRestart > 0 && now-w.lastRestart < int64(st.RestartCooldownMin*60) {
		w.addEvent("", "info", fmt.Sprintf("перезапуск XKeen пропущен: предыдущий был %d мин назад (пауза между перезапусками %d мин)", (now-w.lastRestart)/60, st.RestartCooldownMin))
		w.mu.Unlock()
		return
	}
	w.lastRestart = now
	w.pauseUntil = now + 40
	w.addEvent("", "restart", "перезапускаю XKeen, чтобы приложения переподключились через новый сервер (связь пропадёт на 5–10 с)")
	w.mu.Unlock()
	go func() {
		err := a.restartXray()
		for _, d := range []time.Duration{2, 4, 8} { // вернуть выбор сторожа как можно быстрее
			time.Sleep(d * time.Second)
			a.applyOnce()
		}
		w.mu.Lock()
		if err != nil {
			w.addEvent("", "warn", "не удалось перезапустить XKeen: "+err.Error())
		} else {
			w.addEvent("", "info", "XKeen перезапущен")
		}
		w.mu.Unlock()
	}()
}

// manualSelected: пользователь сам выбрал сервер. Если он не из списка сторожа
// (или выбрал «Авто») — сторож выключается, чтобы не спорить с человеком.
func (a *app) manualSelected(group, target string) {
	w := a.watch
	w.mu.Lock()
	defer w.mu.Unlock()
	g := w.groups[group]
	if g == nil || !g.Enabled {
		return
	}
	for _, s := range g.Servers {
		if s == target {
			w.addEvent(group, "info", "вручную выбран "+target+" — сторож продолжает следить")
			return
		}
	}
	g.Enabled = false
	if target == "" {
		w.addEvent(group, "info", "выбран режим «Авто» — сторож выключен")
	} else {
		w.addEvent(group, "info", "вручную выбран сервер не из списка ("+target+") — сторож выключен")
	}
}

// ---- HTTP ----

func (a *app) watchSnapshot() map[string]any {
	w := a.watch
	w.mu.Lock()
	defer w.mu.Unlock()
	health := map[string]srvHealth{}
	for k, v := range w.health {
		health[k] = *v
	}
	groups := map[string]watchGroup{}
	for k, v := range w.groups {
		groups[k] = *v
	}
	ev := make([]watchEvent, len(w.events))
	copy(ev, w.events)
	ws := w.settings
	ws.TgToken = maskToken(ws.TgToken)
	ws.PassHash, ws.PassSalt = "", ""
	return map[string]any{
		"settings":    ws,
		"realtime":    rtWhoNow(),
		"groups":      groups,
		"health":      health,
		"events":      ev,
		"lastRound":   w.lastRound,
		"lastSpeed":   w.lastSpeed,
		"lastAutoTop": w.lastAutoTop,
		"lastReadEv":  w.lastReadEv,
		"running":     w.running,
	}
}

func (a *app) handleWatch(rw http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeJSON(rw, a.watchSnapshot())
		return
	}
	var req struct {
		Group    string         `json:"group"`
		Enabled  *bool          `json:"enabled"`
		Servers  []string       `json:"servers"`
		Settings *watchSettings `json:"settings"`
		CheckNow bool           `json:"checkNow"`
		SyncNow  bool           `json:"syncNow"`
		Auto     *bool          `json:"auto"`
		Pin      string         `json:"pin"`
		Unpin    string         `json:"unpin"`
		AddCand  []string       `json:"addCand"` // добавить на проверку (кандидатами)
		Lock     string         `json:"lock"`
		Unlock   string         `json:"unlock"`
		ResetEx  bool           `json:"resetExcluded"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		http.Error(rw, "bad request", 400)
		return
	}
	w := a.watch
	w.mu.Lock()
	if req.Settings != nil {
		s := *req.Settings
		s.TgToken, s.TgChatID = w.settings.TgToken, w.settings.TgChatID // токен меняется только через /api/telegram
		s.TgAdminID, s.TgAdminUID, s.TgAdminName, s.TgPinID = w.settings.TgAdminID, w.settings.TgAdminUID, w.settings.TgAdminName, w.settings.TgPinID
		s.PassHash, s.PassSalt = w.settings.PassHash, w.settings.PassSalt
		o := w.settings
		s.SubUp, s.SubDown, s.SubTotal, s.SubExpire, s.SubChecked, s.SubApplied, s.SubErr, s.SubWarned = o.SubUp, o.SubDown, o.SubTotal, o.SubExpire, o.SubChecked, o.SubApplied, o.SubErr, o.SubWarned
		s.SubTitle, s.SubSupport, s.SubWebPage, s.SubUpdateH, s.SubInformers, s.SubInfExpire, s.SubInfLeft, s.SubPaidUntil = o.SubTitle, o.SubSupport, o.SubWebPage, o.SubUpdateH, o.SubInformers, o.SubInfExpire, o.SubInfLeft, o.SubPaidUntil
		s.WizardDone = s.WizardDone || o.WizardDone
		s.Ver = 2
		if s.SpeedActiveSec == 0 {
			s.SpeedActiveSec = 30
		}
		s.normalize()
		w.settings = s
		w.addEvent("", "info", "настройки сторожа изменены")
	}
	if req.Group != "" {
		g := w.groups[req.Group]
		if g == nil {
			g = &watchGroup{}
			w.groups[req.Group] = g
		}
		if req.Servers != nil {
			seen := map[string]bool{}
			var list []string
			for _, s := range req.Servers {
				if s != "" && !seen[s] {
					seen[s] = true
					list = append(list, s)
				}
			}
			if g.Auto { // при автоподборе: убранные вручную — в исключения, добавленные — закрепить
				old := map[string]bool{}
				for _, s := range g.Servers {
					old[s] = true
				}
				for _, s := range g.Servers {
					if !seen[s] {
						g.Excluded = addUniq(g.Excluded, s)
						g.Pinned = removeStr(g.Pinned, s)
					}
				}
				for _, s := range list {
					if !old[s] {
						g.Pinned = addUniq(g.Pinned, s)
						g.Excluded = removeStr(g.Excluded, s)
					}
				}
			}
			g.Servers = list
		}
		if req.Pin != "" {
			g.Pinned = addUniq(g.Pinned, req.Pin)
			g.Excluded = removeStr(g.Excluded, req.Pin)
			delete(g.Trials, req.Pin)
			delete(g.Passed, req.Pin)
			g.Reserve = removeStr(g.Reserve, req.Pin)
			// стабильные — в начало списка (порядок закрепления)
			st := g.stable()
			var rest []string
			for _, x := range g.Servers {
				if !contains(st, x) {
					rest = append(rest, x)
				}
			}
			g.Servers = append(st, rest...)
		}
		if req.Unpin != "" {
			g.Pinned = removeStr(g.Pinned, req.Unpin)
			g.Locked = removeStr(g.Locked, req.Unpin)
		}
		if len(req.AddCand) > 0 {
			if g.Trials == nil {
				g.Trials = map[string]int64{}
			}
			now := time.Now().Unix()
			for _, t := range req.AddCand {
				if t == "" || contains(g.Servers, t) {
					continue
				}
				g.Servers = append(g.Servers, t)
				g.Excluded = removeStr(g.Excluded, t)
				delete(g.Tried, t)
				g.Trials[t] = now
				if g.ManualCand == nil {
					g.ManualCand = map[string]int64{}
				}
				g.ManualCand[t] = now
			}
			w.addEvent(req.Group, "info", fmt.Sprintf("вручную добавлено на проверку: %d", len(req.AddCand)))
		}
		if req.Lock != "" {
			g.Locked = addUniq(g.Locked, req.Lock)
			g.Pinned = addUniq(g.Pinned, req.Lock)
			g.Reserve = removeStr(g.Reserve, req.Lock)
			delete(g.Trials, req.Lock)
		}
		if req.Unlock != "" {
			g.Locked = removeStr(g.Locked, req.Unlock)
		}
		if req.ResetEx {
			g.Excluded = nil
		}
		if req.Auto != nil && *req.Auto != g.Auto {
			g.Auto = *req.Auto
			if g.Auto {
				w.addEvent(req.Group, "info", fmt.Sprintf("автокандидаты включены: %d мест под кандидатов с лучшим пингом, проверка %d мин, проходной балл %d", w.settings.CandSlots, w.settings.TrialMin, w.settings.PassScore))
				if len(g.Pinned) == 0 { // всё, что уже было в списке, считаем стабильным
					g.Pinned = append([]string(nil), g.Servers...)
				}
			} else {
				w.addEvent(req.Group, "info", "автокандидаты выключены — список больше не меняется сам")
			}
		}
		if req.Enabled != nil && *req.Enabled != g.Enabled {
			g.Enabled = *req.Enabled
			if g.Enabled {
				w.addEvent(req.Group, "info", fmt.Sprintf("сторож включён, серверов в списке: %d", len(g.Servers)))
			} else {
				w.addEvent(req.Group, "info", "сторож выключен")
			}
			delete(w.allDown, req.Group)
		}
	}
	var syncList []string
	if req.Group != "" && (req.SyncNow || (req.Servers != nil && !w.settings.NoBalancerSync)) {
		if g := w.groups[req.Group]; g != nil {
			syncList = g.syncList()
		}
	}
	w.mu.Unlock()
	a.mu.Lock()
	a.saveStateLocked()
	a.mu.Unlock()
	var syncErr string
	if len(syncList) > 0 {
		res, err := syncBalancer(*flagConfDir, req.Group, syncList)
		w.mu.Lock()
		if err != nil {
			syncErr = err.Error()
			w.addEvent(req.Group, "info", "не удалось обновить группу в конфиге Xray: "+err.Error())
		} else if res.Changed {
			w.addEvent(req.Group, "info", fmt.Sprintf("состав группы в %s обновлён по списку сторожа (%d серв.) — применится после перезапуска Xray", filepath.Base(res.File), len(res.Members)))
		}
		w.mu.Unlock()
	}
	if syncErr != "" && req.SyncNow {
		rw.WriteHeader(400)
		writeJSON(rw, map[string]any{"error": syncErr})
		return
	}
	if (req.Auto != nil && *req.Auto) || req.ResetEx || req.Unpin != "" || req.Pin != "" || (req.Settings != nil) {
		go a.candOnce(req.Group)
	}
	if req.CheckNow || len(req.AddCand) > 0 || (req.Enabled != nil && *req.Enabled) {
		select {
		case w.kick <- struct{}{}:
		default:
		}
	}
	writeJSON(rw, map[string]any{"ok": true})
}

func addUniq(l []string, s string) []string {
	for _, x := range l {
		if x == s {
			return l
		}
	}
	return append(l, s)
}

func removeStr(l []string, s string) []string {
	var out []string
	for _, x := range l {
		if x != s {
			out = append(out, x)
		}
	}
	return out
}

func anyServiceOK(results map[string]map[string]targetRes) bool {
	for _, m := range results {
		for _, r := range m {
			if r.OK {
				return true
			}
		}
	}
	return false
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

var checkSem = make(chan struct{}, 12)

func rtWhoNow() string {
	if time.Now().Unix() < rtUntil.Load() {
		w, _ := rtWho.Load().(string)
		return w
	}
	return ""
}
