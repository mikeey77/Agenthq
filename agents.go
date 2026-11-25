package main

// Agent orchestrator: every agent is a headless Claude Code process (`claude -p`, stream-json)
// that runs on the user's own Claude login. Turns are resumed with --resume <session>.

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
)

type LogEntry struct {
	T    time.Time `json:"t"`
	Kind string    `json:"k"` // text | tool | info | error | in | result
	Text string    `json:"x"`
}

type Agent struct {
	ID                 string     `json:"id"`
	Name               string     `json:"name"`
	Role               string     `json:"role"`
	Mission            string     `json:"mission"`
	ParentID           string     `json:"parentId"`
	Cwd                string     `json:"cwd"`
	Model              string     `json:"model"`
	Color              string     `json:"color"`
	Kind               string     `json:"kind"`       // lead research build test write review web design data ops other
	ToolKind           string     `json:"toolKind"`   // browser web search file edit terminal app agent
	ToolTarget         string     `json:"toolTarget"` // url host, file name, command, connector
	ProjectID          string     `json:"projectId,omitempty"`
	TrustAll           bool       `json:"trustAll,omitempty"` // user said: stop asking for this agent
	ReviewOf           string     `json:"reviewOf,omitempty"` // lead whose work this agent reviews
	TaskID             string     `json:"taskId,omitempty"`
	Files              []string   `json:"files,omitempty"`       // files it created or changed, newest first
	LimitPaused        bool       `json:"limitPaused,omitempty"` // parked until the usage limit resets
	LimitUntil         time.Time  `json:"limitUntil,omitempty"`
	Status             string     `json:"status"` // queued running waiting paused idle done stopped error
	SessionID          string     `json:"sessionId"`
	Activity           string     `json:"activity"`
	Summary            string     `json:"summary"`
	Depth              int        `json:"depth"`
	Created            time.Time  `json:"created"`
	Updated            time.Time  `json:"updated"`
	TurnStart          time.Time  `json:"turnStart"`
	Cost               float64    `json:"cost"`
	Turns              int        `json:"turns"`
	AutoTurns          int        `json:"autoTurns"`
	InTok              int64      `json:"inTok"`
	OutTok             int64      `json:"outTok"`
	ToolCalls          int        `json:"toolCalls"`
	Inbox              []string   `json:"inbox"`
	Connectors         []string   `json:"connectors"` // nil = all enabled
	NeedsContinue      bool       `json:"needsContinue"`
	ResumeAfterRestart bool       `json:"resumeAfterRestart"`
	Completed          bool       `json:"completed"`
	Log                []LogEntry `json:"log"`

	cmd         *exec.Cmd
	interrupted bool
	mcpRetried  bool
	lastResult  string
	resultErr   bool
	gotResult   bool
}

type Decision struct {
	ID         string    `json:"id"`
	AgentID    string    `json:"agentId"`
	AgentName  string    `json:"agentName"`
	Kind       string    `json:"kind"` // decision | permission
	Question   string    `json:"question"`
	Detail     string    `json:"detail"`
	Options    []string  `json:"options"`
	Created    time.Time `json:"created"`
	Status     string    `json:"status"` // pending answered expired
	Answer     string    `json:"answer"`
	AnsweredAt time.Time `json:"answeredAt"`
	Via        string    `json:"via"`

	ch      chan string
	late    bool
	tgMsgID int64
}

type Event struct {
	ID      int64     `json:"id"`
	Time    time.Time `json:"time"`
	Level   string    `json:"level"` // update | decision | critical | info
	Text    string    `json:"text"`
	AgentID string    `json:"agentId"`
}

var agentNames = []string{"atlas", "nova", "orion", "vega", "echo", "juno", "kepler", "lyra", "onyx", "pixel", "quill", "rune", "sage", "titan", "zephyr", "bolt", "cobalt", "ember", "flux", "halo", "iris", "jade", "koda", "mira"}

// Colors carry meaning: they show what kind of work an agent does.
var roleKinds = []struct {
	kind, color string
	words       []string
}{
	{"test", "#f5c451", []string{"test", "qa", "verif", "check", "controle", "debug"}},
	{"review", "#5fd7d7", []string{"review", "audit", "critic", "beoordel", "security"}},
	{"web", "#38bdf8", []string{"browser", "web", "scrap", "crawl", "shop", "deal", "price", "prijs"}},
	{"research", "#6aa7ff", []string{"research", "onderzo", "analy", "find", "zoek", "scout", "investig"}},
	{"design", "#ff8fa3", []string{"design", "ui", "ux", "visual", "art", "ontwerp"}},
	{"write", "#c792ea", []string{"writ", "schrijf", "copy", "doc", "content", "edit", "translat", "vertaal"}},
	{"data", "#a3d977", []string{"data", "sql", "excel", "sheet", "stat", "financ"}},
	{"ops", "#f0a37f", []string{"deploy", "devops", "server", "install", "setup", "infra", "ops"}},
	{"build", "#3ecf8e", []string{"build", "dev", "code", "program", "engineer", "bouw", "implement", "fix", "maker"}},
}

