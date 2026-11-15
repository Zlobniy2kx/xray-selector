package main

// Диагностика «почему не открывается сайт»: куда его направит Xray, что отвечает DNS
// (и нет ли подмены), открывается ли напрямую и через текущий VPN-сервер — по шагам.

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

type diagStep struct {
	Name string `json:"name"`
	OK   bool   `json:"ok"`
	Ms   int64  `json:"ms,omitempty"`
	Info string `json:"info"`
}

// probeStages: TCP → TLS → HTTP через dial (напрямую или через socks).
func probeStages(prefix, host string, dial func(ctx context.Context, addr string) (net.Conn, error)) []diagStep {
	var steps []diagStep
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	t0 := time.Now()
	conn, err := dial(ctx, net.JoinHostPort(host, "443"))
	if err != nil {
		return append(steps, diagStep{prefix + ": соединение", false, 0, explainNetErr(err)})
	}
	steps = append(steps, diagStep{prefix + ": соединение", true, time.Since(t0).Milliseconds(), "порт 443 открыт"})
	conn.SetDeadline(time.Now().Add(8 * time.Second))
	t1 := time.Now()
	tc := tls.Client(conn, &tls.Config{ServerName: host})
	if err := tc.Handshake(); err != nil {
		conn.Close()
		return append(steps, diagStep{prefix + ": TLS", false, 0, explainNetErr(err) + " — так обычно выглядит блокировка по имени сайта (DPI)"})
	}
	steps = append(steps, diagStep{prefix + ": TLS", true, time.Since(t1).Milliseconds(), "сертификат " + tc.ConnectionState().PeerCertificates[0].Subject.CommonName})
	t2 := time.Now()
	fmt.Fprintf(tc, "GET / HTTP/1.1\r\nHost: %s\r\nUser-Agent: Mozilla/5.0\r\nConnection: close\r\n\r\n", host)
	buf := make([]byte, 64*1024)
	n, err := io.ReadAtLeast(tc, buf, 12)
	tc.Close()
	if err != nil && n < 12 {
		return append(steps, diagStep{prefix + ": загрузка", false, 0, explainNetErr(err) + " — соединение есть, но данные не идут («заморозка»)"})
	}
	line := strings.SplitN(string(buf[:n]), "\r\n", 2)[0]
	steps = append(steps, diagStep{prefix + ": загрузка", true, time.Since(t2).Milliseconds(), line})
	return steps
}

func explainNetErr(err error) string {
	s := err.Error()
	switch {
	case strings.Contains(s, "reset"):
		return "соединение сброшено (reset)"
	case strings.Contains(s, "timeout") || strings.Contains(s, "deadline"):
		return "нет ответа (таймаут)"
	case strings.Contains(s, "refused"):
		return "отказ в соединении"
	case strings.Contains(s, "no such host"):
		return "такого имени нет в DNS"
	case strings.Contains(s, "EOF"):
		return "соединение оборвано (EOF)"
	}
	return cleanErr(err).Error()
}

