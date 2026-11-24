package main

// Telegram as the main terminal: chat with agents, start missions, send files and
// control everything from your phone. Replies to an agent's message go to that agent;
// after you start a mission, your next messages go to its lead until /end or /new.

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var tgCommands = []struct{ cmd, en, nl string }{
	{"new", "Start a new mission", "Nieuwe missie starten"},
	{"agents", "Agents with buttons to talk, pause or stop", "Agents met knoppen om te praten, pauzeren of stoppen"},
	{"talk", "Talk to an agent: /talk forge", "Praat met een agent: /talk forge"},
	{"end", "Stop talking to the current agent", "Stop het gesprek met de huidige agent"},
	{"status", "Status card", "Statuskaart"},
	{"decisions", "Open questions", "Open vragen"},
	{"projects", "Projects and their progress", "Projecten en voortgang"},
	{"task", "Add a task: /task glovebox CSV export", "Taak toevoegen: /task glovebox CSV-export"},
	{"schedules", "Scheduled missions", "Geplande missies"},
	{"log", "Last steps of an agent: /log forge", "Laatste stappen van een agent: /log forge"},
	{"files", "Files an agent changed: /files forge", "Bestanden die een agent wijzigde: /files forge"},
	{"pause", "Pause an agent or all", "Agent of alles pauzeren"},
	{"resume", "Continue a paused agent", "Gepauzeerde agent verder laten gaan"},
	{"stop", "Stop an agent or all", "Agent of alles stoppen"},
	{"usage", "Token usage and reset time", "Tokengebruik en resettijd"},
	{"help", "Everything you can do here", "Alles wat je hier kunt"},
}

func (a *App) tgSetCommands() {
	a.mu.Lock()
	lang := a.cfg.Lang
	a.mu.Unlock()
	var cmds []map[string]string
	for _, c := range tgCommands {
		d := c.en
		if lang == "nl" {
			d = c.nl
		}
		cmds = append(cmds, map[string]string{"command": c.cmd, "description": d})
	}
	_, _ = a.tgCall("setMyCommands", map[string]any{"commands": cmds})
}

// tgAgentSend sends a message about an agent and remembers it, so a reply goes back to that agent.
func (a *App) tgAgentSend(agentID, text string, silent bool, kb any) {
	chat := a.chatID()
	if chat == "" {
		return
	}
	id, err := a.tgSendTo(chat, text, silent, kb)
	if err == nil && agentID != "" {
		a.rememberTg(id, agentID)
	}
}

func (a *App) rememberTg(msgID int64, agentID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.tgAgentMsgs == nil {
		a.tgAgentMsgs = map[int64]string{}
	}
	a.tgAgentMsgs[msgID] = agentID
	if len(a.tgAgentMsgs) > 2000 { // keep it small
		for k := range a.tgAgentMsgs {
			if k < msgID-1500 {
				delete(a.tgAgentMsgs, k)
			}
		}
	}
}

// typing shows "typing…" in Telegram while the agent works on your message (max 3 minutes).
func (a *App) typing(agentID string) {
	go func() {
		chat := a.chatID()
		for i := 0; i < 45; i++ {
			a.mu.Lock()
			ag := a.agents[agentID]
			busy := ag != nil && (ag.Status == "running" || ag.Status == "queued")
			a.mu.Unlock()
			if !busy || chat == "" {
				return
			}
			_, _ = a.tgCall("sendChatAction", map[string]any{"chat_id": chat, "action": "typing"})
			time.Sleep(4 * time.Second)
		}
	}()
}

func agentButtons(a *App, ag *Agent) [][]map[string]string {
	row := []map[string]string{{"text": "💬 " + a.T("tg_btn_talk"), "callback_data": "a:" + ag.ID + ":talk"}}
	switch {
	case ag.Status == "paused" || ag.Status == "error":
		row = append(row, map[string]string{"text": "▶️ " + a.T("tg_btn_resume"), "callback_data": "a:" + ag.ID + ":resume"})
	case isLive(ag.Status):
		row = append(row, map[string]string{"text": "⏸ " + a.T("tg_btn_pause"), "callback_data": "a:" + ag.ID + ":pause"})
	}
	if ag.Status != "stopped" && ag.Status != "done" {
		row = append(row, map[string]string{"text": "⏹", "callback_data": "a:" + ag.ID + ":stop"})
	}
	row = append(row, map[string]string{"text": "📜", "callback_data": "a:" + ag.ID + ":log"})
	return [][]map[string]string{row}
}

var statusEmoji = map[string]string{"running": "🟢", "waiting": "🟡", "queued": "🔵", "paused": "⏸", "error": "🔴", "done": "✅", "idle": "💤", "stopped": "⏹"}

