package main

// Read-only monitoring of the user's own Claude Code: agent teams, recent sessions, token usage.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Member struct {
	Name        string `json:"name"`
	AgentType   string `json:"agentType"`
	Model       string `json:"model"`
	Color       string `json:"color"`
	Status      string `json:"status"`
	CurrentTask string `json:"currentTask"`
}

type Task struct {
	ID         string    `json:"id"`
	Subject    string    `json:"subject"`
	ActiveForm string    `json:"activeForm"`
	Status     string    `json:"status"`
	Owner      string    `json:"owner"`
	BlockedBy  []string  `json:"blockedBy"`
	Updated    time.Time `json:"updated"`
}

type TeamMsg struct {
	Team string    `json:"team"`
	To   string    `json:"to"`
	From string    `json:"from"`
	Text string    `json:"text"`
	Time time.Time `json:"time"`
}

type Team struct {
	Name    string    `json:"name"`
	Cwd     string    `json:"cwd"`
	Active  bool      `json:"active"`
	Members []Member  `json:"members"`
	Tasks   []Task    `json:"tasks"`
	Updated time.Time `json:"updated"`
	Done    int       `json:"done"`
	Total   int       `json:"total"`
}

type Session struct {
	Project  string    `json:"project"`
	ID       string    `json:"id"`
	LastText string    `json:"lastText"`
	Active   bool      `json:"active"`
	Updated  time.Time `json:"updated"`
}

type External struct {
	ClaudeDir    string    `json:"claudeDir"`
	ClaudeFound  bool      `json:"claudeFound"`
	TeamsEnabled bool      `json:"teamsEnabled"`
	Teams        []Team    `json:"teams"`
	Messages     []TeamMsg `json:"messages"`
	Sessions     []Session `json:"sessions"`
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func str(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			switch t := v.(type) {
			case string:
				return t
			case float64:
				return strconv.FormatFloat(t, 'f', -1, 64)
			case bool:
				return strconv.FormatBool(t)
			}
		}
	}
	return ""
}

func strList(m map[string]any, key string) []string {
	var out []string
	if arr, ok := m[key].([]any); ok {
		for _, x := range arr {
			out = append(out, fmt.Sprint(x))
		}
	}
	return out
}

func parseTime(v any) time.Time {
	switch t := v.(type) {
	case string:
		for _, f := range []string{time.RFC3339Nano, time.RFC3339} {
			if tt, err := time.Parse(f, t); err == nil {
				return tt
			}
		}
	case float64:
		if t > 1e12 {
			return time.UnixMilli(int64(t))
		}
		return time.Unix(int64(t), 0)
	}
	return time.Time{}
}

func truncate(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

func cleanMsg(text string) string {
	t := strings.TrimSpace(text)
	if strings.HasPrefix(t, "{") {
		var m map[string]any
		if json.Unmarshal([]byte(t), &m) == nil {
			typ := strings.ReplaceAll(str(m, "type"), "_", " ")
			body := str(m, "summary", "content", "message", "text", "subject", "reason")
			switch {
			case typ != "" && body != "":
				return "[" + typ + "] " + body
			case typ != "":
				return "[" + typ + "]"
			case body != "":
				return body
			}
		}
	}
	return t
}

func claudeDir() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".claude")
}

func scanTasks(dir string) []Task {
	var tasks []Task
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		var m map[string]any
		if readJSON(filepath.Join(dir, e.Name()), &m) != nil {
			continue
		}
		subj, status := str(m, "subject", "title", "content"), strings.ToLower(str(m, "status"))
		if subj == "" && status == "" {
			continue
		}
		t := Task{ID: str(m, "id"), Subject: subj, ActiveForm: str(m, "activeForm"), Status: status,
			Owner: str(m, "owner", "assignee"), BlockedBy: strList(m, "blockedBy")}
		if t.ID == "" {
			t.ID = strings.TrimSuffix(e.Name(), ".json")
		}
		if t.Status == "" {
			t.Status = "pending"
		}
		if info, err := e.Info(); err == nil {
			t.Updated = info.ModTime()
		}
		tasks = append(tasks, t)
	}
	sort.Slice(tasks, func(i, j int) bool {
		ai, ei := strconv.Atoi(tasks[i].ID)
		aj, ej := strconv.Atoi(tasks[j].ID)
		if ei == nil && ej == nil {
			return ai < aj
		}
		return tasks[i].ID < tasks[j].ID
	})
	return tasks
}

