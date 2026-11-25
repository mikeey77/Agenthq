package main

// Projects group missions around one folder (and optionally a GitHub repo) with a task board.
// Agents working inside a project see its tasks and keep the board up to date.

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

type PTask struct {
	ID      string    `json:"id"`
	Title   string    `json:"title"`
	Notes   string    `json:"notes,omitempty"`
	Status  string    `json:"status"` // todo | doing | review | done
	AgentID string    `json:"agentId,omitempty"`
	By      string    `json:"by,omitempty"` // who created it: "you" or an agent name
	Updated time.Time `json:"updated"`
}

type Project struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Desc        string    `json:"desc"`
	Folder      string    `json:"folder"`
	Repo        string    `json:"repo,omitempty"`
	Color       string    `json:"color"`
	Created     time.Time `json:"created"`
	Tasks       []*PTask  `json:"tasks"`
	Memory      []*Note   `json:"memory"`
	NetlifySite string    `json:"netlifySite,omitempty"`
	SiteURL     string    `json:"siteUrl,omitempty"`
	Deploys     []Deploy  `json:"deploys,omitempty"`
	Deploying   bool      `json:"deploying,omitempty"`
}

var projectColors = []string{"#ff8a4c", "#6aa7ff", "#3ecf8e", "#c792ea", "#f5c451", "#5fd7d7", "#ff8fa3", "#a3d977"}

var taskStates = map[string]bool{"todo": true, "doing": true, "review": true, "done": true}

func (a *App) project(ref string) *Project {
	ref = strings.ToLower(strings.TrimSpace(ref))
	for _, p := range a.projects {
		if p.ID == ref || strings.ToLower(p.Name) == ref || slug(p.Name) == ref {
			return p
		}
	}
	return nil
}

func (p *Project) task(id string) *PTask {
	for _, t := range p.Tasks {
		if t.ID == id || strings.EqualFold(t.Title, id) {
			return t
		}
	}
	return nil
}

// newProject creates a project. Caller holds a.mu.
func (a *App) newProject(name, desc, folder string) (*Project, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("name is empty")
	}
	if a.project(name) != nil {
		return nil, fmt.Errorf("project %q already exists", name)
	}
	if strings.TrimSpace(folder) == "" {
		folder = filepath.Join(a.cfg.Workspace, "projects", slug(name))
	}
	if err := os.MkdirAll(folder, 0o755); err != nil {
		return nil, err
	}
	p := &Project{ID: newID(), Name: name, Desc: desc, Folder: folder, Created: time.Now(),
		Color: projectColors[len(a.projects)%len(projectColors)], Tasks: []*PTask{}}
	a.projects = append(a.projects, p)
	a.dirty = true
	return p, nil
}

func (p *Project) addTask(title, notes, by string) *PTask {
	t := &PTask{ID: newID()[:6], Title: strings.TrimSpace(title), Notes: notes, Status: "todo", By: by, Updated: time.Now()}
	p.Tasks = append(p.Tasks, t)
	return t
}

func (p *Project) progress() (done, total int) {
	for _, t := range p.Tasks {
		total++
		if t.Status == "done" {
			done++
		}
	}
	return
}

// taskBoard is what agents see of their project.
func (p *Project) taskBoard() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Project %q — folder %s", p.Name, p.Folder)
	if p.Repo != "" {
		fmt.Fprintf(&b, " — repo %s", p.Repo)
	}
	if p.Desc != "" {
		fmt.Fprintf(&b, "\n%s", p.Desc)
	}
	b.WriteString("\nTasks:")
	if len(p.Tasks) == 0 {
		b.WriteString(" (none yet)")
	}
	for _, t := range p.Tasks {
		fmt.Fprintf(&b, "\n- [%s] %s (id %s)", t.Status, t.Title, t.ID)
	}
	return b.String()
}

// startTask launches a mission for a task. Caller holds a.mu.
func (a *App) startTask(p *Project, t *PTask, model string) (*Agent, error) {
	m := fmt.Sprintf("Project %q, task %q (id %s).", p.Name, t.Title, t.ID)
	if t.Notes != "" {
		m += "\n\n" + t.Notes
	}
	a.nextProject, a.nextTask = p.ID, t.ID
	ag, err := a.createAgent("", "Mission lead", m, nil, p.Folder, model, nil)
	a.nextProject, a.nextTask = "", ""
	if err != nil {
		return nil, err
	}
	t.Status, t.AgentID, t.Updated = "doing", ag.ID, time.Now()
	a.dirty = true
	return ag, nil
}

// taskFinished moves the task of a finished mission lead. Caller holds a.mu.
func (a *App) taskFinished(ag *Agent, ok bool) {
	p := a.projectByID(ag.ProjectID)
	if p == nil || ag.TaskID == "" {
		return
	}
	if t := p.task(ag.TaskID); t != nil && t.Status != "done" {
		if ok {
			t.Status = "review"
		}
		t.Updated = time.Now()
		a.dirty = true
	}
}

