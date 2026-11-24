package main

import (
	"strconv"
	"testing"
	"time"
)

func TestLimitDetectAndReset(t *testing.T) {
	now := time.Date(2026, 11, 24, 19, 40, 0, 0, time.Local)
	fb := now.Add(2 * time.Hour)
	cases := []struct {
		msg  string
		want time.Time
	}{
		{"Claude AI usage limit reached|" + itoa(now.Add(80*time.Minute).Unix()), now.Add(80 * time.Minute)},
		{"5-hour limit reached ∙ resets 9pm", time.Date(2026, 11, 24, 21, 0, 0, 0, time.Local)},
		{"You've hit your limit · resets 8:30pm (Europe/Amsterdam)", time.Date(2026, 11, 24, 20, 30, 0, 0, time.Local)},
		{"You've hit your limit · resets 3am", time.Date(2026, 11, 25, 3, 0, 0, 0, time.Local)},
		{"usage limit reached", fb},
	}
	for _, c := range cases {
		if !isLimitMsg(c.msg) {
			t.Errorf("not detected: %q", c.msg)
		}
		if got := parseReset(c.msg, now, fb); !got.Equal(c.want) {
			t.Errorf("%q: got %v want %v", c.msg, got, c.want)
		}
	}
	for _, ok := range []string{"All 42 tests pass", "Build failed: Android SDK missing"} {
		if isLimitMsg(ok) {
			t.Errorf("false positive: %q", ok)
		}
	}
	if fmtLeft(72*time.Minute) != "1h12" || fmtLeft(9*time.Minute) != "9m" {
		t.Error("fmtLeft")
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
