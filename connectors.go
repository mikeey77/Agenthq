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
	ID       string              `json:"id"`
	Name     string              `json:"name"`
	Desc     map[string]string   `json:"desc"`
	Kind     string              `json:"kind"`
	Command  string              `json:"command"`
	Args     []string            `json:"args"`
	URL      string              `json:"url"`
	Fields   []PresetField       `json:"fields"`
	Docs     string              `json:"docs"`
	Needs    string              `json:"needs"` // runtime requirement shown to user
	Category string              `json:"category"`
	Examples map[string][]string `json:"examples"`
}

var presets = []Preset{
	{ID: "github", Name: "GitHub", Kind: "http", URL: "https://api.githubcopilot.com/mcp/",
		Desc:   map[string]string{"en": "Repos, issues, pull requests and code search.", "nl": "Repo's, issues, pull requests en code zoeken."},
		Fields: []PresetField{{Key: "Authorization", Target: "header", Label: "Personal access token", Placeholder: "github_pat_…", Secret: true, Prefix: "Bearer "}},
		Docs:   "https://github.com/github/github-mcp-server", Category: "code",
		Examples: map[string][]string{"en": {"List my repositories and which ones changed this week", "Open an issue in agent-hq for every TODO in the code", "Review the open pull requests and summarize them"}, "nl": {"Geef mijn repositories en welke deze week veranderd zijn", "Maak een issue in agent-hq voor elke TODO in de code", "Bekijk de open pull requests en vat ze samen"}}},
	{ID: "playwright", Name: "Browser (Playwright)", Kind: "stdio", Command: "npx", Args: []string{"-y", "@playwright/mcp@latest"},
		Desc: map[string]string{"en": "Opens web pages, clicks, fills in forms, takes screenshots.", "nl": "Opent webpagina's, klikt, vult formulieren in, maakt screenshots."},
		Docs: "https://github.com/microsoft/playwright-mcp", Needs: "Node.js", Category: "web",
		Examples: map[string][]string{"en": {"Check if my Netlify sites load and take a screenshot of each", "Find the cheapest price for a product on three web shops", "Fill in a test order on my staging shop"}, "nl": {"Controleer of mijn Netlify-sites laden en maak van elk een screenshot", "Zoek de laagste prijs van een product bij drie webshops", "Doe een testbestelling op mijn staging-shop"}}},
	{ID: "filesystem", Name: "Folders", Kind: "stdio", Command: "npx", Args: []string{"-y", "@modelcontextprotocol/server-filesystem"},
		Desc:   map[string]string{"en": "Read and write files in folders outside the mission folder.", "nl": "Bestanden lezen en schrijven in mappen buiten de missiemap."},
		Fields: []PresetField{{Key: "arg", Target: "arg", Label: "Folder", Placeholder: `C:\Users\Michael\Documents`}},
		Docs:   "https://github.com/modelcontextprotocol/servers/tree/main/src/filesystem", Needs: "Node.js", Category: "files",
		Examples: map[string][]string{"en": {"Sort my Downloads folder into subfolders by type", "Find duplicate photos in Pictures", "Make a list of every PDF in Documents with its size"}, "nl": {"Sorteer mijn Downloads-map in submappen per soort", "Zoek dubbele foto's in Afbeeldingen", "Maak een lijst van alle pdf's in Documenten met hun grootte"}}},
	{ID: "homeassistant", Name: "Home Assistant", Kind: "http", URL: "http://homeassistant.local:8123/api/mcp",
		Desc: map[string]string{"en": "Control lights, switches and sensors in your smart home.", "nl": "Bedien lampen, schakelaars en sensoren in je slimme huis."},
		Fields: []PresetField{{Key: "url", Target: "url", Label: "Server URL", Placeholder: "http://homeassistant.local:8123/api/mcp"},
			{Key: "Authorization", Target: "header", Label: "Long-lived access token", Secret: true, Prefix: "Bearer "}},
		Docs: "https://www.home-assistant.io/integrations/mcp_server/", Category: "home",
		Examples: map[string][]string{"en": {"Turn off every light that is still on", "Tell me which sensors went offline today", "Make an automation idea list for my new house"}, "nl": {"Zet alle lampen uit die nog aan staan", "Vertel welke sensoren vandaag offline gingen", "Maak een lijst met automatisering-ideeën voor mijn nieuwe huis"}}},
	{ID: "notion", Name: "Notion", Kind: "stdio", Command: "npx", Args: []string{"-y", "@notionhq/notion-mcp-server"},
		Desc:   map[string]string{"en": "Search, read and edit pages and databases.", "nl": "Pagina's en databases zoeken, lezen en bewerken."},
		Fields: []PresetField{{Key: "NOTION_TOKEN", Target: "env", Label: "Integration token", Placeholder: "ntn_…", Secret: true}},
		Docs:   "https://github.com/makenotion/notion-mcp-server", Needs: "Node.js", Category: "knowledge",
		Examples: map[string][]string{"en": {"Put the results of this mission in a new Notion page", "Summarize my Notion tasks for this week", "Turn my meeting notes into a to-do list"}, "nl": {"Zet de resultaten van deze missie op een nieuwe Notion-pagina", "Vat mijn Notion-taken van deze week samen", "Maak van mijn notities een takenlijst"}}},
	{ID: "brave", Name: "Brave Search", Kind: "stdio", Command: "npx", Args: []string{"-y", "@brave/brave-search-mcp-server"},
		Desc:   map[string]string{"en": "Web, news and image search with your own API key.", "nl": "Web, nieuws en afbeeldingen zoeken met je eigen API-key."},
		Fields: []PresetField{{Key: "BRAVE_API_KEY", Target: "env", Label: "API key", Secret: true}},
		Docs:   "https://github.com/brave/brave-search-mcp-server", Needs: "Node.js", Category: "web",
		Examples: map[string][]string{"en": {"Research the best offline maps for Android in 2026", "Find news about Claude Code this week", "Compare three robot arm kits under €300"}, "nl": {"Zoek de beste offline kaarten voor Android in 2026", "Zoek nieuws over Claude Code van deze week", "Vergelijk drie robotarm-kits onder €300"}}},
	{ID: "context7", Name: "Context7 docs", Kind: "stdio", Command: "npx", Args: []string{"-y", "@upstash/context7-mcp"},
		Desc: map[string]string{"en": "Up-to-date documentation for libraries and frameworks.", "nl": "Actuele documentatie van libraries en frameworks."},
		Docs: "https://github.com/upstash/context7", Needs: "Node.js", Category: "code",
		Examples: map[string][]string{"en": {"Use the latest Expo docs to fix my build", "Explain how the newest Go release changed generics", "Upgrade my project to the latest Vite"}, "nl": {"Gebruik de nieuwste Expo-docs om mijn build te fixen", "Leg uit wat de nieuwste Go-versie aan generics veranderde", "Upgrade mijn project naar de nieuwste Vite"}}},
	{ID: "memory", Name: "Shared memory", Kind: "stdio", Command: "npx", Args: []string{"-y", "@modelcontextprotocol/server-memory"},
		Desc: map[string]string{"en": "A knowledge graph that agents can share between missions.", "nl": "Een kennisgraaf die agents tussen missies kunnen delen."},
		Docs: "https://github.com/modelcontextprotocol/servers/tree/main/src/memory", Needs: "Node.js", Category: "knowledge",
		Examples: map[string][]string{"en": {"Remember the decisions we made in this mission", "What did we decide last week about the app design?", "Keep a list of facts about my smart home setup"}, "nl": {"Onthoud de beslissingen uit deze missie", "Wat besloten we vorige week over het app-design?", "Houd een lijst bij met feiten over mijn smart home"}}},
	{ID: "fetch", Name: "Fetch", Kind: "stdio", Command: "uvx", Args: []string{"mcp-server-fetch"},
		Desc: map[string]string{"en": "Download web pages and convert them to text.", "nl": "Webpagina's ophalen en omzetten naar tekst."},
		Docs: "https://github.com/modelcontextprotocol/servers/tree/main/src/fetch", Needs: "Python (uv)", Category: "web",
		Examples: map[string][]string{"en": {"Read this article and summarize it in 5 points", "Download the changelog of a project and list what's new", "Check a web page for broken links"}, "nl": {"Lees dit artikel en vat het samen in 5 punten", "Haal de changelog van een project op en zet het nieuwe op een rij", "Controleer een webpagina op kapotte links"}}},
	{ID: "git", Name: "Git", Kind: "stdio", Command: "uvx", Args: []string{"mcp-server-git", "--repository"}, Category: "code",
		Desc:   map[string]string{"en": "Read history, diffs and branches of a local repository.", "nl": "Geschiedenis, diffs en branches van een lokale repository lezen."},
		Fields: []PresetField{{Key: "arg", Target: "arg", Label: "Repository folder", Placeholder: `C:\Users\Michael\Projects\agent-hq`}},
		Docs:   "https://github.com/modelcontextprotocol/servers/tree/main/src/git", Needs: "Python (uv)",
		Examples: map[string][]string{"en": {"Write release notes from the commits since the last tag", "Which files changed most this month?", "Explain what the last 10 commits did"}, "nl": {"Schrijf release notes uit de commits sinds de laatste tag", "Welke bestanden veranderden deze maand het meest?", "Leg uit wat de laatste 10 commits deden"}}},
	{ID: "time", Name: "Time", Kind: "stdio", Command: "uvx", Args: []string{"mcp-server-time", "--local-timezone=Europe/Amsterdam"}, Category: "utilities",
		Desc: map[string]string{"en": "Current time and time zone conversions.", "nl": "Huidige tijd en tijdzones omrekenen."},
		Docs: "https://github.com/modelcontextprotocol/servers/tree/main/src/time", Needs: "Python (uv)",
		Examples: map[string][]string{"en": {"What time is it in Tokyo when it's 9:00 here?", "Plan a call that fits Amsterdam and New York"}, "nl": {"Hoe laat is het in Tokio als het hier 9:00 is?", "Plan een call die past in Amsterdam en New York"}}},
	{ID: "thinking", Name: "Sequential thinking", Kind: "stdio", Command: "npx", Args: []string{"-y", "@modelcontextprotocol/server-sequential-thinking"}, Category: "utilities",
		Desc: map[string]string{"en": "Lets agents think through hard problems step by step.", "nl": "Laat agents moeilijke problemen stap voor stap doordenken."},
		Docs: "https://github.com/modelcontextprotocol/servers/tree/main/src/sequentialthinking", Needs: "Node.js",
		Examples: map[string][]string{"en": {"Plan the architecture of my butler robot step by step"}, "nl": {"Plan stap voor stap de architectuur van mijn butler-robot"}}},
	{ID: "email", Name: "Email", Kind: "stdio", Command: "npx", Args: []string{"-y", "@n24q02m/better-email-mcp@latest"}, Category: "mail",
		Desc: map[string]string{"en": "Read, search, sort and answer email in Gmail, Outlook, iCloud, Yahoo or any IMAP mailbox.", "nl": "Mail lezen, zoeken, sorteren en beantwoorden in Gmail, Outlook, iCloud, Yahoo of elke IMAP-mailbox."},
		Fields: []PresetField{{Key: "EMAIL_USER", Target: "env", Label: "Email address", Placeholder: "you@gmail.com"},
			{Key: "EMAIL_APP_PASSWORD", Target: "env", Label: "App password (not your normal password)", Placeholder: "abcd efgh ijkl mnop", Secret: true}},
		Docs: "https://github.com/n24q02m/better-email-mcp", Needs: "Node.js",
		Examples: map[string][]string{"en": {"Summarize my unread mail from today and tell me what needs an answer", "Find every invoice I got this month and list amount and sender", "Draft replies to the three most urgent emails, but ask before sending"}, "nl": {"Vat mijn ongelezen mail van vandaag samen en zeg wat een antwoord nodig heeft", "Zoek alle facturen van deze maand en zet bedrag en afzender op een rij", "Schrijf conceptantwoorden op de drie dringendste mails, maar vraag voor het versturen"}}},
	{ID: "slack", Name: "Slack", Kind: "stdio", Command: "npx", Args: []string{"-y", "@modelcontextprotocol/server-slack"}, Category: "mail",
		Desc:   map[string]string{"en": "Read channels, post messages and reply in threads.", "nl": "Kanalen lezen, berichten plaatsen en in threads antwoorden."},
		Fields: []PresetField{{Key: "SLACK_BOT_TOKEN", Target: "env", Label: "Bot token", Placeholder: "xoxb-…", Secret: true}, {Key: "SLACK_TEAM_ID", Target: "env", Label: "Workspace ID", Placeholder: "T01234567"}},
		Docs:   "https://github.com/modelcontextprotocol/servers-archived/tree/main/src/slack", Needs: "Node.js",
		Examples: map[string][]string{"en": {"Summarize what happened in #general today", "Post the mission result in #updates"}, "nl": {"Vat samen wat er vandaag in #general gebeurde", "Zet het missieresultaat in #updates"}}},
	{ID: "desktop", Name: "Desktop Commander", Kind: "stdio", Command: "npx", Args: []string{"-y", "@wonderwhy-er/desktop-commander@latest"}, Category: "files",
		Desc: map[string]string{"en": "Run programs and terminal commands on your PC, search and edit files anywhere.", "nl": "Programma's en terminalcommando's op je pc draaien, overal bestanden zoeken en bewerken."},
		Docs: "https://github.com/wonderwhy-er/DesktopCommanderMCP", Needs: "Node.js",
		Examples: map[string][]string{"en": {"Check which programs use the most memory right now", "Find all .blend files on my D: drive"}, "nl": {"Kijk welke programma's nu het meeste geheugen gebruiken", "Zoek alle .blend-bestanden op mijn D:-schijf"}}},
	{ID: "netlify", Name: "Netlify", Kind: "stdio", Command: "npx", Args: []string{"-y", "@netlify/mcp"}, Category: "code",
		Desc:   map[string]string{"en": "Deploy sites, check builds and manage your Netlify projects.", "nl": "Sites deployen, builds controleren en je Netlify-projecten beheren."},
		Fields: []PresetField{{Key: "NETLIFY_PERSONAL_ACCESS_TOKEN", Target: "env", Label: "Personal access token", Placeholder: "nfp_…", Secret: true}},
		Docs:   "https://github.com/netlify/netlify-mcp", Needs: "Node.js",
		Examples: map[string][]string{"en": {"Deploy the scrap-duel folder to Netlify and give me the link", "Why did my last Netlify build fail?"}, "nl": {"Deploy de map scrap-duel naar Netlify en geef me de link", "Waarom faalde mijn laatste Netlify-build?"}}},
	{ID: "supabase", Name: "Supabase", Kind: "stdio", Command: "npx", Args: []string{"-y", "@supabase/mcp-server-supabase@latest"}, Category: "data",
		Desc:   map[string]string{"en": "Query tables, run SQL and manage your Supabase projects.", "nl": "Tabellen bevragen, SQL draaien en je Supabase-projecten beheren."},
		Fields: []PresetField{{Key: "SUPABASE_ACCESS_TOKEN", Target: "env", Label: "Access token", Placeholder: "sbp_…", Secret: true}},
		Docs:   "https://github.com/supabase-community/supabase-mcp", Needs: "Node.js",
		Examples: map[string][]string{"en": {"Show the 10 newest users and when they signed up"}, "nl": {"Laat de 10 nieuwste gebruikers zien en wanneer ze zich aanmeldden"}}},
	{ID: "postgres", Name: "PostgreSQL", Kind: "stdio", Command: "npx", Args: []string{"-y", "@modelcontextprotocol/server-postgres"}, Category: "data",
		Desc:   map[string]string{"en": "Read-only SQL access to a Postgres database.", "nl": "Alleen-lezen SQL-toegang tot een Postgres-database."},
		Fields: []PresetField{{Key: "arg", Target: "arg", Label: "Connection string", Placeholder: "postgresql://user:pass@localhost/db", Secret: true}},
		Docs:   "https://github.com/modelcontextprotocol/servers-archived/tree/main/src/postgres", Needs: "Node.js",
		Examples: map[string][]string{"en": {"Which tables are in my database and how big are they?"}, "nl": {"Welke tabellen zitten in mijn database en hoe groot zijn ze?"}}},
	{ID: "sqlite", Name: "SQLite", Kind: "stdio", Command: "uvx", Args: []string{"mcp-server-sqlite", "--db-path"}, Category: "data",
		Desc:   map[string]string{"en": "Work with a local SQLite database file.", "nl": "Werken met een lokaal SQLite-databasebestand."},
		Fields: []PresetField{{Key: "arg", Target: "arg", Label: "Database file", Placeholder: `C:\data\app.db`}},
		Docs:   "https://github.com/modelcontextprotocol/servers-archived/tree/main/src/sqlite", Needs: "Python (uv)",
		Examples: map[string][]string{"en": {"Make a chart-ready summary of the sales table"}, "nl": {"Maak een overzicht van de verkooptabel dat klaar is voor een grafiek"}}},
	{ID: "stripe", Name: "Stripe", Kind: "stdio", Command: "npx", Args: []string{"-y", "@stripe/mcp", "--tools=all"}, Category: "data",
		Desc:   map[string]string{"en": "Look up payments, customers and invoices. Use a restricted key.", "nl": "Betalingen, klanten en facturen opzoeken. Gebruik een beperkte sleutel."},
		Fields: []PresetField{{Key: "STRIPE_SECRET_KEY", Target: "env", Label: "Restricted API key", Placeholder: "rk_live_…", Secret: true}},
		Docs:   "https://github.com/stripe/agent-toolkit", Needs: "Node.js",
		Examples: map[string][]string{"en": {"How much revenue came in this week, compared to last week?"}, "nl": {"Hoeveel omzet kwam er deze week binnen, vergeleken met vorige week?"}}},
	{ID: "maps", Name: "Google Maps", Kind: "stdio", Command: "npx", Args: []string{"-y", "@modelcontextprotocol/server-google-maps"}, Category: "web",
		Desc:   map[string]string{"en": "Find places, addresses, routes and travel times.", "nl": "Plaatsen, adressen, routes en reistijden vinden."},
		Fields: []PresetField{{Key: "GOOGLE_MAPS_API_KEY", Target: "env", Label: "API key", Placeholder: "AIza…", Secret: true}},
		Docs:   "https://github.com/modelcontextprotocol/servers-archived/tree/main/src/google-maps", Needs: "Node.js",
		Examples: map[string][]string{"en": {"Find hardware stores near Avenhorn open on Saturday"}, "nl": {"Zoek bouwmarkten bij Avenhorn die zaterdag open zijn"}}},
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
