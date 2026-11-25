package main

// Minimal MCP server (streamable HTTP, JSON responses) that every agent connects to.

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type rpcReq struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

func schema(props map[string]any, required ...string) map[string]any {
	if required == nil {
		required = []string{} // Claude Code rejects "required": null
	}
	return map[string]any{"type": "object", "properties": props, "required": required}
}

func sProp(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

var mcpTools = []map[string]any{
	{"name": "report", "description": "Send the user a status message. level=update for progress at meaningful milestones; level=critical when something is blocked, broken or needs attention ASAP.",
		"inputSchema": schema(map[string]any{"level": map[string]any{"type": "string", "enum": []string{"update", "critical"}}, "message": sProp("Short message for the user"), "screenshot": map[string]any{"type": "boolean", "description": "Attach a picture of your current Agent HQ browser page"}}, "level", "message")},
	{"name": "ask_decision", "description": "Ask the user to make a decision. Waits for the answer (can take minutes). The user may pick an option or write a custom answer.",
		"inputSchema": schema(map[string]any{"question": sProp("The question"), "options": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "2-4 short options"}, "context": sProp("Optional extra context")}, "question", "options")},
	{"name": "spawn_agent", "description": "Create a sub-agent that works in parallel on a self-contained mission. It reports back to you automatically.",
		"inputSchema": schema(map[string]any{"name": sProp("Short lowercase name, e.g. researcher"), "role": sProp("Role in a few words"), "mission": sProp("Complete, self-contained mission"), "model": sProp("Optional: opus, sonnet or haiku")}, "name", "role", "mission")},
	{"name": "message_agent", "description": "Send a message to another agent by name.",
		"inputSchema": schema(map[string]any{"to": sProp("Agent name"), "message": sProp("Message")}, "to", "message")},
	{"name": "list_agents", "description": "List all agents with role, status and current activity.", "inputSchema": schema(map[string]any{})},
	{"name": "complete", "description": "Mark your mission as completed with a short summary of the result.",
		"inputSchema": schema(map[string]any{"summary": sProp("Short summary of the result")}, "summary")},
	{"name": "approve", "description": "Internal: permission prompt handler used by Agent HQ.",
		"inputSchema": schema(map[string]any{"tool_name": sProp(""), "input": map[string]any{"type": "object"}, "tool_use_id": sProp("")})},
}