func scanInboxes(team, dir string) []TeamMsg {
	var msgs []TeamMsg
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		var arr []map[string]any
		_ = readJSON(filepath.Join(dir, e.Name()), &arr)
		for _, m := range arr {
			msgs = append(msgs, TeamMsg{Team: team, To: strings.TrimSuffix(e.Name(), ".json"),
				From: str(m, "from", "sender"), Text: cleanMsg(str(m, "text", "content", "message")), Time: parseTime(m["timestamp"])})
		}
	}
	return msgs
}

func sessionTail(path string) (project, last string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	st, _ := f.Stat()
	if off := st.Size() - 96*1024; off > 0 {
		_, _ = f.Seek(off, io.SeekStart)
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		var m map[string]any
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		if c := str(m, "cwd"); c != "" {
			project = filepath.Base(strings.ReplaceAll(c, "\\", "/"))
		}
		if str(m, "type") != "assistant" {
			continue
		}
		msg, _ := m["message"].(map[string]any)
		if msg == nil {
			continue
		}
		if parts, ok := msg["content"].([]any); ok {
			for _, part := range parts {
				p, _ := part.(map[string]any)
				switch str(p, "type") {
				case "text":
					if t := str(p, "text"); strings.TrimSpace(t) != "" {
						last = t
					}
				case "tool_use":
					last = "🔧 " + str(p, "name")
				}
			}
		}
	}
	return project, truncate(last, 140)
}