func roleKind(ag *Agent) (string, string) {
	if ag.ParentID == "" {
		return "lead", "#ff8a4c"
	}
	words := strings.FieldsFunc(strings.ToLower(ag.Role+" "+ag.Name), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	})
	for _, k := range roleKinds {
		for _, kw := range k.words {
			for _, w := range words {
				if strings.HasPrefix(w, kw) {
					return k.kind, k.color
				}
			}
		}
	}
	return "other", "#9aa3b2"
}

// toolKind turns a tool call into the little activity icon shown next to the robot.
func toolKind(name string, in map[string]any) (string, string) {
	base := func(p string) string { return filepath.Base(strings.ReplaceAll(p, "\\", "/")) }
	switch {
	case strings.HasPrefix(name, "mcp__hq__browser_"), strings.HasPrefix(name, "mcp__playwright"), strings.Contains(name, "browser"):
		return "browser", host(str(in, "url"))
	case strings.HasPrefix(name, "mcp__hq__"):
		return "", ""
	case strings.HasPrefix(name, "mcp__"):
		parts := strings.SplitN(strings.TrimPrefix(name, "mcp__"), "__", 2)
		return "app", parts[0]
	}
	switch name {
	case "Bash", "PowerShell":
		f := strings.Fields(str(in, "command"))
		if len(f) > 0 {
			return "terminal", truncate(base(f[0]), 24)
		}
		return "terminal", ""
	case "Read", "Glob", "Grep", "LS":
		return "file", truncate(base(str(in, "file_path", "path", "pattern")), 28)
	case "Edit", "Write", "MultiEdit", "NotebookEdit":
		return "edit", truncate(base(str(in, "file_path", "notebook_path")), 28)
	case "WebSearch":
		return "search", truncate(str(in, "query"), 28)
	case "WebFetch":
		return "web", host(str(in, "url"))
	case "Task":
		return "agent", truncate(str(in, "description"), 28)
	}
	return "", ""
}

func newID() string {
	b := make([]byte, 5)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func slug(s string) string {
	s = strings.Trim(slugRe.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if len(s) > 32 {
		s = strings.Trim(s[:32], "-")
	}
	if s == "" {
		s = "mission"
	}
	return s
}

func isActive(st string) bool { return st == "running" || st == "waiting" }
func isLive(st string) bool   { return st != "stopped" && st != "done" && st != "error" }

// ---------- lookup ----------

func (a *App) agentByRef(ref string) *Agent {
	ref = strings.TrimPrefix(strings.TrimSpace(ref), "@")
	if ag, ok := a.agents[ref]; ok {
		return ag
	}
	for _, id := range a.order {
		if strings.EqualFold(a.agents[id].Name, ref) {
			return a.agents[id]
		}
	}
	return nil
}

func (a *App) children(id string) []*Agent {
	var out []*Agent
	for _, cid := range a.order {
		if c := a.agents[cid]; c.ParentID == id {
			out = append(out, c)
		}
	}
	return out
}

func (a *App) uniqueName(want string) string {
	want = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(want, "@")))
	want = strings.Trim(slugRe.ReplaceAllString(want, "-"), "-")
	taken := func(n string) bool { return a.agentByRef(n) != nil }
	if want == "" {
		for _, n := range agentNames {
			if !taken(n) {
				return n
			}
		}
		want = "agent"
	}
	if !taken(want) {
		return want
	}
	for i := 2; ; i++ {
		if n := fmt.Sprintf("%s-%d", want, i); !taken(n) {
			return n
		}
	}
}

func (a *App) addLog(ag *Agent, kind, text string) {
	ag.Log = append(ag.Log, LogEntry{T: time.Now(), Kind: kind, Text: text})
	if len(ag.Log) > 400 {
		ag.Log = ag.Log[len(ag.Log)-400:]
	}
	ag.Updated = time.Now()
	a.dirty = true
}

// emit records an event and pushes it to Telegram. Caller holds a.mu.
func (a *App) emit(level, agentID, text string) {
	a.evSeq++
	ev := Event{ID: a.evSeq, Time: time.Now(), Level: level, Text: text, AgentID: agentID}
	a.events = append([]Event{ev}, a.events...)
	if len(a.events) > 300 {
		a.events = a.events[:300]
	}
	a.dirty = true
	c := a.cfg
	if c.BotToken == "" || c.ChatID == "" || a.demo || a.tgQuiet {
		return
	}
	switch level {
	case "usage": // limit reached / reset / almost used up
		if c.TgCritical || c.TgUpdates {
			go a.tgSendText(text, false, nil)
		}
	case "update":
		if c.TgUpdates || (agentID != "" && agentID == a.tgFocus) {
			go a.tgAgentSend(agentID, "🟢 "+text, c.TgSilent && agentID != a.tgFocus, nil)
		}
	case "shot": // update with a picture of the agent's browser page
		if c.TgUpdates {
			go func() {
				if img, err := a.browser.Screenshot(agentID, ""); err == nil && len(img) > 0 {
					if _, err := a.tgSendPhoto(img, "🟢 "+text, c.TgSilent, nil); err == nil {
						return
					}
				}
				a.tgSendText("🟢 "+text, c.TgSilent, nil)
			}()
		}
	case "limit": // usage limit reached: card with the reset time
		if c.TgCritical || c.TgUpdates {
			go a.tgCard(a.limitCard(), text, false, nil)
		}
	case "done": // a mission lead finished: send a result card
		if c.TgUpdates || agentID == a.tgFocus {
			if ag := a.agents[agentID]; ag != nil {
				go a.tgCardFor(agentID, a.doneCard(ag), "✅ "+text, c.TgSilent, nil)
			} else {
				go a.tgSendText("✅ "+text, c.TgSilent, nil)
			}
		}
	case "chatter":
		if c.TgChatter {
			go a.tgSendText("💬 "+text, true, nil)
		}
	case "critical":
		if c.TgCritical {
			var kb any
			if agentID != "" {
				kb = map[string]any{"inline_keyboard": [][]map[string]string{{
					{"text": a.T("btn_pause"), "callback_data": "a:" + agentID + ":pause"},
					{"text": a.T("btn_stop"), "callback_data": "a:" + agentID + ":stop"},
				}}}
			}
			go a.tgAgentSend(agentID, "🚨 "+a.T("critical")+"\n"+text, false, kb)
		}
	}
}

