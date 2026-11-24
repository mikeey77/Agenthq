// Agent HQ — command center for Claude Code agents, with Telegram control.
package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

//go:embed index.html
var indexHTML []byte

//go:embed fonts
var fontFS embed.FS

const appName = "AgentHQ"

var version = "2.2.0" // overridden at release build time with -ldflags "-X main.version=..."

type TermLine struct {
	T    time.Time `json:"t"`
	Kind string    `json:"k"` // in | out
	Text string    `json:"x"`
}

type App struct {
	mu        sync.Mutex
	cfg       Config
	agents    map[string]*Agent
	order     []string
	decisions []*Decision
	events    []Event
	evSeq     int64
	dirty     bool
	secret    string
	tg        TelegramState
	tgMsgs    map[int64]string
	usage     *UsageTracker
	external  External
	claudeDir string
	demo      bool
	term      []TermLine
	upd       UpdateInfo
}

func (a *App) termAdd(kind string, lines ...string) {
	for _, l := range lines {
		a.term = append(a.term, TermLine{T: time.Now(), Kind: kind, Text: l})
	}
	if len(a.term) > 300 {
		a.term = a.term[len(a.term)-300:]
	}
}

// ---------- settings.json / autostart / window ----------

func (a *App) enableTeams() error {
	p := filepath.Join(a.claudeDir, "settings.json")
	m := map[string]any{}
	if b, err := os.ReadFile(p); err == nil {
		if len(bytes.TrimSpace(b)) > 0 {
			if err := json.Unmarshal(b, &m); err != nil {
				return fmt.Errorf("settings.json is not valid JSON, not touching it: %v", err)
			}
		}
		_ = os.WriteFile(p+".bak", b, 0o644)
	}
	env, _ := m["env"].(map[string]any)
	if env == nil {
		env = map[string]any{}
	}
	env["CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS"] = "1"
	m["env"] = env
	_ = os.MkdirAll(a.claudeDir, 0o755)
	b, _ := json.MarshalIndent(m, "", "  ")
	return os.WriteFile(p, b, 0o644)
}

const runKey = `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`

func autostartEnabled() bool {
	if runtime.GOOS != "windows" {
		return false
	}
	return hiddenCmd("reg", "query", runKey, "/v", appName).Run() == nil
}

func setAutostart(on bool) error {
	if runtime.GOOS != "windows" {
		return errors.New("Windows only")
	}
	if on {
		exe, _ := os.Executable()
		return hiddenCmd("reg", "add", runKey, "/v", appName, "/t", "REG_SZ", "/d", `"`+exe+`" --background`, "/f").Run()
	}
	return hiddenCmd("reg", "delete", runKey, "/v", appName, "/f").Run()
}

func openUI(u string) {
	if runtime.GOOS == "windows" {
		for _, p := range []string{
			os.Getenv("ProgramFiles(x86)") + `\Microsoft\Edge\Application\msedge.exe`,
			os.Getenv("ProgramFiles") + `\Microsoft\Edge\Application\msedge.exe`,
			os.Getenv("ProgramFiles") + `\Google\Chrome\Application\chrome.exe`,
			os.Getenv("LocalAppData") + `\Google\Chrome\Application\chrome.exe`,
		} {
			if _, err := os.Stat(p); err == nil {
				if exec.Command(p, "--app="+u, "--window-size=1500,960").Start() == nil {
					return
				}
			}
		}
		_ = hiddenCmd("rundll32", "url.dll,FileProtocolHandler", u).Start()
		return
	}
	_ = exec.Command("xdg-open", u).Start()
}

// ---------- http ----------

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func readBody(r *http.Request, v any) error { return json.NewDecoder(r.Body).Decode(v) }