func (a *App) handleMCP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/mcp/")
	if r.URL.Query().Get("k") != a.secret {
		http.Error(w, "forbidden", 403)
		return
	}
	var req rpcReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	if len(req.ID) == 0 || strings.HasPrefix(req.Method, "notifications/") {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	reply := func(result any, rpcErr map[string]any) {
		resp := map[string]any{"jsonrpc": "2.0", "id": req.ID}
		if rpcErr != nil {
			resp["error"] = rpcErr
		} else {
			resp["result"] = result
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		if p.ProtocolVersion == "" {
			p.ProtocolVersion = "2025-06-18"
		}
		reply(map[string]any{"protocolVersion": p.ProtocolVersion, "capabilities": map[string]any{"tools": map[string]any{}},
			"serverInfo": map[string]any{"name": "agent-hq", "version": version}}, nil)
	case "ping":
		reply(map[string]any{}, nil)
	case "tools/list":
		tools := append(append([]map[string]any{}, mcpTools...), browserTools...)
		tools = append(append(tools, projectTools...), githubTools...)
		tools = append(append(tools, scheduleTool, deployTool), libraryTools...)
		reply(map[string]any{"tools": tools}, nil)
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		_ = json.Unmarshal(req.Params, &p)
		if p.Name == "deploy" {
			a.mu.Lock()
			ag := a.agents[id]
			var proj *Project
			if ag != nil {
				proj = a.projectByID(ag.ProjectID)
				ag.ToolKind, ag.ToolTarget = "app", "deploy"
			}
			a.mu.Unlock()
			text, isErr := "", true
			if proj == nil {
				text = "Deploy works for projects. Call create_project first (it uses your folder), then deploy."
			} else if u, err := a.runDeploy(proj, str(p.Arguments, "target"), func(s string) { a.mu.Lock(); a.addLog(ag, "tool", "deploy: "+s); a.mu.Unlock() }); err != nil {
				text = "Deploy failed: " + err.Error()
			} else {
				text, isErr = "Live at "+u, false
			}
			reply(map[string]any{"content": []map[string]any{{"type": "text", "text": text}}, "isError": isErr}, nil)
			return
		}
		if strings.HasPrefix(p.Name, "github_") {
			a.mu.Lock()
			ag := a.agents[id]
			a.mu.Unlock()
			if ag != nil {
				text, isErr := a.githubTool(r, ag, p.Name, p.Arguments)
				reply(map[string]any{"content": []map[string]any{{"type": "text", "text": text}}, "isError": isErr}, nil)
				return
			}
		}
		if strings.HasPrefix(p.Name, "browser_") {
			a.mu.Lock()
			ag := a.agents[id]
			a.mu.Unlock()
			if ag == nil {
				reply(map[string]any{"content": []map[string]any{{"type": "text", "text": "unknown agent"}}, "isError": true}, nil)
				return
			}
			content, isErr := a.browserTool(ag, p.Name, p.Arguments)
			reply(map[string]any{"content": content, "isError": isErr}, nil)
			return
		}
		text, isErr := a.callTool(r, id, p.Name, p.Arguments)
		reply(map[string]any{"content": []map[string]any{{"type": "text", "text": text}}, "isError": isErr}, nil)
	default:
		reply(nil, map[string]any{"code": -32601, "message": "method not found"})
	}
}

func (a *App) callTool(r *http.Request, agentID, name string, args map[string]any) (string, bool) {
	a.mu.Lock()
	ag := a.agents[agentID]
	if ag == nil {
		a.mu.Unlock()
		return "unknown agent", true
	}
	switch name {
	case "remember", "forget", "save_skill", "use_skill":
		text, isErr := a.libraryTool(ag, name, args)
		a.mu.Unlock()
		return text, isErr
	case "schedule_mission":
		text, isErr := a.scheduleFromTool(ag, args)
		a.mu.Unlock()
		return text, isErr
	case "project_tasks", "add_task", "update_task", "create_project":
		text, isErr := a.projectTool(ag, name, args)
		a.mu.Unlock()
		return text, isErr
	case "report":
		msg := str(args, "message")
		level := str(args, "level")
		if level != "critical" {
			level = "update"
		}
		a.addLog(ag, "tool", "reports: "+msg)
		if level == "update" && str(args, "screenshot") == "true" && a.browser.get(ag.ID) != nil {
			level = "shot"
		}
		a.emit(level, ag.ID, ag.Name+": "+msg)
		a.mu.Unlock()
		return "Delivered to the user.", false
	case "spawn_agent":
		child, err := a.createAgent(str(args, "name"), str(args, "role"), str(args, "mission"), ag, "", str(args, "model"), nil)
		a.mu.Unlock()
		if err != nil {
			return err.Error(), true
		}
		return fmt.Sprintf("Created agent %q. It works in parallel and will report back to you when it finishes. End your turn if you need to wait for it.", child.Name), false
	case "message_agent":
		to := a.agentByRef(str(args, "to"))
		if to == nil || to.Status == "stopped" {
			a.mu.Unlock()
			return "No active agent with that name. Use list_agents.", true
		}
		msg := str(args, "message")
		a.deliver(to, fmt.Sprintf(dict["en"]["agent_msg"], ag.Name, msg), false)
		a.emit("chatter", ag.ID, ag.Name+" → "+to.Name+": "+truncate(msg, 200))
		a.mu.Unlock()
		return "Message delivered to " + to.Name + ".", false
	case "list_agents":
		var b strings.Builder
		for _, v := range a.agentViews() {
			parent := ""
			if p := a.agents[v.ParentID]; p != nil {
				parent = " (child of " + p.Name + ")"
			}
			fmt.Fprintf(&b, "- %s%s — %s — %s — %s\n", v.Name, parent, v.Role, v.Status, truncate(v.Activity, 80))
		}
		a.mu.Unlock()
		return b.String(), false
	case "complete":
		ag.Completed = true
		ag.Summary = str(args, "summary")
		a.addLog(ag, "tool", "complete: "+ag.Summary)
		a.mu.Unlock()
		return "Marked as complete. Finish your turn now.", false
	case "ask_decision":
		opts := strList(args, "options")
		if len(opts) == 0 {
			opts = []string{"Yes", "No"}
		}
		if len(opts) > 6 {
			opts = opts[:6]
		}
		d := a.newDecision(ag, "decision", str(args, "question"), str(args, "context"), opts)
		a.mu.Unlock()
		ans, ok := a.waitDecision(r, d, 10*time.Minute)
		if !ok {
			return "The user has not answered yet. End your turn now; the answer will be delivered to you as a new message as soon as they respond. Do not ask the same question again.", false
		}
		if ans == "" {
			return "The decision was cancelled (you were paused or stopped).", true
		}
		return "The user answered: " + ans, false
	case "approve":
		tool := str(args, "tool_name")
		in, _ := args["input"].(map[string]any)
		detail := toolSummary(tool, in)
		if c := str(in, "command"); c != "" {
			detail = c
		}
		allow := func() (string, bool) {
			if in == nil {
				in = map[string]any{}
			}
			b, _ := json.Marshal(map[string]any{"behavior": "allow", "updatedInput": in})
			return string(b), false
		}
		if a.autoAllowed(ag, tool, in) { // read-only, remembered or trusted: no question
			a.addLog(ag, "info", "auto-allowed: "+truncate(detail, 120))
			a.mu.Unlock()
			return allow()
		}
		d := a.newDecision(ag, "permission", a.T("perm_q", ag.Name, tool), truncate(detail, 600), a.permOptions())
		a.mu.Unlock()
		ans, ok := a.waitDecision(r, d, 15*time.Minute)
		deny := func(msg string) (string, bool) {
			b, _ := json.Marshal(map[string]any{"behavior": "deny", "message": msg})
			return string(b), false
		}
		if !ok {
			a.mu.Lock()
			if d.Status == "pending" {
				d.Status = "expired"
				if d.tgMsgID != 0 {
					go a.tgMarkAnswered(d)
				}
			}
			a.mu.Unlock()
			return deny("No answer from the user in time. Try another approach, or ask with ask_decision.")
		}
		switch ans {
		case a.T("allow_always"), dict["en"]["allow_always"], dict["nl"]["allow_always"]:
			a.mu.Lock()
			rule := ruleFor(tool, in)
			a.cfg.AllowRules = append(a.cfg.AllowRules, rule)
			_ = saveConfig(a.cfg)
			a.addLog(ag, "info", "always allowed: "+rule)
			a.mu.Unlock()
			return allow()
		case a.T("allow_agent"), dict["en"]["allow_agent"], dict["nl"]["allow_agent"]:
			a.mu.Lock()
			ag.TrustAll = true
			a.dirty = true
			a.mu.Unlock()
			return allow()
		}
		if isAllow(ans, a) {
			return allow()
		}
		if ans == "" {
			return deny("Cancelled.")
		}
		return deny("The user denied this. Their note: " + ans)
	}
	a.mu.Unlock()
	return "unknown tool", true
}

func isAllow(ans string, a *App) bool {
	l := strings.ToLower(strings.TrimSpace(ans))
	for _, w := range []string{"allow", "toestaan", "yes", "ja", "ok", "👍", strings.ToLower(dict["en"]["allow"]), strings.ToLower(dict["nl"]["allow"])} {
		if l == w {
			return true
		}
	}
	return false
}

// waitDecision blocks until answered/cancelled (ok=true) or timeout (ok=false).
func (a *App) waitDecision(r *http.Request, d *Decision, timeout time.Duration) (string, bool) {
	select {
	case ans := <-d.ch:
		return ans, true
	case <-time.After(timeout):
	case <-r.Context().Done():
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	select { // answered in the meantime
	case ans := <-d.ch:
		return ans, true
	default:
	}
	d.late = true
	return "", false
}

func encodeB64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }
