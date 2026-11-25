package main

// The library: memory (per project and general) and skills (reusable ways of working).
// Memory notes are added to every agent's instructions, so agents stop repeating mistakes.
// Skills are saved by agents (or you) and listed for every agent, which loads one with use_skill.

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

type Note struct {
	ID      string    `json:"id"`
	Text    string    `json:"text"`
	Kind    string    `json:"kind"` // decision | preference | lesson | fact
	By      string    `json:"by"`
	Created time.Time `json:"created"`
}

type Skill struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Desc    string    `json:"desc"`
	Steps   string    `json:"steps"`
	By      string    `json:"by"`
	Uses    int       `json:"uses"`
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
}

const maxNotes = 80

var noteKinds = map[string]bool{"decision": true, "preference": true, "lesson": true, "fact": true}

// notesFor returns the memory list an agent works with: its project's, or the general one.
func (a *App) notesFor(projectID string) *[]*Note {
	if p := a.projectByID(projectID); p != nil {
		return &p.Memory
	}
	return &a.memory
}

func addNote(list *[]*Note, text, kind, by string) (*Note, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, errors.New("note is empty")
	}
	for _, n := range *list { // no duplicates
		if strings.EqualFold(n.Text, text) {
			return n, nil
		}
	}
	if !noteKinds[kind] {
		kind = "fact"
	}
	n := &Note{ID: newID()[:6], Text: truncate(text, 600), Kind: kind, By: by, Created: time.Now()}
	*list = append(*list, n)
	if len(*list) > maxNotes {
		*list = (*list)[len(*list)-maxNotes:]
	}
	return n, nil
}

func removeNote(list *[]*Note, id string) bool {
	for i, n := range *list {
		if n.ID == id {
			*list = append((*list)[:i], (*list)[i+1:]...)
			return true
		}
	}
	return false
}

// memoryText is the memory part of an agent's instructions. Caller holds a.mu.
func (a *App) memoryText(ag *Agent) string {
	var b strings.Builder
	write := func(title string, notes []*Note) {
		if len(notes) == 0 {
			return
		}
		b.WriteString("\n" + title + "\n")
		for _, n := range notes {
			fmt.Fprintf(&b, "- (%s) %s\n", n.Kind, n.Text)
		}
	}
	write("General memory (things the user wants every agent to know):", a.memory)
	if p := a.projectByID(ag.ProjectID); p != nil {
		write(fmt.Sprintf("Project memory for %q (decisions, preferences and lessons from earlier missions — follow them):", p.Name), p.Memory)
	}
	if len(a.skills) > 0 {
		b.WriteString("\nSkills library (proven ways of working; call use_skill(name) before starting a task that matches one):\n")
		for _, s := range a.skills {
			fmt.Fprintf(&b, "- %s: %s\n", s.Name, s.Desc)
		}
	}
	b.WriteString(`
Memory and skills tools (mcp__hq__*):
- remember(text, kind): save something the next agents must know: a decision (we use Vitest), a user preference (Dutch UI texts), a lesson (npm install needs --legacy-peer-deps here) or a fact. Save only what stays true; one sentence each.
- forget(id): remove a note that is no longer true.
- save_skill(name, description, steps): after you solved something in a way worth repeating (deploying, setting up a project type, a tricky fix), save the steps so other agents can reuse them. Steps are concrete markdown instructions.
- use_skill(name): load the steps of a skill.`)
	return b.String()
}

func (a *App) skillByName(name string) *Skill {
	k := slug(name)
	for _, s := range a.skills {
		if s.ID == name || slug(s.Name) == k {
			return s
		}
	}
	return nil
}