// agentLogText is the last steps of an agent, for /log. Caller must NOT hold a.mu.
func (a *App) agentLogText(ag *Agent) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var b strings.Builder
	fmt.Fprintf(&b, "📜 %s — %s\n", ag.Name, a.T("s_"+ag.Status))
	logs := ag.Log
	if len(logs) > 12 {
		logs = logs[len(logs)-12:]
	}
	for _, l := range logs {
		fmt.Fprintf(&b, "\n%s %s", l.T.Format("15:04"), truncate(strings.ReplaceAll(l.Text, "\n", " "), 160))
	}
	return b.String()
}

// saveTgFile downloads a photo or document the user sent and returns where it was saved.
func (a *App) saveTgFile(fileID, name, dir string) (string, error) {
	res, err := a.tgCall("getFile", map[string]any{"file_id": fileID})
	if err != nil {
		return "", err
	}
	r, _ := res["result"].(map[string]any)
	fp := str(r, "file_path")
	a.mu.Lock()
	token := a.cfg.BotToken
	a.mu.Unlock()
	resp, err := httpClient.Get(tgAPI + "/file/bot" + token + "/" + fp)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download failed: %s", resp.Status)
	}
	if name == "" {
		name = time.Now().Format("20060102-150405") + filepath.Ext(fp)
	}
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	dir = filepath.Join(dir, "from-telegram")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.Copy(f, io.LimitReader(resp.Body, 50<<20)); err != nil {
		return "", err
	}
	return path, nil
}