// ---------- create ----------

func (a *App) createAgent(name, role, mission string, parent *Agent, cwd, model string, conns []string) (*Agent, error) {
	live := 0
	for _, ag := range a.agents {
		if isLive(ag.Status) {
			live++
		}
	}
	if live >= a.cfg.MaxAgents {
		return nil, errors.New(a.T("limit_agents", a.cfg.MaxAgents))
	}
	depth := 0
	if parent != nil {
		depth = parent.Depth + 1
		if depth >= a.cfg.MaxDepth {
			return nil, errors.New(a.T("limit_depth", a.cfg.MaxDepth))
		}
		if cwd == "" {
			cwd = parent.Cwd
		}
		if model == "" {
			model = parent.Model
		}
		conns = parent.Connectors
	}
	projectID, taskID := a.nextProject, a.nextTask // set by callers starting project work
	a.nextProject, a.nextTask = "", ""
	if parent != nil {
		projectID, taskID = parent.ProjectID, ""
	}
	if model == "" {
		model = a.cfg.DefaultModel
	}
	if role == "" {
		role = "Agent"
	}
	ag := &Agent{
		ID: newID(), Name: a.uniqueName(name), Role: role, Mission: mission, Cwd: cwd, Model: model,
		Status: "queued", Depth: depth,
		Created: time.Now(), Updated: time.Now(), Connectors: conns, ProjectID: projectID, TaskID: taskID,
	}
	if parent != nil {
		ag.ParentID = parent.ID
	}
	ag.Kind, ag.Color = roleKind(ag)
	_ = os.MkdirAll(cwd, 0o755)
	a.agents[ag.ID] = ag
	a.order = append(a.order, ag.ID)
	a.addLog(ag, "in", mission)
	if parent != nil {
		a.emit("update", ag.ID, a.T("spawned", parent.Name, ag.Name, role))
	} else {
		a.emit("update", ag.ID, a.T("mission_new", ag.Name, truncate(mission, 160)))
	}
	a.schedule()
	return ag, nil
}

func (a *App) startMission(mission, name, cwd, model string, conns []string) (*Agent, error) {
	if strings.TrimSpace(mission) == "" {
		return nil, errors.New("empty mission")
	}
	if cwd == "" {
		cwd = filepath.Join(a.cfg.Workspace, time.Now().Format("0102")+"-"+slug(truncate(mission, 40)))
	}
	return a.createAgent(name, "Mission lead", mission, nil, cwd, model, conns)
}

// ---------- scheduling ----------

func (a *App) schedule() {
	if a.demo || a.limited() {
		return
	}
	running := 0
	for _, ag := range a.agents {
		if isActive(ag.Status) {
			running++
		}
	}
	for _, id := range a.order {
		if running >= a.cfg.MaxRunning {
			return
		}
		ag := a.agents[id]
		if ag.Status != "queued" || ag.cmd != nil {
			continue
		}
		if err := a.startTurn(ag); err != nil {
			ag.Status = "error"
			a.addLog(ag, "error", err.Error())
			a.emit("critical", ag.ID, a.T("crashed", ag.Name, err.Error()))
			continue
		}
		running++
	}
}

