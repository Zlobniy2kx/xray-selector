package main

// Пароль на панель: хэш sha256 с солью в файле настроек, сессии (cookie) — в
// отдельном файле, живут 30 дней. Пока пароль не задан, панель открыта (как раньше)
// и показывает предупреждение. Сброс: xray-selector -reset-password.

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var sessMu sync.Mutex
var sessions = map[string]int64{} // токен → до какого времени
var loginFails = map[string][]int64{}

func sessionsPath() string {
	return filepath.Join(filepath.Dir(*flagState), "xray-selector-sessions.json")
}

func loadSessions() {
	b, err := os.ReadFile(sessionsPath())
	if err != nil {
		return
	}
	sessMu.Lock()
	json.Unmarshal(b, &sessions)
	sessMu.Unlock()
}

func saveSessionsLocked() {
	now := time.Now().Unix()
	for k, v := range sessions {
		if v < now {
			delete(sessions, k)
		}
	}
	b, _ := json.Marshal(sessions)
	os.WriteFile(sessionsPath(), b, 0600)
}

func hashPass(salt, pass string) string {
	h := sha256.Sum256([]byte(salt + "\x00" + pass))
	for i := 0; i < 50000; i++ { // чуть замедлить перебор
		h = sha256.Sum256(h[:])
	}
	return hex.EncodeToString(h[:])
}

func randHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (a *app) passwordSet() (bool, string, string) {
	a.watch.mu.Lock()
	defer a.watch.mu.Unlock()
	return a.watch.settings.PassHash != "", a.watch.settings.PassHash, a.watch.settings.PassSalt
}

func validSession(r *http.Request) bool {
	c, err := r.Cookie("xsel_s")
	if err != nil || c.Value == "" {
		return false
	}
	sessMu.Lock()
	defer sessMu.Unlock()
	exp, ok := sessions[c.Value]
	return ok && exp > time.Now().Unix()
}

// authMiddleware — пропускает всё, если пароль не задан; иначе нужна сессия.
func (a *app) authMiddleware(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		open := p == "/login" || p == "/api/login" || p == "/manifest.webmanifest" || p == "/favicon.png" ||
			p == "/icon-180.png" || p == "/icon-512.png" || p == "/flags.ttf"
		if set, _, _ := a.passwordSet(); !set || open || validSession(r) {
			h.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(p, "/api/") {
			w.WriteHeader(401)
			writeJSON(w, map[string]any{"error": "нужен вход", "login": true})
			return
		}
		http.Redirect(w, r, "/login", http.StatusFound)
	})
}

func clientIP(r *http.Request) string {
	h := r.RemoteAddr
	if i := strings.LastIndex(h, ":"); i > 0 {
		h = h[:i]
	}
	return h
}

func (a *app) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(loginHTML))
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	jsonDecode(r, &req)
	ip := clientIP(r)
	now := time.Now().Unix()
	sessMu.Lock()
	var recent []int64
	for _, t := range loginFails[ip] {
		if now-t < 600 {
			recent = append(recent, t)
		}
	}
	loginFails[ip] = recent
	sessMu.Unlock()
	if len(recent) >= 8 {
		w.WriteHeader(429)
		writeJSON(w, map[string]any{"error": "слишком много попыток — подождите 10 минут"})
		return
	}
	set, hash, salt := a.passwordSet()
	if !set {
		writeJSON(w, map[string]any{"ok": true})
		return
	}
	if subtle.ConstantTimeCompare([]byte(hashPass(salt, req.Password)), []byte(hash)) != 1 {
		sessMu.Lock()
		loginFails[ip] = append(loginFails[ip], now)
		sessMu.Unlock()
		time.Sleep(700 * time.Millisecond)
		w.WriteHeader(401)
		writeJSON(w, map[string]any{"error": "неверный пароль"})
		return
	}
	tok := randHex(24)
	sessMu.Lock()
	sessions[tok] = now + 30*86400
	saveSessionsLocked()
	sessMu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "xsel_s", Value: tok, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 30 * 86400})
	a.watch.mu.Lock()
	a.watch.addEvent("", "info", "вход в панель с "+ip)
	a.watch.mu.Unlock()
	writeJSON(w, map[string]any{"ok": true})
}