func (a *App) handleMessage(um map[string]any) {
	msg, _ := um["message"].(map[string]any)
	if msg == nil {
		return
	}
	chat, _ := msg["chat"].(map[string]any)
	chatID := str(chat, "id")
	text := strings.TrimSpace(str(msg, "text"))
	if text == "" {
		text = strings.TrimSpace(str(msg, "caption"))
	}
	cmd := strings.ToLower(strings.SplitN(strings.SplitN(text, " ", 2)[0], "@", 2)[0])
	a.mu.Lock()
	linked := a.cfg.ChatID
	if linked == "" && cmd == "/start" {
		a.cfg.ChatID = chatID
		_ = saveConfig(a.cfg)
		linked = chatID
		a.emit("info", "", "Telegram linked")
		for _, d := range a.decisions { // decisions that were already waiting
			if d.Status == "pending" && d.tgMsgID == 0 {
				go a.tgSendDecision(d)
			}
		}
	}
	a.mu.Unlock()
	if chatID != linked {
		_, _ = a.tgSendTo(chatID, a.T("tg_other_chat"), false, nil)
		return
	}
	reply := func(s string) { _, _ = a.tgSendTo(chatID, s, false, nil) }
	rest := ""
	if parts := strings.SplitN(text, " ", 2); len(parts) == 2 {
		rest = strings.TrimSpace(parts[1])
	}

	// 1. reply to a decision = answer
	var replyTo int64
	if rt, ok := msg["reply_to_message"].(map[string]any); ok {
		mid, _ := rt["message_id"].(float64)
		replyTo = int64(mid)
	}
	if replyTo != 0 {
		a.mu.Lock()
		did, isDec := a.tgMsgs[replyTo]
		var err error
		ans := text
		if isDec {
			for _, d := range a.decisions {
				if d.ID == did {
					if i, ok := matchOption(d.Options, text); ok {
						ans = d.Options[i]
					}
				}
			}
			err = a.answerDecision(did, ans, "telegram")
		}
		a.mu.Unlock()
		if isDec {
			if err != nil {
				reply("❌ " + err.Error())
			} else {
				_, _ = a.tgSendTo(chatID, "👌 "+a.T("answered", ans), true, nil)
			}
			return
		}
	}

	// 2. a bare 1️⃣ / ✅ / ❌ answers the newest open decision
	if !strings.HasPrefix(text, "/") && text != "" && len([]rune(text)) <= 4 {
		a.mu.Lock()
		var last *Decision
		for _, d := range a.decisions {
			if d.Status == "pending" && (last == nil || d.Created.After(last.Created)) {
				last = d
			}
		}
		answered := ""
		if last != nil {
			if i, ok := matchOption(last.Options, text); ok && a.answerDecision(last.ID, last.Options[i], "telegram") == nil {
				answered = last.Options[i]
			}
		}
		a.mu.Unlock()
		if answered != "" {
			_, _ = a.tgSendTo(chatID, "👌 "+a.T("answered", answered), true, nil)
			return
		}
	}

	// which agent is this conversation with?
	a.mu.Lock()
	target := a.agents[a.tgAgentMsgs[replyTo]]
	if target == nil {
		target = a.agents[a.tgFocus]
	}
	if target != nil && target.Status == "stopped" {
		target = nil
	}
	a.mu.Unlock()

	// 3. photos and files: save them where the agent works and tell the agent
	fileNote := ""
	if ph, ok := msg["photo"].([]any); ok && len(ph) > 0 {
		big, _ := ph[len(ph)-1].(map[string]any)
		fileNote = a.tgIncomingFile(str(big, "file_id"), "", target)
	} else if doc, ok := msg["document"].(map[string]any); ok {
		fileNote = a.tgIncomingFile(str(doc, "file_id"), str(doc, "file_name"), target)
	}
	if fileNote != "" {
		if strings.HasPrefix(fileNote, "❌") {
			reply(fileNote)
			return
		}
		text = strings.TrimSpace(text + "\n\n" + fileNote)
		cmd = ""
	}

	switch cmd {
	case "/start", "/help":
		reply(a.T("tg_help"))
	case "/status":
		a.mu.Lock()
		doc := a.statusCard()
		a.mu.Unlock()
		go a.tgCard(doc, a.statusText(), false, nil)
	case "/agents", "/list":
		a.tgAgentsList(chatID)
	case "/decisions":
		a.mu.Lock()
		n := 0
		for _, d := range a.decisions {
			if d.Status == "pending" {
				n++
				go a.tgSendDecision(d)
			}
		}
		a.mu.Unlock()
		if n == 0 {
			reply("👌 " + a.T("tg_no_decisions"))
		}
	case "/usage":
		reply(strings.Join(a.runCommand("usage"), "\n"))
	case "/projects", "/p":
		reply(strings.Join(a.runCommand("projects"), "\n\n"))
	case "/task":
		reply(a.tgAddTask(rest))
	case "/schedules", "/schedule":
		reply(a.tgSchedules(rest))
	case "/talk":
		a.mu.Lock()
		ag := a.agentByRef(rest)
		if ag != nil {
			a.tgFocus = ag.ID
		}
		a.mu.Unlock()
		if ag == nil {
			a.tgAgentsList(chatID)
			return
		}
		reply("💬 " + a.T("tg_talking", ag.Name) + "\n" + a.T("tg_talk_hint"))
	case "/end":
		a.mu.Lock()
		a.tgFocus = ""
		a.mu.Unlock()
		reply("👋 " + a.T("tg_ended"))
	case "/log", "/files":
		a.mu.Lock()
		ag := a.agentByRef(rest)
		if ag == nil {
			ag = target
		}
		a.mu.Unlock()
		if ag == nil {
			reply(a.T("unknown_agent", rest))
			return
		}
		if cmd == "/log" {
			reply(a.agentLogText(ag))
			return
		}
		a.mu.Lock()
		files := append([]string{}, ag.Files...)
		cwd := ag.Cwd
		a.mu.Unlock()
		if len(files) == 0 {
			reply("📂 " + cwd + "\n" + a.T("tg_no_files"))
			return
		}
		var b strings.Builder
		fmt.Fprintf(&b, "📂 %s\n", cwd)
		for _, f := range files {
			if len(files) > 15 {
				break
			}
			fmt.Fprintf(&b, "\n✎ %s", strings.TrimPrefix(strings.TrimPrefix(f, cwd), string(os.PathSeparator)))
		}
		reply(b.String())
	case "/pause", "/resume", "/stop":
		reply(strings.Join(a.runCommand(strings.TrimPrefix(cmd, "/")+" "+rest), "\n"))
	case "/new", "/mission", "/m":
		if rest == "" {
			a.mu.Lock()
			a.tgFocus = ""
			a.mu.Unlock()
			reply("🚀 " + a.T("tg_new_hint"))
			return
		}
		a.tgStartMission(chatID, rest)
	case "/msg":
		reply(strings.Join(a.runCommand("@"+rest), "\n"))
	default:
		if strings.HasPrefix(text, "/") {
			reply(a.T("tg_unknown"))
			return
		}
		if text == "" {
			return
		}
		if strings.HasPrefix(text, "@") { // @forge do this
			reply(strings.Join(a.runCommand(text), "\n"))
			return
		}
		if target != nil { // continue the conversation
			a.mu.Lock()
			a.tgFocus = target.ID
			name := target.Name
			a.mu.Unlock()
			out := a.runCommand("@" + name + " " + text)
			_, _ = a.tgSendTo(chatID, "📨 "+strings.Join(out, "\n"), true, nil)
			a.typing(target.ID)
			return
		}
		a.tgStartMission(chatID, text)
	}
}