func (a *App) stateJSON() map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	views := a.agentViews()
	stats := map[string]any{}
	var running, waiting, live, done int
	var cost float64
	var tok int64
	for _, ag := range a.agents {
		switch ag.Status {
		case "running":
			running++
		case "waiting":
			waiting++
		case "done":
			done++
		}
		if isLive(ag.Status) {
			live++
		}
		cost += ag.Cost
		tok += ag.InTok + ag.OutTok
	}
	stats["running"], stats["waiting"], stats["live"], stats["done"], stats["cost"], stats["tokens"] = running, waiting, live, done, cost, tok
	var recent []*Decision
	for i := len(a.decisions) - 1; i >= 0 && len(recent) < 12; i-- {
		if a.decisions[i].Status != "pending" {
			recent = append(recent, a.decisions[i])
		}
	}
	ev := a.events
	if ev == nil {
		ev = []Event{}
	}
	if len(ev) > 120 {
		ev = ev[:120]
	}
	tg := a.tg
	tg.Configured = a.cfg.BotToken != ""
	tg.Linked = tg.Configured && a.cfg.ChatID != ""
	conns := []map[string]any{}
	for _, c := range a.cfg.Connectors {
		conns = append(conns, map[string]any{"id": c.ID, "name": c.Name, "enabled": c.Enabled, "tools": len(c.Tools), "error": c.Error, "preset": c.Preset})
	}
	claude := detectClaude(a.cfg.ClaudePath)
	if a.demo {
		claude = `C:\Users\Michael\.local\bin\claude.exe`
	}
	return map[string]any{
		"version": version, "lang": a.cfg.Lang, "theme": a.cfg.Theme, "setupDone": a.cfg.SetupDone, "demo": a.demo,
		"claudePath": claude, "claudeFound": claude != "",
		"agents": views, "decisions": a.pendingDecisions(), "recentDecisions": recent,
		"events": ev, "external": a.external, "telegram": tg, "usage": a.usage.Summary(a.cfg.TokenBudget),
		"stats": stats, "connectors": conns, "update": func() UpdateInfo { u := a.upd; u.Current = version; return u }(), "autoUpdate": a.cfg.AutoUpdate, "autostart": autostartEnabled(), "canAutostart": runtime.GOOS == "windows",
		"permissionMode": a.cfg.PermissionMode, "workspace": a.cfg.Workspace, "term": append([]TermLine{}, a.term...),
		"limits": map[string]int{"maxAgents": a.cfg.MaxAgents, "maxRunning": a.cfg.MaxRunning},
		"now":    time.Now(),
	}
}

func (a *App) routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(indexHTML)
	})
	mux.HandleFunc("/mcp/", a.handleMCP)
	mux.Handle("/fonts/", http.FileServer(http.FS(fontFS)))
	a.connectorRoutes(mux)
	a.updateRoutes(mux)
	mux.HandleFunc("/api/state", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, a.stateJSON()) })
	mux.HandleFunc("/api/agent/log", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		ag := a.agents[r.URL.Query().Get("id")]
		if ag == nil {
			http.Error(w, "not found", 404)
			return
		}
		writeJSON(w, map[string]any{"log": ag.Log, "inbox": ag.Inbox})
	})
	mux.HandleFunc("/api/command", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Text string }
		_ = readBody(r, &in)
		a.mu.Lock()
		a.termAdd("in", in.Text)
		a.mu.Unlock()
		if strings.EqualFold(strings.TrimSpace(in.Text), "clear") {
			a.mu.Lock()
			a.term = nil
			a.mu.Unlock()
			writeJSON(w, map[string]any{"lines": []string{}})
			return
		}
		out := a.runCommand(in.Text)
		a.mu.Lock()
		a.termAdd("out", out...)
		a.mu.Unlock()
		writeJSON(w, map[string]any{"lines": out})
	})
	mux.HandleFunc("/api/mission", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Mission, Name, Cwd, Model string
			Connectors                []string
		}
		_ = readBody(r, &in)
		a.mu.Lock()
		ag, err := a.startMission(in.Mission, in.Name, strings.TrimSpace(in.Cwd), in.Model, in.Connectors)
		if err == nil {
			a.termAdd("out", a.T("term_mission", ag.Name))
		}
		a.mu.Unlock()
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		writeJSON(w, map[string]string{"id": ag.ID, "name": ag.Name})
	})
	mux.HandleFunc("/api/agent", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ ID, Action, Text string }
		_ = readBody(r, &in)
		a.mu.Lock()
		defer a.mu.Unlock()
		ag := a.agents[in.ID]
		if ag == nil {
			http.Error(w, "not found", 404)
			return
		}
		switch in.Action {
		case "pause":
			a.pauseAgent(ag)
		case "resume":
			a.resumeAgent(ag)
		case "stop":
			a.stopAgent(ag)
		case "delete":
			a.deleteAgent(ag)
		case "message":
			if ag.Status == "stopped" || strings.TrimSpace(in.Text) == "" {
				http.Error(w, "cannot message", 400)
				return
			}
			a.addLog(ag, "in", "you: "+in.Text)
			a.deliver(ag, fmt.Sprintf(dict["en"]["user_msg"], in.Text), true)
		}
		writeJSON(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("/api/clear", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		n := a.clearFinished()
		a.mu.Unlock()
		writeJSON(w, map[string]int{"cleared": n})
	})
	mux.HandleFunc("/api/decision", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ ID, Answer string }
		_ = readBody(r, &in)
		a.mu.Lock()
		err := a.answerDecision(in.ID, in.Answer, "dashboard")
		a.mu.Unlock()
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("/api/config", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		if r.Method == http.MethodPost {
			var in map[string]any
			if err := readBody(r, &in); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			if t, ok := in["botToken"].(string); ok && strings.Contains(t, "•") {
				delete(in, "botToken")
			}
			if t, ok := in["botToken"].(string); ok && t != a.cfg.BotToken {
				in["chatId"] = "" // new bot: link again via /start
				in["botToken"] = strings.TrimSpace(t)
			}
			cur, _ := json.Marshal(a.cfg)
			var m map[string]any
			_ = json.Unmarshal(cur, &m)
			for k, v := range in {
				m[k] = v
			}
			nb, _ := json.Marshal(m)
			nc := a.cfg
			if err := json.Unmarshal(nb, &nc); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			nc.Port = a.cfg.Port
			a.cfg = nc
			if !a.demo {
				if err := saveConfig(a.cfg); err != nil {
					http.Error(w, err.Error(), 500)
					return
				}
			}
		}
		c := a.cfg
		c.Connectors = nil // served (masked) by /api/connectors
		if len(c.BotToken) > 10 {
			c.BotToken = c.BotToken[:6] + "••••••" + c.BotToken[len(c.BotToken)-3:]
		}
		writeJSON(w, c)
	})
	mux.HandleFunc("/api/claude/test", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Path string }
		_ = readBody(r, &in)
		p := detectClaude(in.Path)
		if p == "" {
			http.Error(w, "Claude Code CLI not found", 404)
			return
		}
		cmd := hiddenCmd(p, "--version")
		var buf bytes.Buffer
		cmd.Stdout, cmd.Stderr = &buf, &buf
		err := cmd.Start()
		if err == nil {
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			select {
			case err = <-done:
			case <-time.After(20 * time.Second):
				_ = cmd.Process.Kill()
				err = errors.New("timeout")
			}
		}
		out := buf.Bytes()
		if err != nil {
			http.Error(w, strings.TrimSpace(string(out)+" "+err.Error()), 500)
			return
		}
		writeJSON(w, map[string]string{"path": p, "version": strings.TrimSpace(string(out))})
	})
	mux.HandleFunc("/api/telegram/test", func(w http.ResponseWriter, r *http.Request) {
		chat := a.chatID()
		if chat == "" {
			http.Error(w, "Not linked yet: send /start to your bot", 400)
			return
		}
		a.mu.Lock()
		msg := a.T("tg_test")
		a.mu.Unlock()
		if _, err := a.tgSendTo(chat, msg, false, nil); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("/api/enable-teams", func(w http.ResponseWriter, r *http.Request) {
		if err := a.enableTeams(); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("/api/autostart", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Enabled bool }
		_ = readBody(r, &in)
		if err := setAutostart(in.Enabled); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("/api/quit", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]bool{"ok": true})
		go func() {
			a.mu.Lock()
			for _, ag := range a.agents {
				if isActive(ag.Status) {
					ag.NeedsContinue = true
					a.killAgent(ag)
					ag.Status = "paused"
				}
			}
			a.saveState()
			a.mu.Unlock()
			time.Sleep(300 * time.Millisecond)
			os.Exit(0)
		}()
	})
	return mux
}

