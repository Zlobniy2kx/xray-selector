package main

// Минимальный gRPC-клиент к Xray API без внешних зависимостей:
// HTTP/2 без TLS (h2c) из стандартной библиотеки + ручное protobuf-кодирование.

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

type xrayAPI struct {
	addr   string
	client *http.Client
}

func newXrayAPI(addr string) *xrayAPI {
	p := new(http.Protocols)
	p.SetUnencryptedHTTP2(true)
	tr := &http.Transport{Protocols: p, Proxy: nil}
	return &xrayAPI{addr: addr, client: &http.Client{Transport: tr, Timeout: 5 * time.Second}}
}

func (a *xrayAPI) call(ctx context.Context, method string, req []byte) ([]byte, error) {
	frame := make([]byte, 5+len(req))
	binary.BigEndian.PutUint32(frame[1:5], uint32(len(req)))
	copy(frame[5:], req)
	r, err := http.NewRequestWithContext(ctx, "POST", "http://"+a.addr+method, bytes.NewReader(frame))
	if err != nil {
		return nil, err
	}
	r.Header.Set("Content-Type", "application/grpc")
	r.Header.Set("TE", "trailers")
	resp, err := a.client.Do(r)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	st := resp.Trailer.Get("Grpc-Status")
	if st == "" {
		st = resp.Header.Get("Grpc-Status")
	}
	if st != "" && st != "0" {
		return nil, fmt.Errorf("grpc status %s: %s", st, resp.Header.Get("Grpc-Message")+resp.Trailer.Get("Grpc-Message"))
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("http %d", resp.StatusCode)
	}
	if len(body) < 5 {
		return []byte{}, nil
	}
	n := binary.BigEndian.Uint32(body[1:5])
	if int(n) > len(body)-5 {
		return nil, errors.New("short grpc frame")
	}
	return body[5 : 5+n], nil
}

// ---- protobuf helpers ----

func pbVarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

func pbString(b []byte, field int, s string) []byte {
	if s == "" {
		return b
	}
	b = pbVarint(b, uint64(field<<3|2))
	b = pbVarint(b, uint64(len(s)))
	return append(b, s...)
}

type pbField struct {
	num   int
	wire  int
	u     uint64
	bytes []byte
}

func pbParse(b []byte) ([]pbField, error) {
	var out []pbField
	for len(b) > 0 {
		key, n := readVarint(b)
		if n <= 0 {
			return nil, errors.New("bad varint")
		}
		b = b[n:]
		f := pbField{num: int(key >> 3), wire: int(key & 7)}
		switch f.wire {
		case 0:
			v, n := readVarint(b)
			if n <= 0 {
				return nil, errors.New("bad varint")
			}
			f.u = v
			b = b[n:]
		case 1:
			if len(b) < 8 {
				return nil, errors.New("short")
			}
			f.u = binary.LittleEndian.Uint64(b)
			b = b[8:]
		case 2:
			l, n := readVarint(b)
			if n <= 0 || int(l) > len(b)-n {
				return nil, errors.New("bad len")
			}
			f.bytes = b[n : n+int(l)]
			b = b[n+int(l):]
		case 5:
			if len(b) < 4 {
				return nil, errors.New("short")
			}
			f.u = uint64(binary.LittleEndian.Uint32(b))
			b = b[4:]
		default:
			return nil, errors.New("wire type " + strconv.Itoa(f.wire))
		}
		out = append(out, f)
	}
	return out, nil
}

func readVarint(b []byte) (uint64, int) {
	var v uint64
	for i := 0; i < len(b) && i < 10; i++ {
		v |= uint64(b[i]&0x7f) << (7 * i)
		if b[i] < 0x80 {
			return v, i + 1
		}
	}
	return 0, -1
}

// ---- Xray methods ----

type balancerInfo struct {
	Override  string   `json:"override"`
	Principle []string `json:"principle"`
}

func (a *xrayAPI) GetBalancerInfo(ctx context.Context, tag string) (*balancerInfo, error) {
	resp, err := a.call(ctx, "/xray.app.router.command.RoutingService/GetBalancerInfo", pbString(nil, 1, tag))
	if err != nil {
		return nil, err
	}
	info := &balancerInfo{}
	top, err := pbParse(resp)
	if err != nil {
		return nil, err
	}
	for _, f := range top {
		if f.num != 1 || f.wire != 2 {
			continue
		}
		bal, err := pbParse(f.bytes)
		if err != nil {
			return nil, err
		}
		for _, bf := range bal {
			sub, _ := pbParse(bf.bytes)
			switch bf.num {
			case 5: // override
				for _, s := range sub {
					if s.num == 2 {
						info.Override = string(s.bytes)
					}
				}
			case 6: // principle_target
				for _, s := range sub {
					if s.num == 1 && len(s.bytes) > 0 {
						info.Principle = append(info.Principle, string(s.bytes))
					}
				}
			}
		}
	}
	return info, nil
}

func (a *xrayAPI) OverrideBalancer(ctx context.Context, balancer, target string) error {
	if demoMode { // учебная копия не переключает настоящий Xray
		return nil
	}
	req := pbString(nil, 1, balancer)
	req = pbString(req, 2, target)
	_, err := a.call(ctx, "/xray.app.router.command.RoutingService/OverrideBalancerTarget", req)
	return err
}

type obsStatus struct {
	Alive    bool   `json:"alive"`
	Delay    int64  `json:"delay"`
	Err      string `json:"err,omitempty"`
	LastSeen int64  `json:"lastSeen"`
	LastTry  int64  `json:"lastTry"`
}

func (a *xrayAPI) GetObservatory(ctx context.Context) (map[string]obsStatus, error) {
	resp, err := a.call(ctx, "/xray.core.app.observatory.command.ObservatoryService/GetOutboundStatus", nil)
	if err != nil {
		return nil, err
	}
	out := map[string]obsStatus{}
	top, err := pbParse(resp)
	if err != nil {
		return nil, err
	}
	for _, f := range top {
		if f.num != 1 {
			continue
		}
		res, _ := pbParse(f.bytes)
		for _, r := range res {
			if r.num != 1 {
				continue
			}
			st, _ := pbParse(r.bytes)
			var s obsStatus
			var tag string
			for _, x := range st {
				switch x.num {
				case 1:
					s.Alive = x.u != 0
				case 2:
					s.Delay = int64(x.u)
				case 3:
					s.Err = string(x.bytes)
				case 4:
					tag = string(x.bytes)
				case 5:
					s.LastSeen = int64(x.u)
				case 6:
					s.LastTry = int64(x.u)
				}
			}
			if tag != "" {
				out[tag] = s
			}
		}
	}
	return out, nil
}
