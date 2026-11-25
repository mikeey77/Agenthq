package main

// System check: quick health checks of everything Agent HQ depends on.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Check struct {
	ID     string `json:"id"`
	OK     *bool  `json:"ok"` // nil = optional and not set up
	Detail string `json:"detail"`
}

func boolp(b bool) *bool { return &b }

func (a *App) runChecks() []Check {
	a.mu.Lock()
	cfg := a.cfg
	claude := a.claudeBin()
	ntoken, gtoken := a.netlifyToken(), a.githubToken()
	secret := a.secret
	a.mu.Unlock()
	var (
		mu  sync.Mutex
		out = map[string]Check{}
		wg  sync.WaitGroup
	)
	add := func(id string, ok *bool, detail string) {
		mu.Lock()
		out[id] = Check{id, ok, truncate(detail, 200)}
		mu.Unlock()
	}
	run := func(f func()) { wg.Add(1); go func() { defer wg.Done(); f() }() }

	run(func() {
		if claude == "" {
			add("claude", boolp(false), "Claude Code not found")
			return
		}
		v, err := runIn(os.TempDir(), 30*time.Second, claude, "--version")
		add("claude", boolp(err == nil), strings.TrimSpace(firstLine(v)))
	})
	run(func() { // our own MCP server, exactly as agents reach it
		body := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
		resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d/mcp/selftest?k=%s", cfg.Port, secret), "application/json", strings.NewReader(body))
		if err != nil {
			add("mcp", boolp(false), err.Error())
			return
		}
		defer resp.Body.Close()
		var r struct {
			Result struct{ Tools []any } `json:"result"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&r)
		add("mcp", boolp(len(r.Result.Tools) > 10), fmt.Sprintf("%d tools", len(r.Result.Tools)))
	})
	run(func() {
		p := filepath.Join(cfg.Workspace, ".agenthq-write-test")
		err := os.MkdirAll(cfg.Workspace, 0o755)
		if err == nil {
			err = os.WriteFile(p, []byte("ok"), 0o644)
			os.Remove(p)
		}
		if err != nil {
			add("workspace", boolp(false), err.Error())
		} else {
			add("workspace", boolp(true), cfg.Workspace)
		}
	})
	run(func() {
		if cfg.BotToken == "" {
			add("telegram", nil, "not set up")
			return
		}
		res, err := a.tgCall("getMe", map[string]any{})
		if err != nil {
			add("telegram", boolp(false), err.Error())
			return
		}
		me, _ := res["result"].(map[string]any)
		if cfg.ChatID == "" {
			add("telegram", boolp(false), "@"+str(me, "username")+": send /start to the bot")
			return
		}
		add("telegram", boolp(true), "@"+str(me, "username"))
	})
	run(func() {
		if gtoken == "" {
			add("github", nil, "no token")
			return
		}
		login, err := ghLogin(gtoken)
		if err != nil {
			add("github", boolp(false), err.Error())
			return
		}
		add("github", boolp(true), "@"+login)
	})
	run(func() {
		if ntoken == "" {
			add("netlify", nil, "no token")
			return
		}
		u, err := netlifyReq(ntoken, "GET", "/user", "", nil)
		if err != nil {
			add("netlify", boolp(false), err.Error())
			return
		}
		add("netlify", boolp(true), str(u, "email"))
	})
	run(func() {
		if findBrowserExe() == "" {
			add("browser", boolp(false), "Edge or Chrome not found")
			return
		}
		img, err := a.browser.RenderHTML(`<html><body><div class="card" style="height:40px">ok</div></body></html>`, 200)
		if err != nil || len(img) == 0 {
			add("browser", boolp(false), fmt.Sprint(err))
			return
		}
		add("browser", boolp(true), "Edge/Chrome works")
	})
	run(func() {
		w, f, m := voiceTools()
		if w == "" || f == "" || m == "" {
			add("voice", nil, "not installed")
			return
		}
		add("voice", boolp(true), "whisper + ffmpeg")
	})
	run(func() {
		if p, err := exec.LookPath("git"); err == nil {
			v, _ := runIn(os.TempDir(), 15*time.Second, p, "--version")
			add("git", boolp(true), strings.TrimSpace(firstLine(v)))
		} else {
			add("git", boolp(false), "not installed (needed for GitHub)")
		}
	})
	run(func() {
		if _, err := exec.LookPath("npx"); err == nil {
			v, _ := runIn(os.TempDir(), 15*time.Second, "node", "--version")
			add("node", boolp(true), "Node "+strings.TrimSpace(firstLine(v)))
		} else {
			add("node", nil, "not installed (needed for most connectors)")
		}
	})
	wg.Wait()
	order := []string{"claude", "mcp", "workspace", "telegram", "browser", "git", "node", "github", "netlify", "voice"}
	res := []Check{}
	for _, id := range order {
		if c, ok := out[id]; ok {
			res = append(res, c)
		}
	}
	return res
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// claudeBin is the Claude Code executable that agents use. Caller holds a.mu.
func (a *App) claudeBin() string { return detectClaude(a.cfg.ClaudePath) }

func (a *App) selftestRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/selftest", func(w http.ResponseWriter, r *http.Request) {
		if a.demo {
			writeJSON(w, map[string]any{"checks": demoChecks()})
			return
		}
		writeJSON(w, map[string]any{"checks": a.runChecks()})
	})
}

func demoChecks() []Check {
	ok := boolp(true)
	return []Check{{"claude", ok, "2.1.40 (Claude Code)"}, {"mcp", ok, "29 tools"}, {"workspace", ok, `C:\Users\Michael\AgentHQ`},
		{"telegram", ok, "@michael_hq_bot"}, {"browser", ok, "Edge/Chrome works"}, {"git", ok, "git version 2.47.1.windows.1"},
		{"node", ok, "Node v22.11.0"}, {"github", ok, "@mikeey77"}, {"netlify", nil, "no token"}, {"voice", nil, "not installed"}}
}