func (a *App) projectByID(id string) *Project {
	for _, p := range a.projects {
		if p.ID == id {
			return p
		}
	}
	return nil
}

// ---------- tools for agents ----------

var projectTools = []map[string]any{
	{"name": "project_tasks", "description": "Show the task board of your project.", "inputSchema": schema(map[string]any{})},
	{"name": "add_task", "description": "Add a task to your project's board (for follow-up work you or the user should do).",
		"inputSchema": schema(map[string]any{"title": sProp("Short task title"), "notes": sProp("Details")}, "title")},
	{"name": "update_task", "description": "Move a task on your project's board. status: todo, doing, review (finished, user should check) or done.",
		"inputSchema": schema(map[string]any{"id": sProp("PTask id or title"), "status": map[string]any{"type": "string", "enum": []string{"todo", "doing", "review", "done"}}, "notes": sProp("Optional note to add")}, "id", "status")},
	{"name": "create_project", "description": "Create a new project (a folder with a task board) and move your work into it. Use it when the user asks for a new project or app.",
		"inputSchema": schema(map[string]any{"name": sProp("Project name"), "description": sProp("One sentence")}, "name")},
}

// projectTool runs a project tool. Caller holds a.mu.
func (a *App) projectTool(ag *Agent, name string, args map[string]any) (string, bool) {
	if name == "create_project" {
		p, err := a.newProject(str(args, "name"), str(args, "description"), "")
		if err != nil {
			return err.Error(), true
		}
		root := ag
		for root.ParentID != "" && a.agents[root.ParentID] != nil {
			root = a.agents[root.ParentID]
		}
		root.ProjectID, ag.ProjectID = p.ID, p.ID
		a.emit("update", ag.ID, a.T("project_new", ag.Name, p.Name))
		return fmt.Sprintf("Created project %q in %s. Put the project files there.", p.Name, p.Folder), false
	}
	p := a.projectByID(ag.ProjectID)
	if p == nil {
		return "You are not working in a project. Use create_project first if the user wants one.", true
	}
	switch name {
	case "project_tasks":
		return p.taskBoard(), false
	case "add_task":
		t := p.addTask(str(args, "title"), str(args, "notes"), ag.Name)
		a.dirty = true
		return "Added task " + t.ID + ".", false
	case "update_task":
		t := p.task(str(args, "id"))
		st := str(args, "status")
		if t == nil || !taskStates[st] {
			return "Unknown task or status. Use project_tasks.", true
		}
		t.Status, t.Updated = st, time.Now()
		if n := str(args, "notes"); n != "" {
			t.Notes = strings.TrimSpace(t.Notes + "\n" + n)
		}
		a.dirty = true
		return "PTask moved to " + st + ".", false
	}
	return "unknown tool", true
}

// ---------- files ----------

// noteFile remembers files an agent created or changed (newest first).
func (ag *Agent) noteFile(path string) {
	if path == "" {
		return
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(ag.Cwd, path)
	}
	path = filepath.Clean(path)
	out := []string{path}
	for _, f := range ag.Files {
		if f != path && len(out) < 40 {
			out = append(out, f)
		}
	}
	ag.Files = out
}

// reveal opens a folder, or shows a file selected, in Explorer / Finder / the file manager.
func reveal(path string) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	switch runtime.GOOS {
	case "windows":
		if st.IsDir() {
			return exec.Command("explorer.exe", path).Start()
		}
		return exec.Command("explorer.exe", "/select,", path).Start()
	case "darwin":
		if st.IsDir() {
			return exec.Command("open", path).Start()
		}
		return exec.Command("open", "-R", path).Start()
	default:
		if !st.IsDir() {
			path = filepath.Dir(path)
		}
		return exec.Command("xdg-open", path).Start()
	}
}

type fileEntry struct {
	Name  string    `json:"name"`
	Path  string    `json:"path"`
	Dir   bool      `json:"dir"`
	Size  int64     `json:"size"`
	MTime time.Time `json:"mtime"`
}

// listDir lists a folder for the file map, folders first, newest files first.
func listDir(dir string) ([]fileEntry, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := []fileEntry{}
	for _, e := range ents {
		n := e.Name()
		if n == ".git" || n == "node_modules" || strings.HasPrefix(n, ".agenthq") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, fileEntry{n, filepath.Join(dir, n), e.IsDir(), info.Size(), info.ModTime()})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Dir != out[j].Dir {
			return out[i].Dir
		}
		return out[i].MTime.After(out[j].MTime)
	})
	if len(out) > 300 {
		out = out[:300]
	}
	return out, nil
}

