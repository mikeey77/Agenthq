package main

// Usage limits: when Claude says the subscription limit is reached, every agent is parked,
// Telegram gets one message with the reset time, and the agents continue on their own
// once the limit resets. A warning is also sent when the 5-hour block is almost used up.

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

var limitWords = []string{"usage limit", "limit reached", "hit your limit", "limit will reset", "resets at", "out of extra usage", "weekly limit", "session limit"}

func isLimitMsg(s string) bool {
	l := strings.ToLower(s)
	for _, w := range limitWords {
		if strings.Contains(l, w) {
			return true
		}
	}
	return false
}

var (
	reEpoch = regexp.MustCompile(`\|(\d{10})`)
	reClock = regexp.MustCompile(`(?i)resets?(?: at)?\s+(\d{1,2})(?::(\d{2}))?\s*(am|pm)?`)
)

// parseReset finds when the limit resets in Claude's message, falling back to the usage block end.
func parseReset(msg string, now time.Time, fallback time.Time) time.Time {
	if m := reEpoch.FindStringSubmatch(msg); m != nil {
		if n, err := strconv.ParseInt(m[1], 10, 64); err == nil {
			if t := time.Unix(n, 0); t.After(now) {
				return t
			}
		}
	}
	if m := reClock.FindStringSubmatch(msg); m != nil {
		h, _ := strconv.Atoi(m[1])
		mi, _ := strconv.Atoi(m[2])
		switch strings.ToLower(m[3]) {
		case "pm":
			if h < 12 {
				h += 12
			}
		case "am":
			if h == 12 {
				h = 0
			}
		}
		if h < 24 && mi < 60 {
			t := time.Date(now.Year(), now.Month(), now.Day(), h, mi, 0, 0, now.Location())
			if !t.After(now) {
				t = t.Add(24 * time.Hour)
			}
			return t
		}
	}
	if fallback.After(now) {
		return fallback
	}
	return now.Add(time.Hour)
}

func fmtLeft(d time.Duration) string {
	if d < time.Minute {
		return "<1m"
	}
	h, m := int(d.Hours()), int(d.Minutes())%60
	if h == 0 {
		return strconv.Itoa(m) + "m"
	}
	return strconv.Itoa(h) + "h" + strconv.Itoa(m/10) + strconv.Itoa(m%10)
}

// hitLimit parks all agents until the reset. Caller holds a.mu.
func (a *App) hitLimit(ag *Agent, msg string) {
	now := time.Now()
	reset := parseReset(msg, now, a.usage.Summary(a.cfg.TokenBudget).ResetAt)
	first := a.limitUntil.IsZero() || now.After(a.limitUntil)
	if reset.After(a.limitUntil) {
		a.limitUntil = reset
	}
	park := func(x *Agent) {
		if x.Status == "stopped" || x.Status == "done" || (x.Status == "paused" && !x.LimitPaused) {
			return
		}
		if isActive(x.Status) && x != ag {
			x.NeedsContinue = true
			a.killAgent(x)
		}
		if x.SessionID != "" {
			x.NeedsContinue = true
		}
		x.Status, x.LimitPaused, x.LimitUntil = "paused", true, a.limitUntil
		x.Activity = a.T("limit_wait", a.limitUntil.Format("15:04"))
	}
	park(ag)
	for _, x := range a.agents {
		if x.Status == "queued" || isActive(x.Status) || x.Status == "idle" && len(x.Inbox) > 0 {
			park(x)
		}
	}
	a.dirty = true
	a.addLog(ag, "info", "usage limit reached, waiting until "+a.limitUntil.Format("15:04"))
	if first {
		a.emit("usage", ag.ID, a.T("limit_hit", a.limitUntil.Format("15:04"), fmtLeft(time.Until(a.limitUntil))))
		a.termAdd("info", a.T("limit_hit", a.limitUntil.Format("15:04"), fmtLeft(time.Until(a.limitUntil))))
	}
}

// limited reports whether new turns must wait for a usage reset. Caller holds a.mu.
func (a *App) limited() bool { return !a.limitUntil.IsZero() && time.Now().Before(a.limitUntil) }

// limitLoop resumes parked agents after the reset and warns when the block is nearly used up.
func (a *App) limitLoop() {
	for range time.Tick(20 * time.Second) {
		a.checkLimit(time.Now())
	}
}

func (a *App) checkLimit(now time.Time) {
	u := a.usage.Summary(a.cfg.TokenBudget)
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.limitUntil.IsZero() && now.After(a.limitUntil.Add(30*time.Second)) {
		a.limitUntil = time.Time{}
		n := 0
		for _, id := range a.order {
			if x := a.agents[id]; x != nil && x.LimitPaused {
				x.LimitPaused, x.LimitUntil = false, time.Time{}
				if x.Status == "paused" {
					a.resumeAgent(x)
					n++
				}
			}
		}
		if n > 0 {
			a.emit("usage", "", a.T("limit_reset", n))
			a.termAdd("info", a.T("limit_reset", n))
		}
		a.dirty = true
	}
	// warn once per block at 90%
	if u.Ready && u.Percent >= 90 && u.BlockActive && !u.ResetAt.Equal(a.warnedBlock) {
		a.warnedBlock = u.ResetAt
		a.emit("usage", "", a.T("limit_near", u.Percent, u.ResetAt.Format("15:04"), fmtLeft(time.Until(u.ResetAt))))
	}
}