var libraryTools = []map[string]any{
	{"name": "remember", "description": "Save a note to memory (your project's memory, or the general memory when you are not in a project). Future agents see it in their instructions.",
		"inputSchema": schema(map[string]any{"text": sProp("One clear sentence"), "kind": map[string]any{"type": "string", "enum": []string{"decision", "preference", "lesson", "fact"}}}, "text")},
	{"name": "forget", "description": "Remove a memory note that is no longer true.", "inputSchema": schema(map[string]any{"id": sProp("Note id")}, "id")},
	{"name": "save_skill", "description": "Save or update a reusable skill: a proven, step-by-step way to do a kind of task.",
		"inputSchema": schema(map[string]any{"name": sProp("Short name, e.g. Deploy a Vite site to Netlify"), "description": sProp("When to use it, one sentence"), "steps": sProp("Markdown steps, concrete commands and gotchas")}, "name", "description", "steps")},
	{"name": "use_skill", "description": "Load the steps of a skill from the library.", "inputSchema": schema(map[string]any{"name": sProp("Skill name")}, "name")},
}

// libraryTool runs a memory/skill tool. Caller holds a.mu.
func (a *App) libraryTool(ag *Agent, name string, args map[string]any) (string, bool) {
	switch name {
	case "remember":
		n, err := addNote(a.notesFor(ag.ProjectID), str(args, "text"), str(args, "kind"), ag.Name)
		if err != nil {
			return err.Error(), true
		}
		a.dirty = true
		a.addLog(ag, "info", "remembered: "+n.Text)
		return "Saved to memory (id " + n.ID + ").", false
	case "forget":
		if removeNote(a.notesFor(ag.ProjectID), str(args, "id")) || removeNote(&a.memory, str(args, "id")) {
			a.dirty = true
			return "Removed.", false
		}
		return "No note with that id.", true
	case "save_skill":
		nm := strings.TrimSpace(str(args, "name"))
		if nm == "" || strings.TrimSpace(str(args, "steps")) == "" {
			return "name and steps are required", true
		}
		s := a.skillByName(nm)
		if s == nil {
			s = &Skill{ID: newID()[:8], Name: nm, By: ag.Name, Created: time.Now()}
			a.skills = append(a.skills, s)
			a.emit("update", ag.ID, a.T("skill_new", ag.Name, nm))
		}
		s.Desc, s.Steps, s.Updated = str(args, "description"), str(args, "steps"), time.Now()
		a.dirty = true
		return "Skill saved: " + nm, false
	case "use_skill":
		s := a.skillByName(str(args, "name"))
		if s == nil {
			names := []string{}
			for _, x := range a.skills {
				names = append(names, x.Name)
			}
			return "No skill with that name. Available: " + strings.Join(names, "; "), true
		}
		s.Uses++
		a.dirty = true
		a.addLog(ag, "info", "uses skill: "+s.Name)
		return "# " + s.Name + "\n" + s.Desc + "\n\n" + s.Steps, false
	}
	return "unknown tool", true
}

func (a *App) libraryRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/memory", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Project, Action, ID, Text, Kind string }
		_ = readBody(r, &in)
		a.mu.Lock()
		defer a.mu.Unlock()
		list := a.notesFor(in.Project)
		switch in.Action {
		case "add":
			if _, err := addNote(list, in.Text, in.Kind, "you"); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
		case "save":
			for _, n := range *list {
				if n.ID == in.ID {
					n.Text = strings.TrimSpace(in.Text)
					if noteKinds[in.Kind] {
						n.Kind = in.Kind
					}
				}
			}
		case "delete":
			removeNote(list, in.ID)
		}
		a.dirty = true
		writeJSON(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("/api/skill", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Action, ID, Name, Desc, Steps string }
		_ = readBody(r, &in)
		a.mu.Lock()
		defer a.mu.Unlock()
		var s *Skill
		for _, x := range a.skills {
			if x.ID == in.ID {
				s = x
			}
		}
		switch in.Action {
		case "save":
			if strings.TrimSpace(in.Name) == "" || strings.TrimSpace(in.Steps) == "" {
				http.Error(w, "name and steps are required", 400)
				return
			}
			if s == nil {
				s = &Skill{ID: newID()[:8], By: "you", Created: time.Now()}
				a.skills = append(a.skills, s)
			}
			s.Name, s.Desc, s.Steps, s.Updated = strings.TrimSpace(in.Name), in.Desc, in.Steps, time.Now()
		case "delete":
			for i, x := range a.skills {
				if x == s {
					a.skills = append(a.skills[:i], a.skills[i+1:]...)
					break
				}
			}
		}
		a.dirty = true
		writeJSON(w, map[string]bool{"ok": true})
	})
}

