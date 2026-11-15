package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func pbBytes(num int, b []byte) []byte {
	out := binary.AppendUvarint(nil, uint64(num<<3|2))
	out = binary.AppendUvarint(out, uint64(len(b)))
	return append(out, b...)
}
func pbVar(num int, v uint64) []byte {
	out := binary.AppendUvarint(nil, uint64(num<<3))
	return binary.AppendUvarint(out, v)
}

func TestSites(t *testing.T) {
	dir := t.TempDir()
	*flagConfDir = dir
	*flagState = filepath.Join(dir, "st.json")
	*flagXray = filepath.Join(dir, "xray") // assetDir = dir
	os.WriteFile(filepath.Join(dir, "04_outbounds.json"), []byte(`{"outbounds":[{"tag":"🇩🇪 DE-1","protocol":"vless","settings":{"vnext":[{"address":"1.1.1.1","port":443}]}},{"tag":"🇫🇮 FI-1","protocol":"vless","settings":{"vnext":[{"address":"1.1.1.2","port":443}]}},{"tag":"direct","protocol":"freedom"},{"tag":"block","protocol":"blackhole"}]}`), 0644)
	os.WriteFile(filepath.Join(dir, "05_routing.json"), []byte(`{
  // мои правила
  "routing": {
    "rules": [
      { "inboundTag": ["api"], "outboundTag": "api" },
      { "ip": ["192.168.0.0/16"], "outboundTag": "direct" },
      { "network": "udp", "port": "443", "outboundTag": "block" },
      { "domain": ["domain:battle.net"], "outboundTag": "direct" },
      { "domain": ["ext:geosite_zkeen.dat:youtube", "domain:instagram.com", "keyword:hamster"], "balancerTag": "auto" },
      { "network": "tcp,udp", "outboundTag": "direct" }
    ],
    "balancers": [ { "tag": "auto", "selector": ["🇩🇪 DE-1"], "fallbackTag": "🇩🇪 DE-1" } ]
  },
  "observatory": { "subjectSelector": ["🇩🇪 DE-1"] }
}`), 0644)
	// geosite_zkeen.dat: youtube: domain googlevideo.com, full youtube.com
	d1 := append(pbVar(1, 2), pbBytes(2, []byte("googlevideo.com"))...)
	d2 := append(pbVar(1, 3), pbBytes(2, []byte("youtube.com"))...)
	site := append(append(pbBytes(1, []byte("YOUTUBE")), pbBytes(2, d1)...), pbBytes(2, d2)...)
	other := pbBytes(1, []byte("OTHER"))
	os.WriteFile(filepath.Join(dir, "geosite_zkeen.dat"), append(pbBytes(1, other), pbBytes(1, site)...), 0644)

	a := &app{watch: newWatcher(), dead: map[string]bool{}, desired: map[string]string{"auto": "🇩🇪 DE-1"}, results: map[string]result{}, hist: map[string][]check{}}
	rules, _, err := a.listRules()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rules {
		fmt.Println(r.Index, r.Kind, r.Description)
	}
	for _, h := range []string{"rr1.googlevideo.com", "www.youtube.com", "youtube.com", "hamsterkombat.io", "eu.battle.net", "ya.ru", "https://www.instagram.com/p/x"} {
		res, err := a.routeFor(h)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Println(h, "→", res.Kind, res.Rule, res.Why)
	}
	// добавить в VPN
	_, old, nt, desc, err := a.editSites(siteOp{Op: "addTo", Target: "vpn", Values: []string{"https://chat.openai.com/x, 91.105.192.0/23\nbattle.net"}})
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println(desc)
	rem, add := diffLines(old, nt)
	fmt.Println("- ", rem, "\n+ ", add)
	os.WriteFile(filepath.Join(dir, "05_routing.json"), []byte(nt), 0644)
	var chk any
	if err := json.Unmarshal(stripJSONComments([]byte(nt)), &chk); err != nil {
		t.Fatal(err)
	}
	// отключить и включить
	_, _, nt, desc, err = a.editSites(siteOp{Op: "disable", Rule: 4, Values: []string{"keyword:hamster"}})
	fmt.Println(desc, err)
	os.WriteFile(filepath.Join(dir, "05_routing.json"), []byte(nt), 0644)
	res, _ := a.routeFor("hamsterkombat.io")
	fmt.Println("hamster after disable →", res.Kind, res.Disable)
	_, _, nt, desc, err = a.editSites(siteOp{Op: "enable", Rule: 4, Values: []string{"keyword:hamster"}})
	fmt.Println(desc, err)
	os.WriteFile(filepath.Join(dir, "05_routing.json"), []byte(nt), 0644)
	// привязка к Финляндии
	_, _, nt, desc, err = a.editSites(siteOp{Op: "bind", Target: "🇫🇮", Values: []string{"claude.ai", "anthropic.com"}})
	fmt.Println(desc, err)
	os.WriteFile(filepath.Join(dir, "05_routing.json"), []byte(nt), 0644)
	if err := json.Unmarshal(stripJSONComments([]byte(nt)), &chk); err != nil {
		t.Fatal(err)
	}
	res, _ = a.routeFor("claude.ai")
	fmt.Println("claude →", res.Kind, res.Target)
	_, _, nt, desc, err = a.editSites(siteOp{Op: "unbind", Target: "🇫🇮"})
	fmt.Println(desc, err)
	if err := json.Unmarshal(stripJSONComments([]byte(nt)), &chk); err != nil {
		t.Fatal(err, nt)
	}
	fmt.Println(strings.Count(nt, "xsel-"), "xsel left")
	// удалить последнюю запись правила — должно отказать
	_, _, _, _, err = a.editSites(siteOp{Op: "remove", Rule: 3, Values: []string{"domain:battle.net"}})
	fmt.Println("remove last:", err)
	fmt.Println(nt)
}

func TestSubParse(t *testing.T) {
	links := []string{
		"vless://11111111-2222-3333-4444-555555555555@nl.example.com:443?type=xhttp&security=reality&sni=www.google.com&pbk=PUBKEY&sid=ab12&fp=chrome&path=%2Fxh&mode=auto#%F0%9F%87%B3%F0%9F%87%B1%20Netherlands-90s(xHTTP)",
		"vless://uuid@de.example.com:443?type=grpc&security=tls&sni=de.example.com&serviceName=gs&mode=multi#DE gRPC",
		"trojan://pass@tr.example.com:443?type=ws&path=/ws&host=tr.example.com#TR",
		"ss://" + "YWVzLTI1Ni1nY206cGFzcw" + "@ss.example.com:8388#SS",
		"vmess://" + "eyJ2IjoiMiIsInBzIjoiVk0iLCJhZGQiOiJ2bS5leGFtcGxlLmNvbSIsInBvcnQiOiI0NDMiLCJpZCI6InV1aWQiLCJuZXQiOiJ3cyIsInBhdGgiOiIvdiIsInRscyI6InRscyJ9",
	}
	body := []byte(b64std(strings.Join(links, "\n")))
	obs, skipped, err := parseSubBody(body)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range obs {
		b, _ := json.Marshal(o)
		fmt.Println(string(b))
	}
	fmt.Println("skipped", skipped)
	fmt.Println(parseUserinfo("upload=1; download=2; total=100; expire=1800000000"))
}

func TestPass(t *testing.T) {
	h := hashPass("s", "123456")
	if h != hashPass("s", "123456") || h == hashPass("s", "1234567") {
		t.Fatal("hash")
	}
}

func b64std(s string) string { return base64Std(s) }
