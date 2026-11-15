package main

// 2.3, п. 6, 8.4, 13, 15: каталог сервисов для раздела «Правила» и мастера.
// Встроенный список (работает без интернета). Если задан catalogURL — раз в сутки
// панель скачивает свежий каталог через рабочий VPN-сервер, иначе напрямую,
// а если не вышло — остаётся на встроенном (п. 13).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type svcDef struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	Group  string   `json:"group"` // popular | social | ai | media | work | games | other | novpn
	Domain []string `json:"domain"`
	IP     []string `json:"ip,omitempty"`
	Hint   string   `json:"hint,omitempty"`
}

type svcGroup struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

var svcGroups = []svcGroup{
	{"popular", "Популярные"}, {"social", "Соцсети и мессенджеры"}, {"ai", "Нейросети"}, {"media", "Видео и музыка"},
	{"work", "Работа"}, {"games", "Игры"}, {"other", "Прочее"}, {"novpn", "Без VPN — через вашего провайдера"},
}

func d(l ...string) []string {
	out := make([]string, len(l))
	for i, x := range l {
		out[i] = "domain:" + x
	}
	return out
}

var builtinCatalog = []svcDef{
	// ---- популярные ----
	{ID: "youtube", Name: "YouTube", Group: "popular", Domain: d("youtube.com", "youtu.be", "ytimg.com", "googlevideo.com", "youtube-nocookie.com", "youtubei.googleapis.com", "yt.be", "ggpht.com")},
	{ID: "instagram", Name: "Instagram", Group: "popular", Domain: d("instagram.com", "cdninstagram.com", "ig.me", "instagr.am", "igcdn.com", "igsonar.com")},
	{ID: "whatsapp", Name: "WhatsApp", Group: "popular", Domain: d("whatsapp.com", "whatsapp.net", "wa.me", "whatsapp.biz")},
	{ID: "telegram", Name: "Telegram", Group: "popular", Domain: d("telegram.org", "t.me", "telegram.me", "telegra.ph", "telesco.pe", "tdesktop.com", "telegram-cdn.org", "cdn-telegram.org"),
		IP: []string{"91.105.192.0/23", "91.108.4.0/22", "91.108.8.0/22", "91.108.12.0/22", "91.108.16.0/22", "91.108.20.0/22", "91.108.56.0/22", "149.154.160.0/20", "95.161.64.0/20", "185.76.151.0/24", "2001:67c:4e8::/48", "2001:b28:f23c::/47", "2001:b28:f23f::/48"}},
	{ID: "facebook", Name: "Facebook", Group: "popular", Domain: d("facebook.com", "fbcdn.net", "fb.com", "fb.me", "facebook.net", "messenger.com", "fbsbx.com"),
		Hint: "плюс адреса Meta — нужны и для Instagram и WhatsApp", IP: []string{"31.13.24.0/21", "31.13.64.0/18", "57.144.0.0/14", "66.220.144.0/20", "69.63.176.0/20", "69.171.224.0/19", "129.134.0.0/16", "157.240.0.0/16", "163.70.128.0/17", "173.252.64.0/18", "179.60.192.0/22", "185.60.216.0/22", "204.15.20.0/22", "2a03:2880::/32"}},
	{ID: "chatgpt", Name: "ChatGPT", Group: "popular", Domain: d("openai.com", "chatgpt.com", "oaistatic.com", "oaiusercontent.com", "sora.com", "chat.com")},
	{ID: "claude", Name: "Claude", Group: "popular", Domain: d("claude.ai", "anthropic.com", "claude.com", "claudeusercontent.com")},
	{ID: "gemini", Name: "Gemini", Group: "popular", Domain: d("gemini.google.com", "bard.google.com", "generativelanguage.googleapis.com", "alkalimakersuite-pa.clients6.google.com", "gemini.google", "ai.google.dev", "alkalicore-pa.clients6.google.com", "proactivebackend-pa.googleapis.com", "robinfrontend-pa.googleapis.com", "geller-pa.googleapis.com", "aisandbox-pa.googleapis.com", "labs.google", "deepmind.google", "deepmind.com")},
	{ID: "x", Name: "X (Twitter)", Group: "popular", Domain: d("x.com", "twitter.com", "twimg.com", "t.co", "x.ai")},
	{ID: "discord", Name: "Discord", Group: "popular", Domain: d("discord.com", "discord.gg", "discordapp.com", "discordapp.net", "discord.media", "discordcdn.com", "discord.dev")},
	{ID: "tiktok", Name: "TikTok", Group: "popular", Domain: d("tiktok.com", "tiktokv.com", "tiktokcdn.com", "byteoversea.com", "ibytedtos.com", "muscdn.com", "tiktokcdn-us.com", "tiktokv.us", "tiktokw.us", "ttwstatic.com", "ibyteimg.com", "ipstatp.com", "musical.ly")},
	{ID: "spotify", Name: "Spotify", Group: "popular", Domain: d("spotify.com", "scdn.co", "spotifycdn.com", "spoti.fi", "pscdn.co", "spotify.design")},
	// ---- соцсети ----
	{ID: "threads", Name: "Threads", Group: "social", Domain: d("threads.net", "threads.com")},
	{ID: "linkedin", Name: "LinkedIn", Group: "social", Domain: d("linkedin.com", "licdn.com", "lnkd.in")},
	{ID: "signal", Name: "Signal", Group: "social", Domain: d("signal.org", "whispersystems.org", "signal.art", "signal.group")},
	{ID: "viber", Name: "Viber", Group: "social", Domain: d("viber.com")},
	{ID: "snapchat", Name: "Snapchat", Group: "social", Domain: d("snapchat.com", "snap.com", "sc-cdn.net", "snapkit.com")},
	{ID: "pinterest", Name: "Pinterest", Group: "social", Domain: d("pinterest.com", "pinimg.com", "pin.it")},
	{ID: "reddit", Name: "Reddit", Group: "social", Domain: d("reddit.com", "redd.it", "redditmedia.com", "redditstatic.com")},
	{ID: "tumblr", Name: "Tumblr", Group: "social", Domain: d("tumblr.com")},
	{ID: "twitch", Name: "Twitch", Group: "social", Domain: d("twitch.tv", "ttvnw.net", "jtvnw.net", "twitchcdn.net", "twitchsvc.net")},
	// ---- нейросети ----
	{ID: "perplexity", Name: "Perplexity", Group: "ai", Domain: d("perplexity.ai", "pplx.ai")},
	{ID: "grok", Name: "Grok", Group: "ai", Domain: d("grok.com", "x.ai")},
	{ID: "copilot", Name: "Copilot", Group: "ai", Domain: d("copilot.microsoft.com", "githubcopilot.com", "sydney.bing.com")},
	{ID: "midjourney", Name: "Midjourney", Group: "ai", Domain: d("midjourney.com")},
	{ID: "deepseek", Name: "DeepSeek", Group: "ai", Domain: d("deepseek.com")},
	{ID: "aistudio", Name: "NotebookLM / AI Studio", Group: "ai", Domain: d("notebooklm.google.com", "notebooklm.google", "aistudio.google.com", "makersuite.google.com")},
	{ID: "huggingface", Name: "Hugging Face", Group: "ai", Domain: d("huggingface.co", "hf.co")},
	// ---- видео и музыка ----
	{ID: "netflix", Name: "Netflix", Group: "media", Domain: d("netflix.com", "nflxvideo.net", "nflximg.net", "nflxext.com", "nflxso.net")},
	{ID: "disney", Name: "Disney+", Group: "media", Domain: d("disneyplus.com", "disney-plus.net", "dssott.com", "bamgrid.com")},
	{ID: "kick", Name: "Kick", Group: "media", Domain: d("kick.com")},
	{ID: "soundcloud", Name: "SoundCloud", Group: "media", Domain: d("soundcloud.com", "sndcdn.com")},
	{ID: "deezer", Name: "Deezer", Group: "media", Domain: d("deezer.com", "dzcdn.net")},
	{ID: "applemusic", Name: "Apple Music", Group: "media", Domain: d("music.apple.com", "itunes.apple.com", "mzstatic.com")},
	{ID: "vimeo", Name: "Vimeo", Group: "media", Domain: d("vimeo.com", "vimeocdn.com")},
	// ---- работа ----
	{ID: "github", Name: "GitHub", Group: "work", Domain: d("github.com", "githubusercontent.com", "githubassets.com", "github.io", "ghcr.io")},
	{ID: "notion", Name: "Notion", Group: "work", Domain: d("notion.so", "notion.site", "notion.com")},
	{ID: "figma", Name: "Figma", Group: "work", Domain: d("figma.com")},
	{ID: "canva", Name: "Canva", Group: "work", Domain: d("canva.com")},
	{ID: "slack", Name: "Slack", Group: "work", Domain: d("slack.com", "slack-edge.com", "slack-imgs.com")},
	{ID: "zoom", Name: "Zoom", Group: "work", Domain: d("zoom.us", "zoom.com")},
	{ID: "meet", Name: "Google Meet", Group: "work", Domain: d("meet.google.com")},
	{ID: "medium", Name: "Medium", Group: "work", Domain: d("medium.com")},
	{ID: "patreon", Name: "Patreon", Group: "work", Domain: d("patreon.com", "patreonusercontent.com")},
	{ID: "dropbox", Name: "Dropbox", Group: "work", Domain: d("dropbox.com", "dropboxapi.com", "dropboxusercontent.com", "db.tt")},
	// ---- игры ----
	{ID: "steam", Name: "Облако Steam", Group: "games", Hint: "магазин, сообщество, загрузки — не игровые серверы", Domain: d("steampowered.com", "steamcommunity.com", "steamstatic.com", "steamcontent.com", "steamusercontent.com")},
	{ID: "epic", Name: "Epic Games", Group: "games", Domain: d("epicgames.com", "unrealengine.com", "epicgames.dev")},
	{ID: "battlenet", Name: "Battle.net", Group: "games", Domain: d("battle.net", "blizzard.com", "blizzard.net", "bizstatic.cn", "biz-contentstack.com", "overwatch.com")},
	{ID: "roblox", Name: "Roblox", Group: "games", Domain: d("roblox.com", "rbxcdn.com", "rbx.com", "roblox-api.arkoselabs.com", "cdn.arkoselabs.com")},
	{ID: "xbox", Name: "Xbox Live", Group: "games", Domain: d("xboxlive.com", "xbox.com")},
	{ID: "psn", Name: "PlayStation Network", Group: "games", Domain: d("playstation.com", "playstation.net", "sonyentertainmentnetwork.com")},
	{ID: "nintendo", Name: "Nintendo", Group: "games", Domain: d("nintendo.com", "nintendo.net")},
	{ID: "modrinth", Name: "Modrinth", Group: "games", Domain: d("modrinth.com")},
	// ---- прочее ----
	{ID: "proton", Name: "Proton", Group: "other", Domain: d("proton.me", "protonmail.com", "protonvpn.com", "pm.me")},
	{ID: "speedtest", Name: "Speedtest", Group: "other", Domain: d("speedtest.net", "ookla.com")},
	{ID: "rutracker", Name: "rutracker", Group: "other", Domain: d("rutracker.org", "rutracker.cc", "rutracker.net", "t-ru.org")},
	{ID: "songsterr", Name: "Songsterr", Group: "other", Domain: d("songsterr.com")},
	{ID: "archive", Name: "Archive.org", Group: "other", Domain: d("archive.org")},
	{ID: "wikipedia", Name: "Wikipedia", Group: "other", Domain: d("wikipedia.org", "wikimedia.org")},
	{ID: "cloudflare", Name: "Cloudflare (сайты за ним)", Group: "other", Hint: "многие сайты работают через Cloudflare; через VPN — если такие сайты не открываются", IP: []string{"ext:geoip_v2fly.dat:cloudflare"}},
	// ---- без VPN ----
	{ID: "gosuslugi", Name: "Госуслуги", Group: "novpn", Domain: d("gosuslugi.ru", "esia.gosuslugi.ru", "gu-st.ru", "nalog.ru", "nalog.gov.ru", "mos.ru")},
	{ID: "sber", Name: "Сбер", Group: "novpn", Domain: d("sberbank.ru", "sber.ru", "sberbank.com", "online.sberbank.ru")},
	{ID: "tbank", Name: "Т-Банк", Group: "novpn", Domain: d("tbank.ru", "tinkoff.ru", "tinkoffinsurance.ru", "tcsbank.ru")},
	{ID: "alfa", Name: "Альфа-Банк", Group: "novpn", Domain: d("alfabank.ru", "alfa-bank.ru", "alfadirect.ru", "alfacapital.ru")},
	{ID: "vtb", Name: "ВТБ", Group: "novpn", Domain: d("vtb.ru", "online.vtb.ru")},
	{ID: "yandex", Name: "Яндекс", Group: "novpn", Domain: d("yandex.ru", "yandex.net", "ya.ru", "yastatic.net", "yandex.com", "kinopoisk.ru", "dzen.ru")},
	{ID: "vk", Name: "VK", Group: "novpn", Domain: d("vk.com", "vk.ru", "userapi.com", "vkuser.net", "mail.ru", "ok.ru", "vkvideo.ru", "vk-portal.net")},
	{ID: "ozon", Name: "Ozon", Group: "novpn", Domain: d("ozon.ru", "ozone.ru", "ozon.by", "ozon.kz")},
	{ID: "wb", Name: "Wildberries", Group: "novpn", Domain: d("wildberries.ru", "wb.ru", "wbstatic.net", "wbbasket.ru")},
	{ID: "avito", Name: "Авито", Group: "novpn", Domain: d("avito.ru", "avito.st")},
	{ID: "valvegame", Name: "Игровые серверы Steam/Valve", Group: "novpn", Hint: "матчи CS2/Dota 2 — с меньшим пингом напрямую", Domain: d("steamserver.net", "valve.net"),
		IP: []string{"155.133.224.0/19", "162.254.192.0/21", "185.25.180.0/22", "205.196.6.0/24"}},
}