// dohResolve — DNS через DoH (Cloudflare JSON API) через указанный http.Client.
func dohResolve(cl *http.Client, host string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://cloudflare-dns.com/dns-query?type=A&name="+host, nil)
	req.Header.Set("Accept", "application/dns-json")
	resp, err := cl.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var j struct {
		Answer []struct {
			Type int    `json:"type"`
			Data string `json:"data"`
		} `json:"Answer"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&j); err != nil {
		return nil, err
	}
	var out []string
	for _, a := range j.Answer {
		if a.Type == 1 {
			out = append(out, a.Data)
		}
	}
	return out, nil
}

func (a *app) diagnose(host string) map[string]any {
	_, h := normalizeEntry(host)
	h = strings.ToLower(strings.TrimPrefix(h, "domain:"))
	var steps []diagStep
	verdict := ""

	// 1) куда направит Xray
	route, rerr := a.routeFor(h)
	if rerr == nil {
		info := kindName(route.Kind)
		if route.Rule >= 0 {
			info += fmt.Sprintf(" — правило №%d (%s)", route.Rule+1, route.Why)
		}
		if route.Active != "" {
			info += ", сервер " + route.Active
		}
		steps = append(steps, diagStep{"Маршрут в Xray", true, 0, info})
	}

	// 2) DNS роутера
	t0 := time.Now()
	ips, derr := net.DefaultResolver.LookupHost(context.Background(), h)
	if derr != nil {
		steps = append(steps, diagStep{"DNS роутера", false, 0, explainNetErr(derr)})
	} else {
		steps = append(steps, diagStep{"DNS роутера", true, time.Since(t0).Milliseconds(), strings.Join(firstN(ips, 4), ", ")})
	}

	// активный сервер для проверки через VPN
	c := a.reload()
	a.mu.Lock()
	active := ""
	for _, g := range c.groups {
		if v := a.desired[g.Tag]; v != "" {
			active = v
			break
		}
	}
	a.mu.Unlock()
	var vpnPort int
	if active != "" {
		if ports, err := a.wprobe.ensure(c, a.watchProbeTags()); err == nil {
			vpnPort = ports[active]
		}
	}

	// 3) DNS через VPN — сравнить (подмена?)
	if vpnPort > 0 {
		cl, tr := socksClient(vpnPort)
		cl.Timeout = 8 * time.Second
		vips, err := dohResolve(cl, h)
		tr.CloseIdleConnections()
		if err != nil {
			steps = append(steps, diagStep{"DNS через VPN (Cloudflare)", false, 0, explainNetErr(err)})
		} else {
			same := false
			for _, x := range vips {
				for _, y := range ips {
					if x == y {
						same = true
					}
				}
			}
			info := strings.Join(firstN(vips, 4), ", ")
			spoof := derr == nil && len(vips) > 0 && !same && looksStub(ips)
			if spoof {
				info += " — роутер получил другие адреса, похоже на подмену DNS провайдером"
				verdict = "Провайдер подменяет DNS для этого сайта. Включите DoH/DoT на роутере или пустите сайт через VPN."
			}
			steps = append(steps, diagStep{"DNS через VPN (Cloudflare)", !spoof, 0, info})
		}
		a.wprobe.mu.Lock()
		a.wprobe.lastUse = time.Now()
		a.wprobe.mu.Unlock()
	}

	// 4) напрямую
	direct := probeStages("Напрямую", h, func(ctx context.Context, addr string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", addr)
	})
	steps = append(steps, direct...)
	directOK := len(direct) == 3 && direct[2].OK

	// 5) через VPN
	vpnOK := false
	if vpnPort > 0 {
		addr := fmt.Sprintf("127.0.0.1:%d", vpnPort)
		via := probeStages("Через VPN ("+active+")", h, func(ctx context.Context, target string) (net.Conn, error) {
			return socks5Dial(ctx, addr, target)
		})
		steps = append(steps, via...)
		vpnOK = len(via) == 3 && via[2].OK
	}

	if verdict == "" {
		kind := ""
		if route != nil {
			kind = route.Kind
		}
		switch {
		case directOK && vpnOK:
			verdict = "Сайт открывается и напрямую, и через VPN. Если на устройстве не грузится — проверьте, что устройство в политике xkeen, и попробуйте закрыть приложение / отключить «Частный узел iCloud»."
		case !directOK && vpnOK && kind == "vpn":
			verdict = "Напрямую сайт заблокирован, но он и так идёт через VPN и через VPN открывается. Проблема, скорее всего, в самом устройстве или приложении (кэш, QUIC, DNS в браузере)."
		case !directOK && vpnOK:
			verdict = "Напрямую сайт заблокирован, а через VPN открывается — добавьте его в «Через VPN»."
		case directOK && !vpnOK && kind == "vpn":
			verdict = "Через текущий VPN-сервер сайт не открывается, а напрямую — да. Попробуйте другой сервер или перенесите сайт в «Напрямую»."
		case directOK && !vpnOK:
			verdict = "Сайт открывается напрямую и идёт напрямую — всё в порядке. Через этот VPN-сервер он бы не открылся."
		case !directOK && !vpnOK:
			verdict = "Сайт не открывается ни напрямую, ни через VPN — возможно, он сам недоступен или блокирует серверы VPN."
		}
	}
	return map[string]any{"host": h, "steps": steps, "verdict": verdict, "route": route}
}

func firstN(l []string, n int) []string {
	if len(l) > n {
		return l[:n]
	}
	return l
}

// looksStub — адреса-заглушки, которые отдают при подмене DNS.
func looksStub(ips []string) bool {
	for _, s := range ips {
		ip := net.ParseIP(s)
		if ip == nil {
			continue
		}
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() {
			return true
		}
	}
	return false
}

func (a *app) handleDiag(w http.ResponseWriter, r *http.Request) {
	h := strings.TrimSpace(r.URL.Query().Get("host"))
	if h == "" {
		http.Error(w, "host?", 400)
		return
	}
	if r.URL.Query().Get("route") == "1" {
		res, err := a.routeFor(h)
		if err != nil {
			writeJSON(w, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, res)
		return
	}
	writeJSON(w, a.diagnose(h))
}