func (a *App) tgStartMission(chatID, mission string) {
	a.mu.Lock()
	a.tgQuiet = true // the 🚀 reply below replaces the usual "new mission" message
	ag, err := a.startMission(mission, "", "", "", nil)
	a.tgQuiet = false
	if err == nil {
		a.tgFocus = ag.ID
		a.termAdd("out", a.T("term_mission", ag.Name)+" (Telegram)")
	}
	a.mu.Unlock()
	if err != nil {
		_, _ = a.tgSendTo(chatID, "❌ "+err.Error(), false, nil)
		return
	}
	a.tgAgentSend(ag.ID, "🚀 "+a.T("tg_started", ag.Name), true, map[string]any{"inline_keyboard": agentButtons(a, ag)})
	a.typing(ag.ID)
}

func (a *App) tgIncomingFile(fileID, name string, target *Agent) string {
	a.mu.Lock()
	dir := a.cfg.Workspace
	if target != nil {
		dir = target.Cwd
	}
	a.mu.Unlock()
	path, err := a.saveTgFile(fileID, name, dir)
	if err != nil {
		return "❌ " + err.Error()
	}
	return fmt.Sprintf("[The user sent a file via Telegram, saved at %s]", path)
}

func (a *App) tgAgentsList(chatID string) {
	a.mu.Lock()
	type item struct {
		text string
		kb   any
		id   string
	}
	var items []item
	for _, v := range a.agentViews() {
		if v.Status == "stopped" {
			continue
		}
		ag := v.Agent
		focus := ""
		if ag.ID == a.tgFocus {
			focus = " 💬"
		}
		t := fmt.Sprintf("%s %s%s%s — %s\n%s", statusEmoji[ag.Status], strings.Repeat("   ", ag.Depth), ag.Name, focus, a.T("k_"+ag.Kind), truncate(ag.Activity, 140))
		items = append(items, item{t, map[string]any{"inline_keyboard": agentButtons(a, ag)}, ag.ID})
	}
	a.mu.Unlock()
	if len(items) == 0 {
		_, _ = a.tgSendTo(chatID, "💤 "+a.T("card_none")+"\n"+a.T("tg_new_hint"), false, nil)
		return
	}
	for i, it := range items {
		if i >= 12 {
			break
		}
		a.tgAgentSend(it.id, it.text, true, it.kb)
	}
}

// tgAddTask handles "/task <project> <title>".
func (a *App) tgAddTask(rest string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.projects) == 0 {
		return a.T("projects_none")
	}
	var p *Project
	title := rest
	for _, x := range a.projects { // longest matching project name first word(s)
		if strings.HasPrefix(strings.ToLower(rest), strings.ToLower(x.Name)+" ") {
			p, title = x, strings.TrimSpace(rest[len(x.Name):])
		}
	}
	if p == nil {
		if len(a.projects) == 1 {
			p = a.projects[0]
		} else {
			names := []string{}
			for _, x := range a.projects {
				names = append(names, x.Name)
			}
			return a.T("tg_task_usage") + "\n" + strings.Join(names, ", ")
		}
	}
	if title == "" {
		return a.T("tg_task_usage")
	}
	p.addTask(title, "", "you")
	a.dirty = true
	return fmt.Sprintf("📌 %s → %s", title, p.Name)
}

// tgSchedules lists schedules, or creates one: "/schedule daily 08:00 <mission>".
func (a *App) tgSchedules(rest string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	f := strings.Fields(rest)
	if len(f) >= 3 && (f[0] == "daily" || f[0] == "dagelijks") {
		s := &Schedule{Kind: "daily", Time: f[1], Mission: strings.Join(f[2:], " "), By: "you"}
		if err := a.addSchedule(s); err != nil {
			return "❌ " + err.Error()
		}
		return "⏰ " + a.T("sch_created", s.Name, a.when(s))
	}
	if len(f) == 2 && (f[0] == "run" || f[0] == "off" || f[0] == "on") {
		s := a.scheduleByRef(f[1])
		if s == nil {
			return a.T("unknown_agent", f[1])
		}
		if f[0] == "run" {
			if _, err := a.runSchedule(s); err != nil {
				return "❌ " + err.Error()
			}
			return "▶️ " + s.Name
		}
		s.Enabled = f[0] == "on"
		s.NextRun = s.next(time.Now())
		a.dirty = true
		return map[bool]string{true: "✅ ", false: "⏸ "}[s.Enabled] + s.Name
	}
	if len(a.schedules) == 0 {
		return a.T("tg_sch_none")
	}
	var b strings.Builder
	b.WriteString("⏰ " + a.T("tg_sch_title"))
	for i, s := range a.schedules {
		state := "▶"
		next := s.NextRun.Format("Mon 15:04")
		if !s.Enabled {
			state, next = "⏸", "—"
		}
		fmt.Fprintf(&b, "\n\n%d. %s %s\n   %s · %s %s", i+1, state, s.Name, a.when(s), a.T("tg_next"), next)
	}
	b.WriteString("\n\n" + a.T("tg_sch_hint"))
	return b.String()
}