func (a *App) projectRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/project", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ ID, Action, Name, Desc, Folder, Repo string }
		_ = readBody(r, &in)
		a.mu.Lock()
		defer a.mu.Unlock()
		p := a.projectByID(in.ID)
		var err error
		switch in.Action {
		case "create":
			p, err = a.newProject(in.Name, in.Desc, in.Folder)
		case "save":
			if p == nil {
				err = errors.New("no such project")
				break
			}
			if n := strings.TrimSpace(in.Name); n != "" {
				p.Name = n
			}
			p.Desc, p.Repo = in.Desc, strings.TrimSpace(in.Repo)
			if f := strings.TrimSpace(in.Folder); f != "" {
				p.Folder = f
				_ = os.MkdirAll(f, 0o755)
			}
		case "delete": // removes the board only, never the folder
			for i, x := range a.projects {
				if x.ID == in.ID {
					a.projects = append(a.projects[:i], a.projects[i+1:]...)
					break
				}
			}
		}
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		a.dirty = true
		if p != nil {
			writeJSON(w, p)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("/api/task", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Project, ID, Action, Title, Notes, Status, Model string
			Index                                            int
		}
		_ = readBody(r, &in)
		a.mu.Lock()
		defer a.mu.Unlock()
		p := a.projectByID(in.Project)
		if p == nil {
			http.Error(w, "no such project", 404)
			return
		}
		t := p.task(in.ID)
		switch in.Action {
		case "add":
			if strings.TrimSpace(in.Title) == "" {
				http.Error(w, "title is empty", 400)
				return
			}
			t = p.addTask(in.Title, in.Notes, "you")
			if taskStates[in.Status] {
				t.Status = in.Status
			}
		case "save", "move":
			if t == nil {
				http.Error(w, "no such task", 404)
				return
			}
			if in.Action == "save" {
				t.Title, t.Notes = strings.TrimSpace(in.Title), in.Notes
			}
			if taskStates[in.Status] {
				t.Status = in.Status
			}
			t.Updated = time.Now()
			if in.Action == "move" { // reorder: put the task at Index within the list
				for i, x := range p.Tasks {
					if x == t {
						p.Tasks = append(p.Tasks[:i], p.Tasks[i+1:]...)
						break
					}
				}
				idx := in.Index
				if idx < 0 || idx > len(p.Tasks) {
					idx = len(p.Tasks)
				}
				p.Tasks = append(p.Tasks[:idx], append([]*PTask{t}, p.Tasks[idx:]...)...)
			}
		case "delete":
			for i, x := range p.Tasks {
				if x == t {
					p.Tasks = append(p.Tasks[:i], p.Tasks[i+1:]...)
					break
				}
			}
		case "start":
			if t == nil {
				http.Error(w, "no such task", 404)
				return
			}
			ag, err := a.startTask(p, t, in.Model)
			if err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			a.termAdd("out", a.T("term_mission", ag.Name))
		}
		a.dirty = true
		writeJSON(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("/api/files", func(w http.ResponseWriter, r *http.Request) {
		dir := r.URL.Query().Get("dir")
		list, err := listDir(dir)
		if err != nil && a.demo {
			list, err = demoFiles(dir), nil
		}
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		writeJSON(w, map[string]any{"dir": dir, "parent": filepath.Dir(dir), "entries": list})
	})
	mux.HandleFunc("/api/reveal", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Path string }
		_ = readBody(r, &in)
		if a.demo {
			writeJSON(w, map[string]bool{"ok": true})
			return
		}
		if err := reveal(in.Path); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	})
}

type projectView struct {
	*Project
	Done   int  `json:"done"`
	Total  int  `json:"total"`
	Agents int  `json:"agents"` // live agents working in it
	GitHub bool `json:"github"`
}

// projectViews is the projects part of /api/state. Caller holds a.mu.
func (a *App) projectViews() []projectView {
	out := []projectView{}
	for _, p := range a.projects {
		d, t := p.progress()
		n := 0
		for _, ag := range a.agents {
			if ag.ProjectID == p.ID && isLive(ag.Status) {
				n++
			}
		}
		out = append(out, projectView{p, d, t, n, a.githubToken() != ""})
	}
	return out
}

func demoFiles(dir string) []fileEntry {
	now := time.Now()
	f := func(n string, d bool, size int64, mins int) fileEntry {
		return fileEntry{n, dir + `\` + n, d, size, now.Add(-time.Duration(mins) * time.Minute)}
	}
	return []fileEntry{f("src", true, 0, 4), f("test", true, 0, 9), f("docs", true, 0, 26), f("public", true, 0, 300),
		f("package.json", false, 1240, 12), f("README.md", false, 3820, 38), f("vite.config.ts", false, 610, 300), f("tsconfig.json", false, 540, 3000), f("receipts.csv", false, 88200, 3100)}
}