func detectClaude(pref string) string {
	if pref != "" {
		if _, err := os.Stat(pref); err == nil {
			return pref
		}
	}
	if p, err := exec.LookPath("claude"); err == nil {
		return p
	}
	h, _ := os.UserHomeDir()
	cands := []string{filepath.Join(h, ".local", "bin", "claude"), filepath.Join(h, ".claude", "local", "claude"), "/usr/local/bin/claude", "/opt/homebrew/bin/claude"}
	if runtime.GOOS == "windows" {
		cands = []string{
			filepath.Join(h, ".local", "bin", "claude.exe"),
			filepath.Join(os.Getenv("APPDATA"), "npm", "claude.cmd"),
			filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "claude", "claude.exe"),
		}
	}
	for _, c := range cands {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

func (a *App) systemPrompt(ag *Agent) string {
	lang := map[string]string{"en": "English", "nl": "Dutch"}[a.cfg.Lang]
	if lang == "" {
		lang = "English"
	}
	parent := "You report to the user directly (you are a top-level mission lead)."
	if p := a.agents[ag.ParentID]; p != nil {
		parent = fmt.Sprintf("You were created by agent %q, who is your parent. Your final result is delivered to them automatically.", p.Name)
	}
	canSpawn := "You may create sub-agents."
	if ag.Depth+1 >= a.cfg.MaxDepth {
		canSpawn = "You may NOT create sub-agents (maximum depth reached)."
	}
	connText := ""
	for _, c := range a.agentConnectors(ag) {
		if connText == "" {
			connText = "\nConnected services you can use (MCP tools):"
		}
		connText += fmt.Sprintf("\n- %s (tools prefixed mcp__%s__)", c.Name, c.key())
		if c.Approval {
			connText += ", each call needs the user's approval"
		}
	}
	projText := "\nProjects: you are not in a project. If the user asks for a new app, website or longer-running project, call create_project first so it gets its own folder and task board."
	if p := a.projectByID(ag.ProjectID); p != nil {
		projText = "\n" + p.taskBoard() + "\nKeep this board current with add_task and update_task: move tasks you work on to doing, finished ones to review."
	}
	return connText + projText + a.memoryText(ag) + "\n\n" + fmt.Sprintf(`You are %q, an autonomous agent inside Agent HQ: a control room where a human (the user) supervises a team of AI agents.
Your role: %s. %s
Working directory: %s

Agent HQ tools (mcp__hq__*):
- report(level, message): "update" = short progress update for the user at meaningful milestones (not every step). "critical" = something is blocked, broken, risky or needs the user's attention ASAP.
- ask_decision(question, options): ask the user to choose when a choice is important, irreversible, costs money or depends on their preference. Give 2-4 short options. The call waits for the answer (the user may also type a custom answer).
- spawn_agent(name, role, mission): create a sub-agent for an independent, parallelizable piece of work. Give it a complete, self-contained mission. %s Sub-agents are a limited resource: only spawn when it clearly helps.
- message_agent(to, message): message another agent (parent, sub-agent or teammate).
- list_agents(): see all agents and their status.
- complete(summary): call when your mission is fully done, with a short summary of the result.

Browser (mcp__hq__browser_*): browser_open, browser_read, browser_click, browser_type, browser_scroll, browser_back, browser_screenshot.
Whenever a task needs a real web page (shops, logins, forms, pages WebFetch cannot read), use these: the user watches this browser live inside Agent HQ and can take over. Never enter passwords or payment details yourself; ask the user to do it in the Agent HQ browser.

Scheduling: schedule_mission(name, mission, kind, time, days, every_minutes, date) runs a mission later or repeatedly. Use it when the user asks for something recurring or at a later time.
GitHub (mcp__hq__github_*): github_create_repo(name, description, private, push) creates a repo on the user's account and pushes your folder; github_push(message) pushes new work. Repos are private unless the user asked for public. Prefer these over the gh CLI.
Projects (mcp__hq__*): create_project, project_tasks, add_task, update_task. deploy(target) puts the project online (Netlify or GitHub Pages) and returns the URL.

Images on Telegram: report(level, message, screenshot=true) attaches the current Agent HQ browser page, use it when a page is worth seeing.

Autonomy: you have full permissions and nobody approves your individual steps. Be smart with that:
- Just do normal work (reading, searching, writing files in your working directory, running builds and tests) without asking.
- Use ask_decision BEFORE anything that is destructive or hard to undo (deleting or overwriting files outside your working directory, git push --force, dropping data), costs money, sends messages or posts as the user, changes system settings, installs system-wide software, or touches accounts. When in doubt, ask.
- Never work around a "no" from the user.

When you are waiting on sub-agents, end your turn; their results arrive as new messages.
Messages that start with [Message from agent ...] or [Sub-agent ...] come from other agents. [Message from the user] comes from the user.
Write every report, question and summary meant for the user in %s. Keep them short.`, ag.Name, ag.Role, parent, ag.Cwd, canSpawn, lang)
}

func (a *App) startTurn(ag *Agent) error {
	claude := detectClaude(a.cfg.ClaudePath)
	if claude == "" {
		return errors.New(a.T("claude_missing"))
	}
	en := func(k string, args ...any) string { return fmt.Sprintf(dict["en"][k], args...) }
	var prompt string
	if ag.SessionID == "" {
		prompt = "Your mission:\n" + ag.Mission
		if len(ag.Inbox) > 0 {
			prompt += "\n\n" + strings.Join(ag.Inbox, "\n\n")
		}
	} else {
		parts := []string{}
		if ag.NeedsContinue {
			parts = append(parts, en("continue"))
		}
		parts = append(parts, ag.Inbox...)
		prompt = strings.Join(parts, "\n\n")
		if strings.TrimSpace(prompt) == "" {
			prompt = en("continue")
		}
	}
	ag.Inbox = nil
	ag.NeedsContinue = false
	ag.Completed = false

	dir := filepath.Join(configDir(), "agents", ag.ID)
	_ = os.MkdirAll(dir, 0o755)
	servers := map[string]any{"hq": map[string]any{
		"type": "http", "url": fmt.Sprintf("http://127.0.0.1:%d/mcp/%s?k=%s", a.cfg.Port, ag.ID, a.secret)}}
	allowed := "mcp__hq,Read,Glob,Grep,LS,TodoWrite,WebSearch,WebFetch,Task"
	for _, c := range a.agentConnectors(ag) {
		servers[c.key()] = c.mcpEntry()
		if !c.Approval {
			allowed += ",mcp__" + c.key()
		}
	}
	mcp := map[string]any{"mcpServers": servers}
	mb, _ := json.Marshal(mcp)
	mcpPath := filepath.Join(dir, "mcp.json")
	spPath := filepath.Join(dir, "system-prompt.txt")
	if err := os.WriteFile(mcpPath, mb, 0o600); err != nil {
		return err
	}
	_ = os.WriteFile(spPath, []byte(a.systemPrompt(ag)), 0o600)

	args := []string{"-p", "--output-format", "stream-json", "--verbose",
		"--mcp-config", mcpPath, "--append-system-prompt-file", spPath,
		"--allowedTools", allowed}
	switch a.cfg.PermissionMode {
	case "full":
		args = append(args, "--permission-mode", "bypassPermissions")
	case "ask":
		args = append(args, "--permission-mode", "default", "--permission-prompt-tool", "mcp__hq__approve")
	default:
		args = append(args, "--permission-mode", "acceptEdits", "--permission-prompt-tool", "mcp__hq__approve")
	}
	if ag.Model != "" {
		args = append(args, "--model", ag.Model)
	}
	if ag.SessionID != "" {
		args = append(args, "--resume", ag.SessionID)
	}
	cmd := hiddenCmd(claude, args...)
	cmd.Dir = ag.Cwd
	cmd.Env = append(os.Environ(), "MCP_TOOL_TIMEOUT=900000", "MCP_TIMEOUT=30000", "CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=0")
	cmd.Stdin = strings.NewReader(prompt)
	setProcGroup(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr := &tailBuffer{max: 4096}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	ag.cmd = cmd
	ag.Status = "running"
	ag.TurnStart = time.Now()
	ag.gotResult, ag.resultErr, ag.lastResult = false, false, ""
	ag.Activity = "…"
	if ag.Turns > 0 {
		a.addLog(ag, "in", prompt)
	}
	go a.readStream(ag, cmd, stdout, stderr)
	return nil
}

type tailBuffer struct {
	b   []byte
	max int
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.b = append(t.b, p...)
	if len(t.b) > t.max {
		t.b = t.b[len(t.b)-t.max:]
	}
	return len(p), nil
}

func toolSummary(name string, in map[string]any) string {
	if strings.HasPrefix(name, "mcp__hq__") {
		n := strings.TrimPrefix(name, "mcp__hq__")
		switch n {
		case "spawn_agent":
			return "creates agent " + str(in, "name")
		case "message_agent":
			return "message to " + str(in, "to")
		case "report":
			return "reports: " + truncate(str(in, "message"), 80)
		case "ask_decision":
			return "asks: " + truncate(str(in, "question"), 80)
		case "complete":
			return "marks mission complete"
		}
		return n
	}
	arg := str(in, "command", "file_path", "path", "pattern", "query", "url", "description", "prompt")
	if arg != "" {
		if strings.Contains(arg, "/") || strings.Contains(arg, "\\") {
			if name != "Bash" && name != "WebFetch" {
				arg = filepath.Base(strings.ReplaceAll(arg, "\\", "/"))
			}
		}
		return name + ": " + truncate(arg, 90)
	}
	return name
}

func (a *App) readStream(ag *Agent, cmd *exec.Cmd, stdout interface{ Read([]byte) (int, error) }, stderr *tailBuffer) {
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 1024*1024), 32*1024*1024)
	for sc.Scan() {
		var m map[string]any
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		a.mu.Lock()
		a.handleStream(ag, m)
		a.mu.Unlock()
	}
	err := cmd.Wait()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.finishTurn(ag, cmd, err, string(stderr.b))
}