func (a *App) skillViews() []*Skill {
	out := append([]*Skill{}, a.skills...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Uses > out[j].Uses })
	return out
}

// ---------- review agent ----------

// startReview puts a reviewer on a finished mission that changed files. Caller holds a.mu.
func (a *App) startReview(lead *Agent) bool {
	mode := a.cfg.ReviewMode
	if mode == "off" || lead.ReviewOf != "" || lead.ParentID != "" {
		return false
	}
	if mode == "projects" && lead.ProjectID == "" {
		return false
	}
	files := []string{}
	for _, x := range a.agents {
		r := x
		for r.ParentID != "" && a.agents[r.ParentID] != nil {
			r = a.agents[r.ParentID]
		}
		if r == lead {
			files = append(files, x.Files...)
		}
	}
	if len(files) == 0 { // nothing to check
		return false
	}
	if len(files) > 25 {
		files = files[:25]
	}
	m := fmt.Sprintf(`Review the work of agent %s before the user sees it.

Mission was: %s

What %s reported: %s

Files that changed:
- %s

Check that the work does what the mission asked and actually works: read the changed files, run the tests or build if the project has them, try the main flow. Fix small problems yourself (typos, a failing test, a missing import). Do not rewrite the work or add features.
Save lessons for next time with remember (e.g. a check that was forgotten).
Finish with complete(summary) where the summary starts with "✅ Approved" or "⚠️ Needs work", followed by what you checked, what you fixed and what the user should look at.`,
		lead.Name, lead.Mission, lead.Name, truncate(lead.Summary, 1500), strings.Join(files, "\n- "))
	a.nextProject, a.nextTask = lead.ProjectID, lead.TaskID
	rv, err := a.createAgent("", "Reviewer", m, nil, lead.Cwd, lead.Model, lead.Connectors)
	a.nextProject, a.nextTask = "", ""
	if err != nil {
		return false
	}
	rv.ReviewOf = lead.ID
	rv.Kind, rv.Color = "review", "#5fd7d7"
	a.emit("update", rv.ID, a.T("review_started", rv.Name, lead.Name))
	return true
}

// ---------- spending ledger ----------

type Spend struct {
	Cost   float64 `json:"cost"`
	Tokens int64   `json:"tokens"`
	Turns  int     `json:"turns"`
}

// spend records one finished turn under today's date and the agent's project. Caller holds a.mu.
func (a *App) spend(ag *Agent, cost float64, tokens int64) {
	day := time.Now().Format("2006-01-02")
	if a.ledger == nil {
		a.ledger = map[string]map[string]*Spend{}
	}
	if a.ledger[day] == nil {
		a.ledger[day] = map[string]*Spend{}
	}
	s := a.ledger[day][ag.ProjectID]
	if s == nil {
		s = &Spend{}
		a.ledger[day][ag.ProjectID] = s
	}
	s.Cost += cost
	s.Tokens += tokens
	s.Turns++
	if len(a.ledger) > 120 { // keep about four months
		days := make([]string, 0, len(a.ledger))
		for d := range a.ledger {
			days = append(days, d)
		}
		sort.Strings(days)
		for _, d := range days[:len(days)-120] {
			delete(a.ledger, d)
		}
	}
	a.dirty = true
}

// ledgerView is the last 30 days per project for the Usage page. Caller holds a.mu.
func (a *App) ledgerView() map[string]any {
	days := []map[string]any{}
	for i := 29; i >= 0; i-- {
		d := time.Now().AddDate(0, 0, -i).Format("2006-01-02")
		per := map[string]*Spend{}
		for k, v := range a.ledger[d] {
			per[k] = v
		}
		days = append(days, map[string]any{"day": d, "projects": per})
	}
	return map[string]any{"days": days}
}
