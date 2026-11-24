package main

import (
	"fmt"
	"strings"
	"time"
)

var statusIcon = map[string]string{"queued": "🔵", "running": "🟢", "waiting": "⚠️", "paused": "⏸", "idle": "⚪", "done": "✅", "stopped": "⏹", "error": "🚨"}

func fmtDur(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h > 0 {
		return fmt.Sprintf("%du %02dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

func (a *App) usageLine() string {
	u := a.usage.Summary(a.cfg.TokenBudget)
	pct := u.Percent
	if pct < 0 {
		pct = 0
	}
	reset := "—"
	if u.BlockActive {
		reset = fmtDur(time.Until(u.ResetAt))
	}
	return a.T("usage_line", fmtTokens(u.BlockTokens), pct, reset, fmtTokens(u.WeekTokens))
}

func (a *App) statusText() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	run, wait := 0, 0
	for _, ag := range a.agents {
		if ag.Status == "running" {
			run++
		}
		if ag.Status == "waiting" {
			wait++
		}
	}
	var b strings.Builder
	b.WriteString(a.T("status_head", run, wait) + "\n")
	views := a.agentViews()
	if len(views) == 0 {
		b.WriteString("\n" + a.T("no_agents"))
	}
	for _, v := range views {
		if !isLive(v.Status) && v.Status != "done" {
			continue
		}
		fmt.Fprintf(&b, "\n%s%s %s — %s", strings.Repeat("   ", v.Depth), statusIcon[v.Status], v.Name, truncate(v.Activity, 90))
	}
	b.WriteString("\n\n" + a.usageLine())
	return b.String()
}

// runCommand executes a master-terminal command and returns output lines.
func (a *App) runCommand(input string) []string {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	fields := strings.Fields(input)
	verb := strings.ToLower(fields[0])
	rest := strings.TrimSpace(strings.TrimPrefix(input, fields[0]))

	forTargets := func(ref string, fn func(*Agent)) []string {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			return []string{"usage: " + verb + " <agent|all>"}
		}
		if strings.EqualFold(ref, "all") {
			for _, id := range a.order {
				fn(a.agents[id])
			}
			return []string{"✓ " + verb + " all"}
		}
		ag := a.agentByRef(ref)
		if ag == nil {
			return []string{a.T("unknown_agent", ref)}
		}
		fn(ag)
		return []string{"✓ " + ag.Name + " → " + ag.Status}
	}

	switch {
	case verb == "help" || verb == "?":
		return strings.Split(a.T("help"), "\n")
	case verb == "list" || verb == "ls" || verb == "agents":
		views := a.agentViews()
		if len(views) == 0 {
			return []string{a.T("term_list_empty")}
		}
		var out []string
		for _, v := range views {
			out = append(out, fmt.Sprintf("%s%s %-10s %-8s %s", strings.Repeat("  ", v.Depth), statusIcon[v.Status], v.Name, v.Status, truncate(v.Activity, 70)))
		}
		return out
	case verb == "usage":
		return strings.Split(a.usageLine(), "\n")
	case verb == "pause":
		return forTargets(rest, a.pauseAgent)
	case verb == "resume" || verb == "unpause" || verb == "continue":
		return forTargets(rest, a.resumeAgent)
	case verb == "stop" || verb == "kill":
		return forTargets(rest, a.stopAgent)
	case verb == "delete" || verb == "rm":
		return forTargets(rest, a.deleteAgent)
	case verb == "spawn":
		name, mission, ok := strings.Cut(rest, ":")
		if !ok {
			return []string{"usage: spawn <name>: <mission>"}
		}
		ag, err := a.startMission(strings.TrimSpace(mission), strings.TrimSpace(name), "", "", nil)
		if err != nil {
			return []string{"❌ " + err.Error()}
		}
		return []string{a.T("term_mission", ag.Name)}
	case strings.HasPrefix(verb, "@"):
		ag := a.agentByRef(verb)
		if ag == nil {
			return []string{a.T("unknown_agent", verb)}
		}
		if ag.Status == "stopped" {
			return []string{"❌ " + ag.Name + " is stopped"}
		}
		if rest == "" {
			return []string{"usage: @agent <message>"}
		}
		a.addLog(ag, "in", "you: "+rest)
		a.deliver(ag, fmt.Sprintf(dict["en"]["user_msg"], rest), true)
		return []string{a.T("queued_msg", ag.Name)}
	case verb == "mission":
		input = rest
		fallthrough
	default:
		ag, err := a.startMission(input, "", "", "", nil)
		if err != nil {
			return []string{"❌ " + err.Error()}
		}
		return []string{a.T("term_mission", ag.Name)}
	}
}