func (a *App) handleStream(ag *Agent, m map[string]any) {
	switch str(m, "type") {
	case "system":
		if sid := str(m, "session_id"); sid != "" && str(m, "subtype") == "init" {
			if ag.SessionID == "" {
				a.addLog(ag, "info", "session "+sid[:min(8, len(sid))])
			}
			ag.SessionID = sid
		}
	case "assistant":
		msg, _ := m["message"].(map[string]any)
		parts, _ := msg["content"].([]any)
		for _, p := range parts {
			pm, _ := p.(map[string]any)
			switch str(pm, "type") {
			case "text":
				if t := strings.TrimSpace(str(pm, "text")); t != "" {
					a.addLog(ag, "text", t)
					ag.Activity = truncate(t, 140)
				}
			case "tool_use":
				in, _ := pm["input"].(map[string]any)
				s := toolSummary(str(pm, "name"), in)
				ag.ToolCalls++
				a.addLog(ag, "tool", s)
				ag.Activity = s
				if k, t := toolKind(str(pm, "name"), in); k != "" {
					ag.ToolKind, ag.ToolTarget = k, t
				}
				switch str(pm, "name") {
				case "Edit", "Write", "MultiEdit", "NotebookEdit":
					ag.noteFile(str(in, "file_path", "notebook_path"))
				}
			}
		}
	case "user":
		msg, _ := m["message"].(map[string]any)
		parts, _ := msg["content"].([]any)
		for _, p := range parts {
			pm, _ := p.(map[string]any)
			if str(pm, "type") == "tool_result" && str(pm, "is_error") == "true" {
				txt := str(pm, "content")
				if txt == "" {
					if arr, ok := pm["content"].([]any); ok && len(arr) > 0 {
						if c0, ok := arr[0].(map[string]any); ok {
							txt = str(c0, "text")
						}
					}
				}
				a.addLog(ag, "error", truncate(txt, 300))
			}
		}
	case "result":
		ag.gotResult = true
		ag.resultErr = str(m, "is_error") == "true"
		ag.lastResult = str(m, "result")
		var turnCost float64
		var turnTok int64
		if c, ok := m["total_cost_usd"].(float64); ok {
			ag.Cost += c
			turnCost = c
		}
		if u, ok := m["usage"].(map[string]any); ok {
			for _, k := range []string{"input_tokens", "cache_creation_input_tokens"} {
				if v, ok := u[k].(float64); ok {
					ag.InTok += int64(v)
					turnTok += int64(v)
				}
			}
			if v, ok := u["output_tokens"].(float64); ok {
				ag.OutTok += int64(v)
				turnTok += int64(v)
			}
		}
		a.spend(ag, turnCost, turnTok)
		if ag.resultErr {
			a.addLog(ag, "error", truncate(ag.lastResult+" "+str(m, "subtype"), 400))
		} else if ag.lastResult != "" {
			a.addLog(ag, "result", ag.lastResult)
		}
	}
}