func scanSessions(base string) []Session {
	var out []Session
	projDirs, _ := os.ReadDir(filepath.Join(base, "projects"))
	cutoff := time.Now().Add(-48 * time.Hour)
	for _, pd := range projDirs {
		if !pd.IsDir() {
			continue
		}
		files, _ := os.ReadDir(filepath.Join(base, "projects", pd.Name()))
		for _, f := range files {
			if !strings.HasSuffix(f.Name(), ".jsonl") {
				continue
			}
			info, err := f.Info()
			if err != nil || info.ModTime().Before(cutoff) {
				continue
			}
			s := Session{ID: strings.TrimSuffix(f.Name(), ".jsonl"), Updated: info.ModTime()}
			s.Project, s.LastText = sessionTail(filepath.Join(base, "projects", pd.Name(), f.Name()))
			if s.Project == "" {
				parts := strings.Split(pd.Name(), "-")
				s.Project = parts[len(parts)-1]
			}
			s.Active = time.Since(info.ModTime()) < 2*time.Minute
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	if len(out) > 10 {
		out = out[:10]
	}
	return out
}

func teamsEnabled(base string) bool {
	if os.Getenv("CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS") == "1" {
		return true
	}
	var m map[string]any
	if readJSON(filepath.Join(base, "settings.json"), &m) != nil {
		return false
	}
	env, _ := m["env"].(map[string]any)
	return env != nil && fmt.Sprint(env["CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS"]) == "1"
}

func scanExternal(base string) External {
	ex := External{ClaudeDir: base, TeamsEnabled: teamsEnabled(base)}
	if fi, err := os.Stat(base); err == nil && fi.IsDir() {
		ex.ClaudeFound = true
	}
	teams := map[string]*Team{}
	tdirs, _ := os.ReadDir(filepath.Join(base, "teams"))
	for _, d := range tdirs {
		if !d.IsDir() {
			continue
		}
		t := &Team{Name: d.Name(), Active: true}
		var cfg map[string]any
		if readJSON(filepath.Join(base, "teams", d.Name(), "config.json"), &cfg) == nil {
			if ms, ok := cfg["members"].([]any); ok {
				for _, x := range ms {
					mm, _ := x.(map[string]any)
					if mm == nil {
						continue
					}
					t.Members = append(t.Members, Member{Name: str(mm, "name"), AgentType: str(mm, "agentType"), Model: str(mm, "model"), Color: str(mm, "color")})
					if t.Cwd == "" {
						t.Cwd = str(mm, "cwd")
					}
				}
			}
		}
		if info, err := d.Info(); err == nil {
			t.Updated = info.ModTime()
		}
		ex.Messages = append(ex.Messages, scanInboxes(d.Name(), filepath.Join(base, "teams", d.Name(), "inboxes"))...)
		teams[d.Name()] = t
	}
	kdirs, _ := os.ReadDir(filepath.Join(base, "tasks"))
	for _, d := range kdirs {
		if !d.IsDir() {
			continue
		}
		info, _ := d.Info()
		t, ok := teams[d.Name()]
		if !ok {
			if info == nil || time.Since(info.ModTime()) > 72*time.Hour {
				continue
			}
			t = &Team{Name: d.Name(), Updated: info.ModTime()}
		}
		t.Tasks = scanTasks(filepath.Join(base, "tasks", d.Name()))
		if len(t.Tasks) == 0 && !t.Active {
			continue
		}
		teams[d.Name()] = t
	}
	for _, t := range teams {
		t.Total = len(t.Tasks)
		for _, tk := range t.Tasks {
			if tk.Status == "completed" {
				t.Done++
			}
		}
		for i := range t.Members {
			m := &t.Members[i]
			m.Status = "idle"
			if m.AgentType == "team-lead" || m.Name == "team-lead" {
				m.Status = "lead"
			}
			for _, tk := range t.Tasks {
				if tk.Owner == m.Name && tk.Status == "in_progress" {
					m.Status, m.CurrentTask = "busy", tk.Subject
					if tk.ActiveForm != "" {
						m.CurrentTask = tk.ActiveForm
					}
				}
			}
		}
		ex.Teams = append(ex.Teams, *t)
	}
	sort.Slice(ex.Teams, func(i, j int) bool {
		if ex.Teams[i].Active != ex.Teams[j].Active {
			return ex.Teams[i].Active
		}
		return ex.Teams[i].Updated.After(ex.Teams[j].Updated)
	})
	sort.Slice(ex.Messages, func(i, j int) bool { return ex.Messages[i].Time.After(ex.Messages[j].Time) })
	if len(ex.Messages) > 30 {
		ex.Messages = ex.Messages[:30]
	}
	ex.Sessions = scanSessions(base)
	return ex
}

// ---------- token usage (5-hour blocks, like the Claude plan limits) ----------

type usageEntry struct {
	t      time.Time
	tokens int64
}

type fileCursor struct{ off int64 }

type UsageTracker struct {
	mu      sync.Mutex
	files   map[string]*fileCursor
	seen    map[string]bool
	entries []usageEntry
	ready   bool
}

type Usage struct {
	Ready       bool      `json:"ready"`
	BlockTokens int64     `json:"blockTokens"`
	BlockActive bool      `json:"blockActive"`
	BlockStart  time.Time `json:"blockStart"`
	ResetAt     time.Time `json:"resetAt"`
	WeekTokens  int64     `json:"weekTokens"`
	TodayTokens int64     `json:"todayTokens"`
	Budget      int64     `json:"budget"`
	BudgetAuto  bool      `json:"budgetAuto"`
	Percent     int       `json:"percent"` // -1 unknown
	BurnPerMin  int64     `json:"burnPerMin"`
	History     []int64   `json:"history"` // tokens per hour, last 24h
}

func newUsageTracker() *UsageTracker {
	return &UsageTracker{files: map[string]*fileCursor{}, seen: map[string]bool{}}
}

type usageLine struct {
	Timestamp string `json:"timestamp"`
	RequestID string `json:"requestId"`
	Message   struct {
		ID    string `json:"id"`
		Usage *struct {
			In          int64 `json:"input_tokens"`
			Out         int64 `json:"output_tokens"`
			CacheCreate int64 `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

func (u *UsageTracker) Update(base string) {
	cutoff := time.Now().Add(-8 * 24 * time.Hour)
	root := filepath.Join(base, "projects")
	var newEntries []usageEntry
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".jsonl") {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.ModTime().Before(cutoff) {
			return nil
		}
		u.mu.Lock()
		cur := u.files[p]
		if cur == nil {
			cur = &fileCursor{}
			u.files[p] = cur
		}
		off := cur.off
		u.mu.Unlock()
		if info.Size() < off {
			off = 0
		}
		if info.Size() == off {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return nil
		}
		defer f.Close()
		_, _ = f.Seek(off, io.SeekStart)
		r := bufio.NewReaderSize(f, 256*1024)
		pos := off
		for {
			line, err := r.ReadBytes('\n')
			if err != nil { // incomplete last line: retry next time
				break
			}
			pos += int64(len(line))
			if !bytes.Contains(line, []byte(`"usage"`)) {
				continue
			}
			var ul usageLine
			if json.Unmarshal(line, &ul) != nil || ul.Message.Usage == nil {
				continue
			}
			key := ul.Message.ID + ":" + ul.RequestID
			u.mu.Lock()
			dup := ul.Message.ID != "" && u.seen[key]
			if !dup {
				u.seen[key] = true
			}
			u.mu.Unlock()
			if dup {
				continue
			}
			t := parseTime(ul.Timestamp)
			if t.IsZero() || t.Before(cutoff) {
				continue
			}
			us := ul.Message.Usage
			newEntries = append(newEntries, usageEntry{t: t, tokens: us.In + us.Out + us.CacheCreate})
		}
		u.mu.Lock()
		cur.off = pos
		u.mu.Unlock()
		return nil
	})
	u.mu.Lock()
	u.entries = append(u.entries, newEntries...)
	keep := u.entries[:0]
	for _, e := range u.entries {
		if e.t.After(cutoff) {
			keep = append(keep, e)
		}
	}
	u.entries = keep
	u.ready = true
	u.mu.Unlock()
}

func (u *UsageTracker) Summary(budget int64) Usage {
	u.mu.Lock()
	es := append([]usageEntry(nil), u.entries...)
	ready := u.ready
	u.mu.Unlock()
	sort.Slice(es, func(i, j int) bool { return es[i].t.Before(es[j].t) })
	now := time.Now()
	res := Usage{Ready: ready, Percent: -1, History: make([]int64, 24)}
	var blockStart time.Time
	var blockTok, maxPrev int64
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	for _, e := range es {
		if blockStart.IsZero() || !e.t.Before(blockStart.Add(5*time.Hour)) {
			if blockTok > maxPrev {
				maxPrev = blockTok
			}
			blockStart = e.t.UTC().Truncate(time.Hour)
			blockTok = 0
		}
		blockTok += e.tokens
		if e.t.After(now.Add(-7 * 24 * time.Hour)) {
			res.WeekTokens += e.tokens
		}
		if e.t.After(dayStart) {
			res.TodayTokens += e.tokens
		}
		if h := int(now.Sub(e.t).Hours()); h >= 0 && h < 24 {
			res.History[23-h] += e.tokens
		}
	}
	if !blockStart.IsZero() && now.Before(blockStart.Add(5*time.Hour)) {
		res.BlockActive = true
		res.BlockTokens = blockTok
		res.BlockStart = blockStart
		res.ResetAt = blockStart.Add(5 * time.Hour)
		if mins := now.Sub(blockStart).Minutes(); mins > 1 {
			res.BurnPerMin = int64(float64(blockTok) / mins)
		}
	} else if blockTok > maxPrev {
		maxPrev = blockTok
	}
	res.Budget = budget
	if budget <= 0 {
		res.BudgetAuto = true
		res.Budget = maxPrev
	}
	if res.Budget > 0 {
		res.Percent = int(res.BlockTokens * 100 / res.Budget)
	}
	return res
}