func main() {
	background, demo, updated := false, false, false
	for _, arg := range os.Args[1:] {
		switch arg {
		case "--background":
			background = true
		case "--demo":
			demo = true
		case "--updated":
			background, updated = true, true
		}
	}
	_ = os.MkdirAll(configDir(), 0o755)
	if lf, err := os.OpenFile(filepath.Join(configDir(), "agenthq.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
		log.SetOutput(lf)
	}
	app := &App{cfg: loadConfig(), agents: map[string]*Agent{}, tgMsgs: map[int64]string{},
		usage: newUsageTracker(), claudeDir: claudeDir(), secret: newID() + newID(), demo: demo}
	if v := os.Getenv("AGENTHQ_SECRET"); v != "" {
		app.secret = v
	}
	addr := fmt.Sprintf("127.0.0.1:%d", app.cfg.Port)
	uiURL := "http://" + addr
	ln, err := net.Listen("tcp", addr)
	for i := 0; err != nil && updated && i < 40; i++ { // the old version is still shutting down
		time.Sleep(250 * time.Millisecond)
		ln, err = net.Listen("tcp", addr)
	}
	if err != nil { // already running: just open the window
		if !background {
			openUI(uiURL)
		}
		return
	}
	if demo {
		app.seedDemo()
	} else {
		app.loadState()
		app.mu.Lock()
		app.emit("info", "", app.T("started"))
		app.schedule()
		app.mu.Unlock()
		go cleanupOldExe()
		go app.updateLoop()
	}
	app.external = scanExternal(app.claudeDir)
	go func() {
		for {
			app.usage.Update(app.claudeDir)
			time.Sleep(15 * time.Second)
		}
	}()
	go func() {
		for {
			time.Sleep(2 * time.Second)
			ex := scanExternal(app.claudeDir)
			app.mu.Lock()
			if !app.demo {
				app.external = ex
			}
			if app.dirty {
				app.saveState()
			}
			app.mu.Unlock()
		}
	}()
	go app.telegramLoop()
	if !background && os.Getenv("AGENTHQ_NO_BROWSER") == "" {
		go func() { time.Sleep(400 * time.Millisecond); openUI(uiURL) }()
	}
	log.Printf("Agent HQ %s on %s", version, uiURL)
	log.Fatal(http.Serve(ln, app.routes()))
}