func (a *App) finishTurn(ag *Agent, cmd *exec.Cmd, waitErr error, stderr string) {
	ag.ToolKind, ag.ToolTarget = "", ""
	if ag.cmd != cmd {
		return
	}
	ag.cmd = nil
	ag.Turns++
	ag.AutoTurns++
	a.dirty = true
	defer a.schedule()
	a.expireDecisions(ag.ID, true)
	if ag.interrupted {
		ag.interrupted = false
		return
	}
	if (waitErr != nil && !ag.gotResult) || ag.resultErr {
		msg := strings.TrimSpace(stderr)
		if ag.resultErr && ag.lastResult != "" {
			msg = ag.lastResult
		}
		if msg == "" && waitErr != nil {
			msg = waitErr.Error()
		}
		// Claude Code can start before our MCP server finished connecting: retry once.
		if strings.Contains(msg, "mcp__hq__") && strings.Contains(msg, "not found") && !ag.mcpRetried {
			ag.mcpRetried = true
			ag.NeedsContinue = ag.SessionID != ""
			ag.Status = "queued"
			a.addLog(ag, "info", "HQ tools not ready yet, retrying")
			return
		}
		if isLimitMsg(msg) || isLimitMsg(stderr) {
			a.hitLimit(ag, msg+" "+stderr)
			return
		}
		ag.Status = "error"
		ag.Activity = truncate(msg, 120)
		a.addLog(ag, "error", truncate(msg, 600))
		a.emit("critical", ag.ID, a.T("crashed", ag.Name, truncate(msg, 300)))
		if ag.ParentID == "" {
			a.termAdd("ag", ag.Name+": ❌ "+truncate(msg, 300))
		}
		return
	}
	if !ag.Completed && len(ag.lastResult) < 200 && strings.Contains(strings.ToLower(ag.lastResult), "limit") && isLimitMsg(ag.lastResult) {
		a.hitLimit(ag, ag.lastResult)
		return
	}
	parent := a.agents[ag.ParentID]
	if ag.Completed {
		ag.Status = "done"
		ag.Activity = truncate(ag.Summary, 120)
		if parent != nil {
			a.deliver(parent, fmt.Sprintf(dict["en"]["agent_finished_parent"], ag.Name, ag.Summary), false)
			a.emit("info", ag.ID, a.T("done", ag.Name, truncate(ag.Summary, 300)))
		} else {
			if !a.startReview(ag) {
				a.taskFinished(ag, true)
			}
			a.emit("done", ag.ID, a.T("done", ag.Name, truncate(ag.Summary, 900)))
			a.termAdd("ag", ag.Name+": "+truncate(ag.Summary, 1200))
		}
	} else {
		if ag.lastResult != "" {
			if parent != nil {
				a.deliver(parent, fmt.Sprintf(dict["en"]["agent_reply_parent"], ag.Name, ag.lastResult), false)
				a.emit("info", ag.ID, a.T("replied", ag.Name, truncate(ag.lastResult, 300)))
			} else {
				a.emit("update", ag.ID, a.T("replied", ag.Name, truncate(ag.lastResult, 3000)))
				a.termAdd("ag", ag.Name+": "+truncate(ag.lastResult, 1200))
			}
		}
		ag.Status = "idle"
		if len(ag.Inbox) > 0 {
			ag.Status = "queued"
		}
	}
	if ag.Status == "queued" && ag.AutoTurns >= a.cfg.MaxTurns {
		ag.Status = "paused"
		a.emit("critical", ag.ID, a.T("turn_limit", ag.Name, a.cfg.MaxTurns))
	}
}

