package main

// Типы устройств (значки) и автоопределение по имени/производителю; имена из
// списка клиентов Keenetic (то, как пользователь назвал устройство в роутере).

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

var devKindEmoji = map[string]string{
	"pc": "🖥", "laptop": "💻", "macbook": "💻", "imac": "🖥", "iphone": "📱", "android": "📱", "tablet": "📱", "watch": "⌚",
	"tv": "📺", "ps": "🎮", "xbox": "🎮", "switch": "🎮", "media": "📡", "speaker": "🔊", "camera": "📷", "intercom": "🔔",
	"printer": "🖨", "nas": "🗄", "iot": "🏠", "router": "📶", "car": "🚗", "vacuum": "🧹", "ebook": "📖", "other": "❔",
}

// guessKind — тип по названию/hostname и производителю.
func guessKind(text, vendor string) string {
	t := strings.ToLower(text + " " + vendor)
	has := func(ws ...string) bool {
		for _, w := range ws {
			if strings.Contains(t, w) {
				return true
			}
		}
		return false
	}
	switch {
	case has("iphone"):
		return "iphone"
	case has("ipad", "tablet", "планшет", "galaxy tab", "mipad", "matepad"):
		return "tablet"
	case has("watch", "часы", "band", "браслет"):
		return "watch"
	case has("macbook"):
		return "macbook"
	case has("imac", "mac mini", "mac-mini", "macmini", "mac studio"):
		return "imac"
	case has("playstation", "ps5", "ps4", "ps3", "sony interactive"):
		return "ps"
	case has("xbox"):
		return "xbox"
	case has("switch", "nintendo"):
		return "switch"
	case has("shield", "apple tv", "appletv", "mibox", "mi box", "chromecast", "android tv box", "tv box", "tvbox", "приставк", "fire tv", "roku", "kinopoisk", "яндекс модуль"):
		return "media"
	case has("tv", "телевизор", "bravia", "webos", "tizen", "smart-tv", "smarttv"):
		return "tv"
	case has("станция", "station", "alice", "алиса", "homepod", "echo", "колонк", "маруся", "sber boom", "капсула"):
		return "speaker"
	case has("camera", "камер", "cam", "ipc", "hikvision", "dahua", "ezviz", "imou"):
		return "camera"
	case has("домофон", "intercom", "doorbell", "domofon"):
		return "intercom"
	case has("keenetic", "netcraze", "router", "роутер", "точка", "mesh", "extender", "tp-link", "mikrotik"):
		return "router"
	case has("printer", "принтер", "canon", "epson", "laserjet", "brother", "kyocera", "mfp"):
		return "printer"
	case has("nas", "synology", "qnap", "server", "сервер", "raspberry"):
		return "nas"
	case has("aqara", "hub", "zigbee", "tuya", "espressif", "esp-", "esp32", "esp8266", "lumi", "sonoff", "датчик", "розетк", "лампа", "light"):
		return "iot"
	case has("пылесос", "vacuum", "roborock", "dreame", "roomba"):
		return "vacuum"
	case has("kindle", "pocketbook", "onyx", "книг"):
		return "ebook"
	case has("car", "авто", "tesla", "bmw", "mercedes"):
		return "car"
	case has("android", "galaxy", "redmi", "xiaomi", "poco", "pixel", "huawei", "honor", "oneplus", "realme", "смартфон", "телефон", "phone", "частный адрес"):
		return "android"
	case has("laptop", "notebook", "ноутбук", "thinkpad", "zenbook", "vivobook", "matebook"):
		return "laptop"
	case has("pc", "desktop", "компьютер", "comp", "-pc", "workstation"):
		return "pc"
	}
	return "other"
}