func (a *app) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("xsel_s"); err == nil {
		sessMu.Lock()
		delete(sessions, c.Value)
		saveSessionsLocked()
		sessMu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: "xsel_s", Value: "", Path: "/", MaxAge: -1})
	writeJSON(w, map[string]any{"ok": true})
}

// handlePassword — задать/сменить/снять пароль. Если пароль уже есть — нужен старый.
func (a *app) handlePassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		set, _, _ := a.passwordSet()
		writeJSON(w, map[string]any{"set": set})
		return
	}
	var req struct {
		Old string `json:"old"`
		New string `json:"new"`
	}
	jsonDecode(r, &req)
	set, hash, salt := a.passwordSet()
	if set && subtle.ConstantTimeCompare([]byte(hashPass(salt, req.Old)), []byte(hash)) != 1 {
		time.Sleep(700 * time.Millisecond)
		w.WriteHeader(401)
		writeJSON(w, map[string]any{"error": "старый пароль неверный"})
		return
	}
	if req.New != "" && len([]rune(req.New)) < 6 {
		w.WriteHeader(400)
		writeJSON(w, map[string]any{"error": "пароль — хотя бы 6 символов"})
		return
	}
	a.watch.mu.Lock()
	if req.New == "" {
		a.watch.settings.PassHash, a.watch.settings.PassSalt = "", ""
		a.watch.addEvent("", "info", "пароль панели снят")
	} else {
		s := randHex(16)
		a.watch.settings.PassSalt, a.watch.settings.PassHash = s, hashPass(s, req.New)
		a.watch.addEvent("", "info", "пароль панели задан")
	}
	a.watch.mu.Unlock()
	a.mu.Lock()
	a.saveStateLocked()
	a.mu.Unlock()
	// все старые сессии — закрыть, текущему — новая
	sessMu.Lock()
	sessions = map[string]int64{}
	if req.New != "" {
		tok := randHex(24)
		sessions[tok] = time.Now().Unix() + 30*86400
		http.SetCookie(w, &http.Cookie{Name: "xsel_s", Value: tok, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 30 * 86400})
	}
	saveSessionsLocked()
	sessMu.Unlock()
	writeJSON(w, map[string]any{"ok": true, "set": req.New != ""})
}

const loginHTML = `<!doctype html><html lang="ru"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Xray Selector — вход</title><link rel="icon" href="/favicon.png"><link rel="apple-touch-icon" href="/icon-180.png">
<style>:root{color-scheme:dark}body{margin:0;min-height:100vh;display:grid;place-items:center;background:radial-gradient(1200px 600px at 20% 0%,#1b1f3a,#0b0d18);font:15px/1.5 -apple-system,Segoe UI,Inter,sans-serif;color:#e7e9f3}
.c{width:min(360px,calc(100vw - 32px));background:rgba(255,255,255,.05);border:1px solid rgba(255,255,255,.1);border-radius:18px;padding:28px;backdrop-filter:blur(12px)}
h1{font-size:20px;margin:0 0 4px}p{margin:0 0 18px;color:#9aa0b8;font-size:13px}input{width:100%;box-sizing:border-box;padding:12px 14px;border-radius:12px;border:1px solid rgba(255,255,255,.15);background:rgba(0,0,0,.3);color:inherit;font-size:16px}
button{margin-top:12px;width:100%;padding:12px;border:0;border-radius:12px;background:linear-gradient(135deg,#7c5cff,#22d3ee);color:#fff;font-weight:600;font-size:15px;cursor:pointer}.e{color:#f87171;font-size:13px;min-height:20px;margin-top:8px}</style></head>
<body><form class="c" id="f"><h1>Xray Selector</h1><p>Введите пароль панели</p><input id="p" type="password" autocomplete="current-password" autofocus><button>Войти</button><div class="e" id="e"></div>
<p style="margin-top:14px">Забыли пароль? По SSH на роутере:<br><code>xray-selector -reset-password</code></p></form>
<script>f.onsubmit=async e=>{e.preventDefault();const r=await fetch('/api/login',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({password:p.value})});if(r.ok)location.href='/';else{const j=await r.json().catch(()=>({}));document.getElementById('e').textContent=j.error||'ошибка'}}</script></body></html>`