// deliver puts a message in an agent's inbox and wakes it up if it is idle.
func (a *App) deliver(ag *Agent, msg string, fromUser bool) {
	ag.Inbox = append(ag.Inbox, msg)
	if fromUser {
		ag.AutoTurns = 0
	}
	if ag.Status == "idle" || ag.Status == "done" {
		ag.Status = "queued"
	}
	a.dirty = true
	a.schedule()
}

// ---------- controls ----------

func (a *App) killAgent(ag *Agent) {
	if ag.cmd != nil && ag.cmd.Process != nil {
		ag.interrupted = true
		killTree(ag.cmd)
	}
}

func (a *App) pauseAgent(ag *Agent) {
	if ag.Status == "paused" || ag.Status == "stopped" {
		return
	}
	if isActive(ag.Status) {
		ag.NeedsContinue = true
		a.killAgent(ag)
	}
	ag.Status = "paused"
	a.expireDecisions(ag.ID, false)
	a.addLog(ag, "info", "paused")
	a.emit("info", ag.ID, a.T("paused", ag.Name))
}

func (a *App) resumeAgent(ag *Agent) {
	if ag.Status != "paused" && ag.Status != "error" {
		return
	}
	ag.AutoTurns = 0
	ag.LimitPaused, ag.LimitUntil = false, time.Time{}
	if ag.Status == "error" {
		ag.NeedsContinue = true
	}
	if ag.SessionID == "" || ag.NeedsContinue || len(ag.Inbox) > 0 {
		ag.Status = "queued"
	} else {
		ag.Status = "idle"
	}
	a.addLog(ag, "info", "resumed")
	a.emit("info", ag.ID, a.T("resumed", ag.Name))
	a.schedule()
}

func (a *App) stopAgent(ag *Agent) {
	for _, c := range a.children(ag.ID) {
		a.stopAgent(c)
	}
	if ag.Status == "stopped" {
		return
	}
	a.killAgent(ag)
	ag.Status = "stopped"
	ag.Inbox = nil
	ag.Activity = a.T("stop_reason")
	a.expireDecisions(ag.ID, false)
	a.addLog(ag, "info", "stopped")
	a.emit("info", ag.ID, a.T("stopped", ag.Name))
}

func (a *App) deleteAgent(ag *Agent) {
	for _, c := range a.children(ag.ID) {
		a.deleteAgent(c)
	}
	a.stopAgent(ag)
	delete(a.agents, ag.ID)
	for i, id := range a.order {
		if id == ag.ID {
			a.order = append(a.order[:i], a.order[i+1:]...)
			break
		}
	}
	a.dirty = true
}

// ---------- decisions ----------

func (a *App) newDecision(ag *Agent, kind, question, detail string, options []string) *Decision {
	d := &Decision{ID: newID(), AgentID: ag.ID, AgentName: ag.Name, Kind: kind, Question: question, Detail: detail,
		Options: options, Created: time.Now(), Status: "pending", ch: make(chan string, 1)}
	a.decisions = append(a.decisions, d)
	if ag.Status == "running" {
		ag.Status = "waiting"
	}
	a.addLog(ag, "tool", "asks: "+question)
	evText := ag.Name + ": " + question
	if kind == "permission" {
		evText = question + " — " + truncate(detail, 120)
	}
	a.emit("decision", ag.ID, evText)
	c := a.cfg
	if c.BotToken != "" && c.ChatID != "" && c.TgDecisions && !a.demo {
		go a.tgSendDecision(d)
	}
	return d
}

func (a *App) answerDecision(id, answer, via string) error {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return errors.New("empty answer")
	}
	var d *Decision
	for _, x := range a.decisions {
		if x.ID == id {
			d = x
		}
	}
	if d == nil {
		return errors.New("unknown decision")
	}
	if d.Status != "pending" {
		return errors.New("already " + d.Status)
	}
	d.Status, d.Answer, d.AnsweredAt, d.Via = "answered", answer, time.Now(), via
	a.dirty = true
	ag := a.agents[d.AgentID]
	if !d.late {
		d.ch <- answer
	} else if ag != nil && d.Kind == "decision" {
		a.deliver(ag, fmt.Sprintf(dict["en"]["decision_answer"], d.Question, answer), true)
	}
	if ag != nil {
		a.addLog(ag, "in", "answer: "+answer)
		if ag.Status == "waiting" && !a.hasPending(ag.ID) {
			ag.Status = "running"
		}
		ag.AutoTurns = 0
	}
	a.emit("info", d.AgentID, a.T("answered", answer)+" ("+d.AgentName+")")
	if d.tgMsgID != 0 {
		go a.tgMarkAnswered(d)
	}
	return nil
}

func (a *App) hasPending(agentID string) bool {
	for _, d := range a.decisions {
		if d.AgentID == agentID && d.Status == "pending" {
			return true
		}
	}
	return false
}

// expireDecisions cancels pending decisions of an agent. onlyPermissions keeps
// late "decision" questions open so a later answer is delivered as a message.
func (a *App) expireDecisions(agentID string, onlyPermissions bool) {
	for _, d := range a.decisions {
		if d.AgentID != agentID || d.Status != "pending" {
			continue
		}
		if onlyPermissions && d.Kind == "decision" {
			d.late = true
			continue
		}
		d.Status = "expired"
		if !d.late {
			select {
			case d.ch <- "":
			default:
			}
		}
		if d.tgMsgID != 0 {
			go a.tgMarkAnswered(d)
		}
	}
	a.dirty = true
}

// ---------- persistence ----------

type savedState struct {
	Memory    []*Note                      `json:"memory"`
	Skills    []*Skill                     `json:"skills"`
	Ledger    map[string]map[string]*Spend `json:"ledger"`
	Schedules []*Schedule                  `json:"schedules"`
	Projects  []*Project                   `json:"projects"`
	Agents    []*Agent                     `json:"agents"`
	Decisions []*Decision                  `json:"decisions"`
	Events    []Event                      `json:"events"`
	EvSeq     int64                        `json:"evSeq"`
}

func (a *App) saveState() {
	if a.demo {
		return
	}
	st := savedState{Events: a.events, EvSeq: a.evSeq, Projects: a.projects, Schedules: a.schedules, Memory: a.memory, Skills: a.skills, Ledger: a.ledger}
	for _, id := range a.order {
		st.Agents = append(st.Agents, a.agents[id])
	}
	ds := a.decisions
	if len(ds) > 100 {
		ds = ds[len(ds)-100:]
	}
	st.Decisions = ds
	b, err := json.Marshal(st)
	if err != nil {
		return
	}
	p := filepath.Join(configDir(), "state.json")
	_ = os.WriteFile(p+".tmp", b, 0o600)
	_ = os.Rename(p+".tmp", p)
	a.dirty = false
}

func (a *App) loadState() {
	var st savedState
	if readJSON(filepath.Join(configDir(), "state.json"), &st) != nil {
		return
	}
	a.projects = st.Projects
	a.schedules = st.Schedules
	a.memory, a.skills, a.ledger = st.Memory, st.Skills, st.Ledger
	for _, s := range a.schedules {
		if s.Enabled {
			s.NextRun = s.next(time.Now())
		}
	}
	for _, ag := range st.Agents {
		if ag.Status == "running" || ag.Status == "waiting" || ag.Status == "queued" {
			ag.Status = "paused"
			ag.NeedsContinue = ag.SessionID != ""
		}
		ag.Kind, ag.Color = roleKind(ag)
		ag.ToolKind, ag.ToolTarget = "", ""
		if ag.LimitPaused && ag.LimitUntil.After(a.limitUntil) {
			a.limitUntil = ag.LimitUntil
		}
		if ag.LimitPaused && a.limitUntil.IsZero() {
			a.limitUntil = time.Now()
		}
		if ag.ResumeAfterRestart { // paused by an update: continue automatically
			ag.ResumeAfterRestart = false
			ag.Status = "queued"
		}
		a.agents[ag.ID] = ag
		a.order = append(a.order, ag.ID)
	}
	for _, d := range st.Decisions {
		if d.Status == "pending" {
			d.Status = "expired"
		}
		a.decisions = append(a.decisions, d)
	}
	a.events, a.evSeq = st.Events, st.EvSeq
}

// ---------- views ----------

type AgentView struct {
	*Agent
	Log      []LogEntry `json:"log,omitempty"`
	Children int        `json:"children"`
	Elapsed  int64      `json:"elapsed"`
	Recent   []LogEntry `json:"recent"`
}

func (a *App) agentViews() []AgentView {
	// order: depth-first tree by creation
	out := []AgentView{}
	var walk func(parent string)
	walk = func(parent string) {
		for _, id := range a.order {
			ag := a.agents[id]
			if ag.ParentID != parent {
				continue
			}
			v := AgentView{Agent: ag, Children: len(a.children(id))}
			if isActive(ag.Status) {
				v.Elapsed = int64(time.Since(ag.TurnStart).Seconds())
			}
			n := len(ag.Log)
			v.Recent = ag.Log[max(0, n-6):]
			out = append(out, v)
			walk(id)
		}
	}
	walk("")
	// orphans (parent deleted)
	seen := map[string]bool{}
	for _, v := range out {
		seen[v.ID] = true
	}
	for _, id := range a.order {
		if !seen[id] {
			out = append(out, AgentView{Agent: a.agents[id]})
		}
	}
	return out
}

func (a *App) pendingDecisions() []*Decision {
	out := []*Decision{}
	for _, d := range a.decisions {
		if d.Status == "pending" {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.Before(out[j].Created) })
	return out
}
