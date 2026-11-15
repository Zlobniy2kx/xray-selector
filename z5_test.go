package main

import "testing"

func TestStats29(t *testing.T) {
	w := newWatcher()
	now := int64(1_800_000_000)
	now -= now % 86400
	// 5 дней: в 20:00 (UTC, tz 0) сервер падает, в остальное время ок
	w.settings.TzOffsetMin = 0
	for d := int64(1); d <= 5; d++ {
		for h := int64(0); h < 24; h++ {
			for k := int64(0); k < 4; k++ {
				ts := now - d*86400 + h*3600 + k*900
				ok := h != 20
				w.recordWatchLocked("A", wcheck{T: ts, OK: ok, Ms: int32(200 + k*100)})
			}
		}
	}
	at20 := now + 20*3600
	if p := w.hourPenaltyLocked("A", at20); p == 0 {
		t.Fatal("no evening penalty")
	}
	if p := w.hourPenaltyLocked("A", now+10*3600); p != 0 {
		t.Fatal("penalty at 10:00", p)
	}
	wk := w.serverWeekLocked("A", now)
	if wk["falls7"].(int) < 4 {
		t.Fatalf("falls %v", wk["falls7"])
	}
	if jitterPenalty(400) == 0 || jitterPenalty(50) != 0 {
		t.Fatal("jitter penalty")
	}
}
