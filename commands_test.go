package main

import (
	"strings"
	"testing"
)

func newTestApp(t *testing.T) *App {
	t.Setenv("AGENTHQ_HOME", t.TempDir())
	cfg := defaultConfig()
	cfg.Workspace = t.TempDir()
	return &App{cfg: cfg, agents: map[string]*Agent{}, tgMsgs: map[int64]string{}, usage: newUsageTracker(), secret: "x", demo: true}
}

func TestCommandParsing(t *testing.T) {
	a := newTestApp(t)
	missions := func() int { return len(a.order) }

	// sentences that start with a command word are missions
	for _, in := range []string{
		"list my github repos and send me a message on telegram",
		"pause the backups tonight and tell me",
		"stop using jest, switch to vitest",
		"help me write a README",
		"usage report for last month",
	} {
		before := missions()
		out := a.runCommand(in)
		if missions() != before+1 || !strings.Contains(strings.Join(out, " "), "Mission started") {
			t.Fatalf("%q should start a mission, got %v", in, out)
		}
	}
	first := a.agents[a.order[0]].Name

	// real commands
	if out := a.runCommand("list"); missions() != 5 || len(out) != 5 {
		t.Fatalf("list: %v", out)
	}
	if out := a.runCommand("help"); !strings.Contains(strings.Join(out, "\n"), "clear done") {
		t.Fatalf("help: %v", out)
	}
	a.runCommand("pause " + first)
	if s := a.agents[a.order[0]].Status; s != "paused" {
		t.Fatalf("pause: %s", s)
	}
	a.runCommand("resume " + first)
	if s := a.agents[a.order[0]].Status; s == "paused" {
		t.Fatalf("resume: %s", s)
	}
	if out := a.runCommand("@" + first + " hello"); !strings.Contains(out[0], first) {
		t.Fatalf("@msg: %v", out)
	}
	if out := a.runCommand("pause"); !strings.HasPrefix(out[0], "usage") {
		t.Fatalf("bare pause: %v", out)
	}
	a.runCommand("stop all")
	for _, id := range a.order {
		if a.agents[id].Status != "stopped" {
			t.Fatalf("stop all left %s %s", a.agents[id].Name, a.agents[id].Status)
		}
	}
	if out := a.runCommand("clear done"); !strings.Contains(out[0], "5") || missions() != 0 {
		t.Fatalf("clear done: %v, %d left", out, missions())
	}
}