var catalog struct {
	sync.Mutex
	list    []svcDef
	updated int64
	src     string
	via     string
	err     string
}

const catalogURL = "" // свежий каталог (raw-ссылка на GitHub) — появится после публикации

func catalogPath() string {
	return filepath.Join(filepath.Dir(*flagState), "xray-selector-catalog.json")
}

func svcCatalog() []svcDef {
	catalog.Lock()
	defer catalog.Unlock()
	if catalog.list == nil {
		catalog.list, catalog.src = builtinCatalog, "встроенный"
		if b, err := os.ReadFile(catalogPath()); err == nil {
			var l []svcDef
			if json.Unmarshal(b, &l) == nil && len(l) > 20 {
				catalog.list, catalog.src = l, "скачанный"
				if st, err := os.Stat(catalogPath()); err == nil {
					catalog.updated = st.ModTime().Unix()
				}
			}
		}
	}
	return catalog.list
}

func svcByID(id string) *svcDef {
	for _, s := range svcCatalog() {
		if s.ID == id {
			cp := s
			return &cp
		}
	}
	return nil
}

// catalogLoop — п. 13: обновить каталог через VPN → напрямую → остаться на встроенном.
func (a *app) catalogLoop() {
	time.Sleep(7 * time.Minute)
	for {
		if catalogURL != "" && !demoMode {
			guard.pace(10 * time.Minute)
			b, _, via, err := a.smartGet(catalogURL, "xray-selector/"+version, 2<<20, 30*time.Second)
			var l []svcDef
			if err == nil {
				if e := json.Unmarshal(b, &l); e != nil || len(l) < 20 {
					err = errString("каталог повреждён")
				}
			}
			catalog.Lock()
			if err != nil {
				catalog.err = err.Error()
			} else {
				os.WriteFile(catalogPath(), b, 0644)
				catalog.list, catalog.src, catalog.via, catalog.err, catalog.updated = l, "скачанный", via, "", time.Now().Unix()
			}
			catalog.Unlock()
		}
		time.Sleep(24 * time.Hour)
	}
}

type errString string

func (e errString) Error() string { return string(e) }

// matchSvc — к какому сервису относится запись правила (domain:x, full:x, x, geosite:x).
func matchSvc(entry string) string {
	e := strings.ToLower(strings.TrimSpace(entry))
	host := e
	for _, p := range []string{"domain:", "full:", "keyword:", "dotless:"} {
		host = strings.TrimPrefix(host, p)
	}
	if strings.HasPrefix(e, "geosite:") || strings.HasPrefix(e, "ext:") {
		cat := e[strings.LastIndex(e, ":")+1:]
		cat = strings.Split(cat, "@")[0]
		for _, s := range svcCatalog() {
			if s.ID == cat || strings.EqualFold(s.Name, cat) {
				return s.ID
			}
		}
		return ""
	}
	if strings.HasPrefix(e, "regexp:") {
		return ""
	}
	for _, s := range svcCatalog() {
		for _, x := range s.Domain {
			dm := strings.TrimPrefix(x, "domain:")
			if host == dm || strings.HasSuffix(host, "."+dm) {
				return s.ID
			}
		}
		for _, x := range s.IP {
			if host == x {
				return s.ID
			}
		}
	}
	return ""
}