// migrateEmojiName — имена старого формата («📱 Папа · iPhone»): значок → тип, имя — без значка.
func migrateEmojiName(d *devInfo) {
	n := strings.TrimSpace(d.Name)
	if n == "" {
		return
	}
	pref := map[string]string{"📱": "", "💻": "laptop", "🖥": "pc", "⌚": "watch", "📺": "", "🎮": "", "📡": "media", "🔊": "speaker", "📷": "camera"}
	for e, k := range pref {
		if strings.HasPrefix(n, e) {
			rest := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(n, e), "️"))
			if d.Kind == "" {
				if k == "" || guessKind(rest, d.Vendor) != "other" {
					k = guessKind(rest, d.Vendor)
					if k == "other" {
						k = map[string]string{"📱": "android", "📺": "tv", "🎮": "ps"}[e]
					}
				}
				d.Kind = k
			}
			d.Name = rest
			return
		}
	}
}

// devLabel — подпись устройства для журналов и Telegram: «значок имя».
func devLabel(d *devInfo) string {
	n := d.Name
	if n == "" {
		n = d.KName
	}
	if n == "" {
		return ""
	}
	k := d.Kind
	if k == "" {
		k = guessKind(d.Name+" "+d.KName+" "+d.Host, d.Vendor)
	}
	if e := devKindEmoji[k]; e != "" && k != "other" {
		return e + " " + n
	}
	return n
}

// keeneticHostsLoop — раз в 5 минут берёт имена клиентов из Keenetic (show ip hotspot).
func (a *app) keeneticHostsLoop() {
	time.Sleep(30 * time.Second)
	for {
		loadPolicyNames()
		if b, err := rciGet("show/ip/hotspot"); err == nil {
			var r struct {
				Host []struct {
					MAC      string          `json:"mac"`
					IP       string          `json:"ip"`
					Name     string          `json:"name"`
					Hostname string          `json:"hostname"`
					Policy   string          `json:"policy"`
					SSID     string          `json:"ssid"`
					RSSI     json.Number     `json:"rssi"`
					Port     json.RawMessage `json:"port"`
					Iface    json.RawMessage `json:"interface"`
					Mws      json.RawMessage `json:"mws"`
					Active   any             `json:"active"`
				} `json:"host"`
			}
			if json.Unmarshal(b, &r) == nil && len(r.Host) > 0 {
				tr := a.trf
				tr.mu.Lock()
				for _, h := range r.Host {
					mac := strings.ToLower(h.MAC)
					d := tr.devices[mac]
					if d == nil {
						continue // устройство ещё не появлялось в XKeen — не добавляем
					}
					if h.Name != "" && h.Name != h.MAC {
						d.KName = h.Name
					}
					if h.Hostname != "" {
						d.Host = h.Hostname
					}
					d.Policy = policyName(h.Policy)
					switch {
					case h.SSID != "":
						d.Conn = "Wi-Fi «" + h.SSID + "»"
						if h.RSSI != "" {
							d.Conn += ", сигнал " + string(h.RSSI) + " дБм"
						}
						if len(h.Mws) > 2 && string(h.Mws) != "null" {
							d.Conn += ", через mesh-точку"
						}
					case len(h.Port) > 0 && string(h.Port) != "null":
						d.Conn = "кабель, порт " + strings.Trim(string(h.Port), `"{}`)
						if len(d.Conn) > 40 {
							d.Conn = "кабель"
						}
					}
				}
				tr.mu.Unlock()
			}
		}
		time.Sleep(5 * time.Minute)
	}
}

// policyName — человеческое имя политики Keenetic (Policy0, Policy1…), если известно.
var policyNames = map[string]string{}

func policyName(id string) string {
	if id == "" {
		return "по умолчанию"
	}
	if n := policyNames[id]; n != "" {
		return n
	}
	return id
}

// loadPolicyNames — описания политик из Keenetic (show ip policy).
func loadPolicyNames() {
	b, err := rciGet("show/ip/policy")
	if err != nil {
		return
	}
	var m map[string]map[string]any
	if json.Unmarshal(b, &m) != nil {
		return
	}
	for id, v := range m {
		if d, _ := v["description"].(string); d != "" {
			policyNames[id] = d
		}
	}
}

// handleRCI — только чтение «show …» из Keenetic (для диагностики).
func (a *app) handleRCI(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Query().Get("p"), "/")
	if !strings.HasPrefix(p, "show/") || strings.Contains(p, "..") {
		http.Error(w, "только show/…", 400)
		return
	}
	b, err := rciGet(p)
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Write(b)
}
