package main

// Connectors: MCP servers that Agent HQ hands to its agents (in addition to the
// servers the user already configured in Claude Code itself).

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Connector struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Preset   string            `json:"preset"`
	Kind     string            `json:"kind"` // stdio | http
	Command  string            `json:"command"`
	Args     []string          `json:"args"`
	Env      map[string]string `json:"env"`
	URL      string            `json:"url"`
	Headers  map[string]string `json:"headers"`
	Enabled  bool              `json:"enabled"`
	Approval bool              `json:"approval"` // ask before each tool call
	Tools    []string          `json:"tools"`
	Tested   time.Time         `json:"tested"`
	Error    string            `json:"error"`
}

type PresetField struct {
	Key         string `json:"key"`    // env var / header name / "url" / "arg"
	Target      string `json:"target"` // env | header | url | arg
	Label       string `json:"label"`
	Placeholder string `json:"placeholder"`
	Secret      bool   `json:"secret"`
	Prefix      string `json:"prefix"`
}

type Preset struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Desc    map[string]string `json:"desc"`
	Kind    string            `json:"kind"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	URL     string            `json:"url"`
	Fields  []PresetField     `json:"fields"`
	Docs    string            `json:"docs"`
	Needs   string            `json:"needs"` // runtime requirement shown to user
}

var presets = []Preset{
	{ID: "github", Name: "GitHub", Kind: "http", URL: "https://api.githubcopilot.com/mcp/",
		Desc:   map[string]string{"en": "Repos, issues, pull requests and code search.", "nl": "Repo's, issues, pull requests en code zoeken."},
		Fields: []PresetField{{Key: "Authorization", Target: "header", Label: "Personal access token", Placeholder: "github_pat_…", Secret: true, Prefix: "Bearer "}},
		Docs:   "https://github.com/github/github-mcp-server"},
	{ID: "playwright", Name: "Browser (Playwright)", Kind: "stdio", Command: "npx", Args: []string{"-y", "@playwright/mcp@latest"},
		Desc: map[string]string{"en": "Opens web pages, clicks, fills in forms, takes screenshots.", "nl": "Opent webpagina's, klikt, vult formulieren in, maakt screenshots."},
		Docs: "https://github.com/microsoft/playwright-mcp", Needs: "Node.js"},
	{ID: "filesystem", Name: "Folders", Kind: "stdio", Command: "npx", Args: []string{"-y", "@modelcontextprotocol/server-filesystem"},
		Desc:   map[string]string{"en": "Read and write files in folders outside the mission folder.", "nl": "Bestanden lezen en schrijven in mappen buiten de missiemap."},
		Fields: []PresetField{{Key: "arg", Target: "arg", Label: "Folder", Placeholder: `C:\Users\Michael\Documents`}},
		Docs:   "https://github.com/modelcontextprotocol/servers/tree/main/src/filesystem", Needs: "Node.js"},
	{ID: "homeassistant", Name: "Home Assistant", Kind: "http", URL: "http://homeassistant.local:8123/api/mcp",
		Desc: map[string]string{"en": "Control lights, switches and sensors in your smart home.", "nl": "Bedien lampen, schakelaars en sensoren in je slimme huis."},
		Fields: []PresetField{{Key: "url", Target: "url", Label: "Server URL", Placeholder: "http://homeassistant.local:8123/api/mcp"},
			{Key: "Authorization", Target: "header", Label: "Long-lived access token", Secret: true, Prefix: "Bearer "}},
		Docs: "https://www.home-assistant.io/integrations/mcp_server/"},
	{ID: "notion", Name: "Notion", Kind: "stdio", Command: "npx", Args: []string{"-y", "@notionhq/notion-mcp-server"},
		Desc:   map[string]string{"en": "Search, read and edit pages and databases.", "nl": "Pagina's en databases zoeken, lezen en bewerken."},
		Fields: []PresetField{{Key: "NOTION_TOKEN", Target: "env", Label: "Integration token", Placeholder: "ntn_…", Secret: true}},
		Docs:   "https://github.com/makenotion/notion-mcp-server", Needs: "Node.js"},
	{ID: "brave", Name: "Brave Search", Kind: "stdio", Command: "npx", Args: []string{"-y", "@brave/brave-search-mcp-server"},
		Desc:   map[string]string{"en": "Web, news and image search with your own API key.", "nl": "Web, nieuws en afbeeldingen zoeken met je eigen API-key."},
		Fields: []PresetField{{Key: "BRAVE_API_KEY", Target: "env", Label: "API key", Secret: true}},
		Docs:   "https://github.com/brave/brave-search-mcp-server", Needs: "Node.js"},
	{ID: "context7", Name: "Context7 docs", Kind: "stdio", Command: "npx", Args: []string{"-y", "@upstash/context7-mcp"},
		Desc: map[string]string{"en": "Up-to-date documentation for libraries and frameworks.", "nl": "Actuele documentatie van libraries en frameworks."},
		Docs: "https://github.com/upstash/context7", Needs: "Node.js"},
	{ID: "memory", Name: "Shared memory", Kind: "stdio", Command: "npx", Args: []string{"-y", "@modelcontextprotocol/server-memory"},
		Desc: map[string]string{"en": "A knowledge graph that agents can share between missions.", "nl": "Een kennisgraaf die agents tussen missies kunnen delen."},
		Docs: "https://github.com/modelcontextprotocol/servers/tree/main/src/memory", Needs: "Node.js"},
	{ID: "fetch", Name: "Fetch", Kind: "stdio", Command: "uvx", Args: []string{"mcp-server-fetch"},
		Desc: map[string]string{"en": "Download web pages and convert them to text.", "nl": "Webpagina's ophalen en omzetten naar tekst."},
		Docs: "https://github.com/modelcontextprotocol/servers/tree/main/src/fetch", Needs: "Python (uv)"},
}

func (c *Connector) key() string {
	k := strings.ReplaceAll(slug(c.Name), "-", "_")
	if k == "hq" || k == "" {
		k = "c_" + c.ID
	}
	return k
}

func (c *Connector) mcpEntry() map[string]any {
	if c.Kind == "http" {
		e := map[string]any{"type": "http", "url": c.URL}
		if len(c.Headers) > 0 {
			e["headers"] = c.Headers
		}
		return e
	}
	e := map[string]any{"type": "stdio", "command": c.Command, "args": c.Args}
	if len(c.Env) > 0 {
		e["env"] = c.Env
	}
	return e
}

func maskVal(v string) string {
	if len(v) <= 8 {
		return "••••"
	}
	return v[:4] + "••••" + v[len(v)-3:]
}

func maskedConnector(c Connector) Connector {
	m := c
	m.Env, m.Headers = map[string]string{}, map[string]string{}
	for k, v := range c.Env {
		m.Env[k] = maskVal(v)
	}
	for k, v := range c.Headers {
		m.Headers[k] = maskVal(v)
	}
	return m
}

// mergeSecrets keeps old secret values when the UI sends back masked ones.
func mergeSecrets(in map[string]string, old map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range in {
		if strings.Contains(v, "••••") {
			if ov, ok := old[k]; ok {
				out[k] = ov
			}
			continue
		}
		if strings.TrimSpace(v) != "" {
			out[k] = strings.TrimSpace(v)
		}
	}
	return out
}

func (a *App) connectorByID(id string) *Connector {
	for i := range a.cfg.Connectors {
		if a.cfg.Connectors[i].ID == id {
			return &a.cfg.Connectors[i]
		}
	}
	return nil
}

// agentConnectors returns the enabled connectors an agent may use.
func (a *App) agentConnectors(ag *Agent) []*Connector {
	var out []*Connector
	for i := range a.cfg.Connectors {
		c := &a.cfg.Connectors[i]
		if !c.Enabled {
			continue
		}
		if ag.Connectors != nil {
			ok := false
			for _, id := range ag.Connectors {
				if id == c.ID {
					ok = true
				}
			}
			if !ok {
				continue
			}
		}
		out = append(out, c)
	}
	return out
}

// ---------- Claude Code's own MCP servers (read only) ----------

type ExternalServer struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Target string `json:"target"`
	Scope  string `json:"scope"`
}

func claudeCodeServers() []ExternalServer {
	h, _ := os.UserHomeDir()
	var out []ExternalServer
	var m map[string]any
	if readJSON(filepath.Join(h, ".claude.json"), &m) != nil {
		return out
	}
	add := func(servers map[string]any, scope string) {
		for name, v := range servers {
			s, _ := v.(map[string]any)
			kind := str(s, "type")
			if kind == "" {
				kind = "stdio"
			}
			target := str(s, "url")
			if target == "" {
				target = strings.TrimSpace(str(s, "command") + " " + strings.Join(strList(s, "args"), " "))
			}
			out = append(out, ExternalServer{Name: name, Kind: kind, Target: truncate(target, 80), Scope: scope})
		}
	}
	if s, ok := m["mcpServers"].(map[string]any); ok {
		add(s, "user")
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ---------- connection test ----------

func testConnector(c Connector) ([]string, error) {
	if c.Kind == "http" {
		return testHTTP(c)
	}
	return testStdio(c)
}

func toolNames(res map[string]any) []string {
	var names []string
	if ts, ok := res["tools"].([]any); ok {
		for _, t := range ts {
			if tm, ok := t.(map[string]any); ok {
				names = append(names, str(tm, "name"))
			}
		}
	}
	return names
}

func testStdio(c Connector) ([]string, error) {
	if c.Command == "" {
		return nil, errors.New("no command")
	}
	cmd := hiddenCmd(c.Command, c.Args...)
	cmd.Env = os.Environ()
	for k, v := range c.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	setProcGroup(cmd)
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	stderr := &tailBuffer{max: 2000}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("cannot start %q: %v", c.Command, err)
	}
	defer killTree(cmd)
	lines := make(chan map[string]any, 16)
	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
		for sc.Scan() {
			var m map[string]any
			if json.Unmarshal(sc.Bytes(), &m) == nil {
				lines <- m
			}
		}
		close(lines)
	}()
	send := func(v any) { b, _ := json.Marshal(v); _, _ = stdin.Write(append(b, '\n')) }
	wait := func(id float64, timeout time.Duration) (map[string]any, error) {
		dl := time.After(timeout)
		for {
			select {
			case m, ok := <-lines:
				if !ok {
					return nil, fmt.Errorf("server exited: %s", strings.TrimSpace(string(stderr.b)))
				}
				if mid, _ := m["id"].(float64); mid == id {
					if e, ok := m["error"].(map[string]any); ok {
						return nil, fmt.Errorf("%v", e["message"])
					}
					r, _ := m["result"].(map[string]any)
					return r, nil
				}
			case <-dl:
				return nil, fmt.Errorf("no answer within %s %s", timeout, strings.TrimSpace(string(stderr.b)))
			}
		}
	}
	send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{
		"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "agent-hq", "version": version}}})
	if _, err := wait(1, 90*time.Second); err != nil { // npx may download first
		return nil, err
	}
	send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	send(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"})
	r, err := wait(2, 30*time.Second)
	if err != nil {
		return nil, err
	}
	return toolNames(r), nil
}

func testHTTP(c Connector) ([]string, error) {
	client := &http.Client{Timeout: 20 * time.Second}
	session := ""
	call := func(id int, method string, params any) (map[string]any, error) {
		body := map[string]any{"jsonrpc": "2.0", "method": method}
		if id > 0 {
			body["id"] = id
		}
		if params != nil {
			body["params"] = params
		}
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", c.URL, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("MCP-Protocol-Version", "2025-06-18")
		if session != "" {
			req.Header.Set("Mcp-Session-Id", session)
		}
		for k, v := range c.Headers {
			req.Header.Set(k, v)
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if s := resp.Header.Get("Mcp-Session-Id"); s != "" {
			session = s
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		if resp.StatusCode == 401 || resp.StatusCode == 403 {
			return nil, fmt.Errorf("HTTP %d: token missing or not accepted", resp.StatusCode)
		}
		if resp.StatusCode >= 300 {
			return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(raw), 160))
		}
		if id == 0 {
			return nil, nil
		}
		payload := raw
		if strings.Contains(resp.Header.Get("Content-Type"), "event-stream") {
			for _, ln := range strings.Split(string(raw), "\n") {
				if strings.HasPrefix(ln, "data:") {
					payload = []byte(strings.TrimSpace(strings.TrimPrefix(ln, "data:")))
				}
			}
		}
		var m map[string]any
		if err := json.Unmarshal(payload, &m); err != nil {
			return nil, fmt.Errorf("unexpected answer: %s", truncate(string(raw), 120))
		}
		if e, ok := m["error"].(map[string]any); ok {
			return nil, fmt.Errorf("%v", e["message"])
		}
		r, _ := m["result"].(map[string]any)
		return r, nil
	}
	if _, err := call(1, "initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{},
		"clientInfo": map[string]any{"name": "agent-hq", "version": version}}); err != nil {
		return nil, err
	}
	_, _ = call(0, "notifications/initialized", nil)
	r, err := call(2, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	return toolNames(r), nil
}

// ---------- HTTP API ----------

func (a *App) connectorRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/connectors", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		list := []Connector{}
		for _, c := range a.cfg.Connectors {
			list = append(list, maskedConnector(c))
		}
		a.mu.Unlock()
		writeJSON(w, map[string]any{"connectors": list, "presets": presets, "claudeCode": claudeCodeServers()})
	})
	mux.HandleFunc("/api/connectors/save", func(w http.ResponseWriter, r *http.Request) {
		var in Connector
		if err := readBody(r, &in); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		in.Name = strings.TrimSpace(in.Name)
		if in.Name == "" {
			http.Error(w, "name required", 400)
			return
		}
		if in.Kind == "http" && !strings.HasPrefix(in.URL, "http") {
			http.Error(w, "URL must start with http:// or https://", 400)
			return
		}
		if in.Kind != "http" && strings.TrimSpace(in.Command) == "" {
			http.Error(w, "command required", 400)
			return
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		if old := a.connectorByID(in.ID); old != nil {
			in.Env, in.Headers = mergeSecrets(in.Env, old.Env), mergeSecrets(in.Headers, old.Headers)
			in.Tools, in.Tested, in.Error = old.Tools, old.Tested, old.Error
			*old = in
		} else {
			in.ID = newID()
			in.Env, in.Headers = mergeSecrets(in.Env, nil), mergeSecrets(in.Headers, nil)
			a.cfg.Connectors = append(a.cfg.Connectors, in)
		}
		if !a.demo {
			_ = saveConfig(a.cfg)
		}
		writeJSON(w, map[string]string{"id": in.ID})
	})
	mux.HandleFunc("/api/connectors/toggle", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			ID      string
			Enabled bool
		}
		_ = readBody(r, &in)
		a.mu.Lock()
		defer a.mu.Unlock()
		if c := a.connectorByID(in.ID); c != nil {
			c.Enabled = in.Enabled
			if !a.demo {
				_ = saveConfig(a.cfg)
			}
		}
		writeJSON(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("/api/connectors/delete", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ ID string }
		_ = readBody(r, &in)
		a.mu.Lock()
		defer a.mu.Unlock()
		out := a.cfg.Connectors[:0]
		for _, c := range a.cfg.Connectors {
			if c.ID != in.ID {
				out = append(out, c)
			}
		}
		a.cfg.Connectors = out
		if !a.demo {
			_ = saveConfig(a.cfg)
		}
		writeJSON(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("/api/connectors/test", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ ID string }
		_ = readBody(r, &in)
		a.mu.Lock()
		c := a.connectorByID(in.ID)
		if c == nil {
			a.mu.Unlock()
			http.Error(w, "not found", 404)
			return
		}
		cp := *c
		a.mu.Unlock()
		tools, err := testConnector(cp)
		a.mu.Lock()
		if c := a.connectorByID(in.ID); c != nil {
			c.Tested = time.Now()
			if err != nil {
				c.Error = err.Error()
			} else {
				c.Error, c.Tools = "", tools
			}
			if !a.demo {
				_ = saveConfig(a.cfg)
			}
		}
		a.mu.Unlock()
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		writeJSON(w, map[string]any{"tools": tools})
	})
}
