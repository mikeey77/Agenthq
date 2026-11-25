package main

import (
	"fmt"
	"strings"
	"time"
)

const fence = "```"

// seedDemo fills the dashboard with example data (run with --demo). No processes are started.
func (a *App) seedDemo() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cfg.SetupDone = true
	if a.cfg.BotToken == "" {
		a.tg = TelegramState{BotName: "michael_hq_bot"}
		a.cfg.BotToken, a.cfg.ChatID = "demo", "demo"
	}
	now := time.Now()
	tools := func(n int, p string) []string {
		out := []string{}
		for i := 0; i < n; i++ {
			out = append(out, p)
		}
		return out
	}
	a.cfg.Connectors = []Connector{
		{ID: "c1", Name: "GitHub", Preset: "github", Kind: "http", URL: "https://api.githubcopilot.com/mcp/", Headers: map[string]string{"Authorization": "Bearer github_pat_11ABCDEFG0123456789"}, Enabled: true, Tools: tools(41, "x"), Tested: now.Add(-3 * time.Hour)},
		{ID: "c2", Name: "Browser (Playwright)", Preset: "playwright", Kind: "stdio", Command: "npx", Args: []string{"-y", "@playwright/mcp@latest"}, Enabled: true, Tools: tools(21, "x"), Tested: now.Add(-26 * time.Hour)},
		{ID: "c3", Name: "Home Assistant", Preset: "homeassistant", Kind: "http", URL: "http://homeassistant.local:8123/api/mcp", Enabled: true, Approval: true, Error: "HTTP 401: token missing or not accepted", Tested: now.Add(-10 * time.Minute)},
		{ID: "c4", Name: "Folders", Preset: "filesystem", Kind: "stdio", Command: "npx", Args: []string{"-y", "@modelcontextprotocol/server-filesystem", `C:\Users\Michael\Documents`}, Enabled: false},
	}
	ago := func(m float64) time.Time { return now.Add(-time.Duration(m * float64(time.Minute))) }
	ws := `C:\Users\Michael\AgentHQ\1124-glovebox-receipt-scanner`
	mk := func(id, name, role, parent, status, activity, model string, depth int, cost float64, turns, tools int, created float64, log []LogEntry) *Agent {
		ag := &Agent{ID: id, Name: name, Role: role, ParentID: parent, Status: status, Activity: activity, Model: model,
			Depth: depth, Cost: cost, Turns: turns, ToolCalls: tools, Created: ago(created), Updated: ago(0.2), Cwd: ws,
			TurnStart: ago(created / 3), SessionID: "s-" + id, Log: log,
			InTok: int64(cost * 180000), OutTok: int64(cost * 22000)}
		ag.Kind, ag.Color = roleKind(ag)
		ag.ToolKind, ag.ToolTarget = toolKindFromActivity(activity)
		a.agents[id] = ag
		a.order = append(a.order, id)
		return ag
	}
	L := func(m float64, k, x string) LogEntry { return LogEntry{T: ago(m), Kind: k, Text: x} }
	atlas := mk("a1", "atlas", "Mission lead", "", "running", "message to forge", "opus", 0, 1.84, 6, 31, 42, []LogEntry{
		L(42, "in", "Build an MVP of the Glovebox receipt scanner: photo → amount, date, shop → CSV export. Android first."),
		L(41, "text", "Plan: research OCR (scout), build parser + app (forge), tests (probe), docs (quill). Spawning the team."),
		L(41, "tool", "creates agent scout"), L(40, "tool", "creates agent forge"), L(39, "tool", "creates agent probe"), L(39, "tool", "creates agent quill"),
		L(26, "in", "[Sub-agent scout finished] ML Kit v2 wins: offline, free, 94% amount accuracy on 50 test receipts."),
		L(25, "tool", "reports: OCR choice made: Google ML Kit (offline, free)."),
		L(3, "text", "forge is blocked on the Android SDK. Asking it to switch to the web prototype first."),
		L(2, "tool", "message to forge"),
	})
	atlas.Mission = "Build an MVP of the Glovebox receipt scanner: photo → amount, date, shop → CSV export. Android first."
	s := mk("a2", "scout", "OCR researcher", "a1", "done", "ML Kit v2 wins: offline, free, 94% accuracy", "sonnet", 1, 0.42, 3, 18, 41, []LogEntry{
		L(40, "tool", "WebSearch: best offline OCR android receipts 2026"), L(33, "tool", "Bash: python bench_ocr.py --set receipts50"),
		L(27, "tool", "marks mission complete"), L(27, "result", "ML Kit v2 wins: offline, free, 94% amount accuracy on 50 test receipts.")})
	s.Summary = "ML Kit v2 wins: offline, free, 94% amount accuracy on 50 test receipts."
	mk("a3", "forge", "Builder", "a1", "running", "Edit: parser.ts", "sonnet", 1, 0.97, 4, 47, 40, []LogEntry{
		L(24, "text", "Scaffolding the Expo app and the receipt parser."), L(18, "tool", "Bash: npx create-expo-app glovebox"),
		L(9, "error", "Android SDK not found (ANDROID_HOME not set)"), L(8, "tool", "reports: Build failed 3x: Android SDK missing on this PC."),
		L(2, "in", "[Message from agent atlas] Switch to the web prototype first, we do Android later."),
		L(1, "text", "Switching to web: parsing logic is shared, only the camera layer differs."), L(0.3, "tool", "Edit: parser.ts")})
	mk("a4", "probe", "Tester", "a1", "waiting", "asks: Which test framework should I use?", "haiku", 1, 0.11, 2, 9, 39, []LogEntry{
		L(30, "text", "Collecting 20 sample receipts for the test set."), L(12, "tool", "Write: fixtures.json"), L(1, "tool", "asks: Which test framework should I use?")})
	mk("a5", "quill", "Docs writer", "a1", "paused", "paused by you", "haiku", 1, 0.05, 1, 4, 39, []LogEntry{L(35, "text", "Drafting README and user guide."), L(20, "info", "paused")})
	mk("a6", "lens", "Web scout", "a3", "running", "browser_open: pixelmarkt.test", "sonnet", 2, 0.18, 1, 7, 6, []LogEntry{
		L(6, "in", "Build the browser camera capture with crop + auto-rotate."), L(2, "tool", "WebFetch: developer.mozilla.org/…/getUserMedia"), L(1, "tool", "browser_open: pixelmarkt.test")})
	b := mk("b1", "nova", "Mission lead", "", "idle", "Telegram plugin done, all 42 tests pass. Ready for a release build?", "sonnet", 0, 0.64, 5, 22, 180, []LogEntry{
		L(180, "in", "Finish the Telegram plugin for Assistant22 and make all tests pass."), L(61, "text", "Ran the full suite:\n\n"+fence+"\nnpm test\n42 passing (3.1s)\n"+fence+"\n\n**Changes**\n- plugin registry loads Telegram first\n- retry on 429 with backoff\n- new test for `/status`"), L(60, "result", "Telegram plugin done, all 42 tests pass. Ready for a release build?")})
	b.Mission = "Finish the Telegram plugin for Assistant22 and make all tests pass."
	b.Cwd = `C:\Users\Michael\Projects\assistant22`

	missions := map[string]string{"a2": "Compare offline OCR libraries for receipts (amount, date, shop) and pick the best one.", "a3": "Build the receipt parser and the app shell. Use the OCR choice from scout.", "a4": "Write tests for the parser with 20 real-world sample receipts.", "a5": "Write the README and a short user guide.", "a6": "Build the browser camera capture with crop + auto-rotate."}
	for id, m := range missions {
		a.agents[id].Mission = m
	}
	// projects
	T := func(title, st, agent, by string, mins float64) *PTask {
		return &PTask{ID: newID()[:6], Title: title, Status: st, AgentID: agent, By: by, Updated: ago(mins)}
	}
	gb := &Project{ID: "p1", Name: "Glovebox", Desc: "Receipt and mileage app for small businesses.", Folder: `C:\Users\Michael\AgentHQ\projects\glovebox`,
		Repo: "https://github.com/mikeey77/glovebox", Color: "#ff8a4c", Created: ago(60 * 24 * 6), Tasks: []*PTask{
			T("Pick an OCR library", "done", "a2", "atlas", 26), T("Receipt parser (amount, date, shop)", "doing", "a3", "atlas", 4),
			T("Parser tests with 20 receipts", "doing", "a4", "atlas", 9), T("README and user guide", "todo", "", "atlas", 39),
			T("CSV export", "todo", "", "you", 300), T("Mileage tracking with GPS", "todo", "", "you", 2000),
			T("App icon and splash screen", "review", "", "you", 120), T("Project setup + CI", "done", "", "atlas", 3000)}}
	as := &Project{ID: "p2", Name: "Assistant22", Desc: "Telegram mail and calendar assistant.", Folder: `C:\Users\Michael\Projects\assistant22`,
		Color: "#6aa7ff", Created: ago(60 * 24 * 20), Tasks: []*PTask{
			T("Telegram plugin", "review", "b1", "you", 60), T("Release build 1.3", "todo", "", "nova", 59), T("Upwork plugin", "todo", "", "you", 4000),
			T("WhatsApp bridge", "done", "", "you", 9000), T("Calendar sync", "done", "", "you", 9500)}}
	a.projects = []*Project{gb, as}
	N := func(text, kind, by string, mins float64) *Note {
		return &Note{ID: newID()[:6], Text: text, Kind: kind, By: by, Created: ago(mins)}
	}
	gb.Memory = []*Note{N("We use Vitest for tests, not Jest.", "decision", "atlas", 3000), N("UI texts in Dutch, code and comments in English.", "preference", "you", 5000),
		N("Tesseract misreads crumpled receipts; always run the deskew step first.", "lesson", "lens", 40), N("Amounts are stored in cents (integers), never floats.", "decision", "forge", 26)}
	gb.SiteURL, gb.NetlifySite = "https://glovebox-demo.netlify.app", "demo"
	gb.Deploys = []Deploy{{Target: "netlify", URL: gb.SiteURL, At: ago(60 * 20), OK: true}}
	as.Memory = []*Note{N("Telegram bot token lives in .env, never commit it.", "fact", "nova", 900)}
	a.memory = []*Note{N("Michael prefers short answers in Dutch.", "preference", "you", 9000), N("Main PC is a Windows laptop without a strong GPU.", "fact", "you", 9000)}
	a.skills = []*Skill{
		{ID: "k1", Name: "Deploy a Vite site to Netlify", Desc: "When a Vite or React app has to go online.", Steps: "1. Run `npm run build`.\n2. Check that dist/index.html exists.\n3. Call deploy(target: netlify).\n4. Open the URL and check the console for errors.", By: "nova", Uses: 6, Created: ago(60 * 24 * 9)},
		{ID: "k2", Name: "Set up Vitest in a TypeScript project", Desc: "Adding tests to a project that has none.", Steps: "1. `npm i -D vitest`\n2. Add \"test\": \"vitest run\" to package.json.\n3. Put tests next to the code as *.test.ts.", By: "probe", Uses: 3, Created: ago(60 * 24 * 4)},
		{ID: "k3", Name: "Read receipts with OCR", Desc: "Turning a photo of a receipt into amount, date and shop.", Steps: "1. Deskew and threshold the image.\n2. Tesseract with nld+eng.\n3. Find the total with the regex in src/ocr.ts.", By: "lens", Uses: 2, Created: ago(60 * 30)},
		{ID: "k4", Name: "Clean up the mailbox", Desc: "Summarize unread mail and archive newsletters.", Steps: "1. List unread mail of the last 24 h.\n2. Group by: needs answer, info, newsletter.\n3. Ask before archiving.", By: "you", Uses: 12, Created: ago(60 * 24 * 20)},
	}
	a.ledger = map[string]map[string]*Spend{}
	for i := 0; i < 30; i++ {
		d := time.Now().AddDate(0, 0, -i).Format("2006-01-02")
		f := float64((i*37)%11+3) / 10
		a.ledger[d] = map[string]*Spend{
			"p1": {Cost: 1.9 * f, Tokens: int64(1900000 * f), Turns: int(12 * f)},
			"p2": {Cost: 0.6 * f * float64(i%3), Tokens: int64(600000 * f * float64(i%3)), Turns: int(4 * f)},
			"":   {Cost: 0.4 * f, Tokens: int64(380000 * f), Turns: int(5 * f)},
		}
	}
	a.schedules = []*Schedule{
		{ID: "s1", Name: "Morning mail summary", Mission: "Summarize my unread mail from the last 24 hours and tell me what needs an answer.", Kind: "daily", Time: "07:30", Enabled: true, Runs: 12, LastRun: ago(60 * 15), By: "you"},
		{ID: "s2", Name: "Glovebox nightly tests", Mission: "Run all Glovebox tests, fix small failures and report what broke.", Project: "p1", Kind: "weekly", Days: []int{1, 2, 3, 4, 5}, Time: "02:00", Enabled: true, Runs: 4, LastRun: ago(60 * 21), By: "atlas"},
		{ID: "s3", Name: "Price check Xbox Series X", Mission: "Check the price of the Xbox Series X at three Dutch shops and tell me if it drops below 400 euro.", Kind: "interval", Every: 360, Enabled: false, Runs: 7, LastRun: ago(60 * 30), By: "you"},
	}
	for _, s := range a.schedules {
		s.NextRun = s.next(time.Now())
	}
	for _, id := range []string{"a1", "a2", "a3", "a4", "a5", "a6"} {
		a.agents[id].ProjectID = "p1"
		a.agents[id].Cwd = gb.Folder
	}
	a.agents["b1"].ProjectID = "p2"
	a.agents["a3"].Files = []string{gb.Folder + `\src\parser.ts`, gb.Folder + `\src\ocr.ts`, gb.Folder + `\src\app.tsx`, gb.Folder + `\package.json`}
	a.agents["a4"].Files = []string{gb.Folder + `\test\parser.test.ts`, gb.Folder + `\test\receipts\README.md`}
	a.agents["a2"].Files = []string{gb.Folder + `\docs\ocr-comparison.md`}
	a.agents["b1"].Files = []string{as.Folder + `\plugins\telegram.ts`, as.Folder + `\test\telegram.test.ts`}
	go func() { // a live browser tab for the demo
		time.Sleep(1500 * time.Millisecond)
		_, _ = a.browser.Open("a6", "lens", fmt.Sprintf("http://127.0.0.1:%d/assets/demo-shop.html", a.cfg.Port))
	}()
	a.decisions = []*Decision{
		{ID: "d1", AgentID: "a4", AgentName: "probe", Kind: "decision", Question: "Which test framework should I use for the parser?", Detail: "Vitest is faster and works with Vite; Jest has more examples online.", Options: []string{"Vitest", "Jest", "Both (compare)"}, Created: ago(1), Status: "pending", ch: make(chan string, 1)},
		{ID: "d2", AgentID: "a3", AgentName: "forge", Kind: "permission", Question: "forge wants to use Bash", Detail: "npm install tesseract.js@6 sharp --save", Options: []string{"Allow", "Deny"}, Created: ago(0.4), Status: "pending", ch: make(chan string, 1)},
		{ID: "d0", AgentID: "a1", AgentName: "atlas", Kind: "decision", Question: "Start with Android or a web prototype?", Options: []string{"Android", "Web first"}, Created: ago(4), Status: "answered", Answer: "Web first", AnsweredAt: ago(3), Via: "telegram"},
	}
	ev := func(m float64, level, agent, text string) {
		a.evSeq++
		a.events = append([]Event{{ID: a.evSeq, Time: ago(m), Level: level, Text: text, AgentID: agent}}, a.events...)
	}
	ev(42, "update", "a1", "New mission for atlas: Build an MVP of the Glovebox receipt scanner")
	ev(41, "update", "a2", "atlas created scout (OCR researcher)")
	ev(40, "update", "a3", "atlas created forge (Builder)")
	ev(39, "update", "a4", "atlas created probe (Tester)")
	ev(27, "info", "a2", "scout finished: ML Kit v2 wins: offline, free, 94% accuracy")
	ev(25, "update", "a1", "atlas: OCR choice made: Google ML Kit (offline, free).")
	ev(8, "critical", "a3", "forge: Build failed 3x — Android SDK missing on this PC.")
	ev(6, "update", "a6", "forge created lens (Camera UX)")
	ev(4, "decision", "a1", "atlas: Start with Android or a web prototype?")
	ev(3, "info", "a1", "Answered: Web first (atlas)")
	ev(1, "decision", "a4", "probe: Which test framework should I use for the parser?")
	ev(0.4, "decision", "a3", "forge wants to use Bash")
	a.term = []TermLine{
		{T: ago(43), Kind: "in", Text: "Build an MVP of the Glovebox receipt scanner: photo → amount, date, shop → CSV export. Android first."},
		{T: ago(43), Kind: "out", Text: "Mission started → atlas"},
		{T: ago(21), Kind: "in", Text: "pause quill"},
		{T: ago(21), Kind: "out", Text: "✓ quill → paused"},
		{T: ago(2), Kind: "in", Text: "@atlas keep the CSV export simple, one row per receipt"},
		{T: ago(2), Kind: "out", Text: "Message queued for atlas"},
		{T: ago(1), Kind: "in", Text: "list my github repos and send me a message on telegram"},
		{T: ago(1), Kind: "out", Text: "Mission started → nova"},
		{T: ago(0.5), Kind: "ag", Text: "nova: You have 6 repos: agent-hq, assistant22, scrap-duel, glovebox, romscan, last-light. Sent the list to Telegram."},
	}
	// fake token usage: a block that started ~3h ago
	u := a.usage
	u.mu.Lock()
	for i := 0; i < 180; i++ {
		u.entries = append(u.entries, usageEntry{t: now.Add(-time.Duration(178-i) * time.Minute), tokens: int64(9000 + (i%7)*2500)})
	}
	for i := 0; i < 60; i++ {
		u.entries = append(u.entries, usageEntry{t: now.Add(-time.Duration(20*60+i*4) * time.Minute), tokens: 85000})
	}
	for i := 0; i < 40; i++ {
		u.entries = append(u.entries, usageEntry{t: now.Add(-time.Duration(30*60+i*5) * time.Minute), tokens: 52000})
	}
	u.ready = true
	u.mu.Unlock()
	go a.demoTicker()
}

// demoTicker keeps the demo floor moving: agents message each other now and then.
func (a *App) demoTicker() {
	pairs := [][2]string{{"a3", "a1"}, {"a1", "a3"}, {"a6", "a3"}, {"a1", "a4"}, {"a2", "a1"}}
	msgs := []string{"parser handles dates now", "use the web prototype", "camera crop works", "use Vitest if probe asks", "shared the OCR benchmark"}
	for i := 0; ; i++ {
		time.Sleep(2500 * time.Millisecond)
		a.mu.Lock()
		p := pairs[i%len(pairs)]
		from, to := a.agents[p[0]], a.agents[p[1]]
		if from != nil && to != nil {
			a.emit("chatter", from.ID, from.Name+" → "+to.Name+": "+msgs[i%len(msgs)])
		}
		a.mu.Unlock()
	}
}

// toolKindFromActivity guesses the activity icon for demo agents from their activity text.
func toolKindFromActivity(act string) (string, string) {
	name, arg, _ := strings.Cut(act, ": ")
	switch {
	case strings.HasPrefix(name, "browser_"):
		return "browser", arg
	case name == "Edit" || name == "Write":
		return "edit", arg
	case name == "Read":
		return "file", arg
	case name == "Bash":
		return "terminal", strings.Fields(arg + " x")[0]
	case name == "WebFetch":
		return "web", host("https://" + arg)
	case name == "WebSearch":
		return "search", arg
	}
	return "", ""
}
