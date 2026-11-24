package main

// Scheduled missions: run a mission every day, on chosen weekdays, every N hours or once.
// A schedule that comes due while the usage limit is reached waits until the reset.

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type Schedule struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Mission   string    `json:"mission"`
	Project   string    `json:"project,omitempty"`
	Model     string    `json:"model,omitempty"`
	Kind      string    `json:"kind"`            // daily | weekly | interval | once
	Time      string    `json:"time,omitempty"`  // HH:MM for daily/weekly/once
	Days      []int     `json:"days,omitempty"`  // 0=Sunday … 6=Saturday for weekly
	Every     int       `json:"every,omitempty"` // minutes for interval
	Date      string    `json:"date,omitempty"`  // YYYY-MM-DD for once
	Enabled   bool      `json:"enabled"`
	NextRun   time.Time `json:"nextRun"`
	LastRun   time.Time `json:"lastRun,omitempty"`
	LastAgent string    `json:"lastAgent,omitempty"`
	Runs      int       `json:"runs"`
	By        string    `json:"by,omitempty"`
}

func parseHM(s string) (int, int, bool) {
	s = strings.TrimSpace(strings.ReplaceAll(s, ".", ":"))
	h, m, ok := strings.Cut(s, ":")
	if !ok {
		m = "0"
	}
	hh, e1 := strconv.Atoi(h)
	mm, e2 := strconv.Atoi(m)
	if e1 != nil || e2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, 0, false
	}
	return hh, mm, true
}

// next computes the next run after t, or zero when it will not run again.
func (s *Schedule) next(t time.Time) time.Time {
	switch s.Kind {
	case "interval":
		if s.Every < 15 {
			s.Every = 15
		}
		base := s.LastRun
		if base.IsZero() {
			return t.Add(time.Minute)
		}
		n := base.Add(time.Duration(s.Every) * time.Minute)
		if n.Before(t) {
			n = t.Add(time.Minute)
		}
		return n
	case "once":
		if s.Runs > 0 {
			return time.Time{}
		}
		h, m, _ := parseHM(s.Time)
		d, err := time.ParseInLocation("2006-01-02", s.Date, time.Local)
		if err != nil {
			d = t
		}
		n := time.Date(d.Year(), d.Month(), d.Day(), h, m, 0, 0, time.Local)
		if n.Before(t) && s.Date == "" {
			n = n.Add(24 * time.Hour)
		}
		return n
	}
	h, m, _ := parseHM(s.Time)
	for i := 0; i < 8; i++ {
		d := t.AddDate(0, 0, i)
		n := time.Date(d.Year(), d.Month(), d.Day(), h, m, 0, 0, time.Local)
		if !n.After(t) {
			continue
		}
		if s.Kind == "weekly" && len(s.Days) > 0 {
			ok := false
			for _, wd := range s.Days {
				if int(n.Weekday()) == wd {
					ok = true
				}
			}
			if !ok {
				continue
			}
		}
		return n
	}
	return time.Time{}
}

func (s *Schedule) validate() error {
	s.Mission = strings.TrimSpace(s.Mission)
	if s.Mission == "" {
		return errors.New("mission is empty")
	}
	switch s.Kind {
	case "daily", "weekly", "once":
		if _, _, ok := parseHM(s.Time); !ok {
			return errors.New("time must look like 08:30")
		}
		if s.Kind == "weekly" && len(s.Days) == 0 {
			return errors.New("pick at least one day")
		}
	case "interval":
		if s.Every < 15 {
			return errors.New("every must be at least 15 minutes")
		}
	default:
		return errors.New("kind must be daily, weekly, interval or once")
	}
	if s.Name == "" {
		s.Name = truncate(s.Mission, 40)
	}
	return nil
}

var dayNames = map[string][]string{
	"en": {"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"},
	"nl": {"zo", "ma", "di", "wo", "do", "vr", "za"},
}

// when describes a schedule in words ("every day at 08:00").
func (a *App) when(s *Schedule) string {
	switch s.Kind {
	case "daily":
		return a.T("sch_daily", s.Time)
	case "weekly":
		names := dayNames[a.cfg.Lang]
		if names == nil {
			names = dayNames["en"]
		}
		var ds []string
		for _, d := range s.Days {
			if d >= 0 && d < 7 {
				ds = append(ds, names[d])
			}
		}
		return a.T("sch_weekly", strings.Join(ds, ", "), s.Time)
	case "interval":
		if s.Every%60 == 0 {
			return a.T("sch_hours", s.Every/60)
		}
		return a.T("sch_minutes", s.Every)
	case "once":
		return a.T("sch_once", s.Date, s.Time)
	}
	return s.Kind
}

// addSchedule validates and stores a schedule. Caller holds a.mu.
func (a *App) addSchedule(s *Schedule) error {
	if err := s.validate(); err != nil {
		return err
	}
	if s.ID == "" {
		s.ID = newID()[:8]
		s.Enabled = true
		a.schedules = append(a.schedules, s)
	}
	s.NextRun = s.next(time.Now())
	a.dirty = true
	return nil
}

func (a *App) scheduleByRef(ref string) *Schedule {
	ref = strings.ToLower(strings.TrimSpace(ref))
	for i, s := range a.schedules {
		if s.ID == ref || strings.ToLower(s.Name) == ref || strconv.Itoa(i+1) == ref {
			return s
		}
	}
	return nil
}

// runSchedule starts a scheduled mission now. Caller holds a.mu.
func (a *App) runSchedule(s *Schedule) (*Agent, error) {
	if p := a.projectByID(s.Project); p != nil {
		a.nextProject = p.ID
	}
	cwd := ""
	if p := a.projectByID(s.Project); p != nil {
		cwd = p.Folder
	}
	ag, err := a.startMission(s.Mission, "", cwd, s.Model, nil)
	a.nextProject = ""
	s.LastRun = time.Now()
	s.Runs++
	if err != nil {
		s.NextRun = time.Now().Add(15 * time.Minute) // try again a bit later
		return nil, err
	}
	s.LastAgent = ag.ID
	s.NextRun = s.next(time.Now())
	if s.NextRun.IsZero() {
		s.Enabled = false
	}
	a.dirty = true
	a.emit("update", ag.ID, a.T("sch_started", s.Name, ag.Name))
	return ag, nil
}

func (a *App) scheduleLoop() {
	for range time.Tick(30 * time.Second) {
		a.mu.Lock()
		now := time.Now()
		for _, s := range a.schedules {
			if !s.Enabled || s.NextRun.IsZero() || now.Before(s.NextRun) || a.limited() {
				continue
			}
			if _, err := a.runSchedule(s); err != nil {
				a.emit("critical", "", a.T("sch_failed", s.Name, err.Error()))
			}
		}
		a.mu.Unlock()
	}
}

var scheduleTool = map[string]any{"name": "schedule_mission",
	"description": "Schedule a mission to run later or repeatedly (for requests like 'every Monday at 9', 'every 2 hours', 'tomorrow at 7'). The mission text must be self-contained.",
	"inputSchema": schema(map[string]any{
		"name":          sProp("Short name"),
		"mission":       sProp("The full mission to run each time"),
		"kind":          map[string]any{"type": "string", "enum": []string{"daily", "weekly", "interval", "once"}},
		"time":          sProp("HH:MM (24h), for daily, weekly and once"),
		"days":          map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "description": "Weekdays for weekly: 0=Sunday, 1=Monday … 6=Saturday"},
		"every_minutes": map[string]any{"type": "integer", "description": "For interval, at least 15"},
		"date":          sProp("YYYY-MM-DD for once (empty = next occurrence of time)"),
	}, "mission", "kind")}

// scheduleFromTool creates a schedule for an agent. Caller holds a.mu.
func (a *App) scheduleFromTool(ag *Agent, args map[string]any) (string, bool) {
	s := &Schedule{Name: str(args, "name"), Mission: str(args, "mission"), Kind: str(args, "kind"), Time: str(args, "time"), Date: str(args, "date"), Project: ag.ProjectID, By: ag.Name}
	if n, err := strconv.Atoi(str(args, "every_minutes")); err == nil {
		s.Every = n
	}
	if ds, ok := args["days"].([]any); ok {
		for _, d := range ds {
			if f, ok := d.(float64); ok {
				s.Days = append(s.Days, int(f))
			}
		}
	}
	if err := a.addSchedule(s); err != nil {
		return "Could not schedule: " + err.Error(), true
	}
	a.emit("update", ag.ID, a.T("sch_created", s.Name, a.when(s)))
	return fmt.Sprintf("Scheduled %q: %s. Next run %s.", s.Name, a.when(s), s.NextRun.Format("Mon 2 Jan 15:04")), false
}

func (a *App) scheduleRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/schedule", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Action string
			S      Schedule
		}
		_ = readBody(r, &in)
		a.mu.Lock()
		defer a.mu.Unlock()
		s := a.scheduleByRef(in.S.ID)
		var err error
		switch in.Action {
		case "save":
			if s == nil {
				n := in.S
				n.ID, n.By = "", "you"
				err = a.addSchedule(&n)
			} else {
				keep := *s
				*s = in.S
				s.LastRun, s.Runs, s.LastAgent, s.Enabled, s.By = keep.LastRun, keep.Runs, keep.LastAgent, keep.Enabled, keep.By
				err = a.addSchedule(s)
			}
		case "toggle":
			if s != nil {
				s.Enabled = !s.Enabled
				s.NextRun = s.next(time.Now())
			}
		case "run":
			if s != nil {
				_, err = a.runSchedule(s)
			}
		case "delete":
			for i, x := range a.schedules {
				if x == s {
					a.schedules = append(a.schedules[:i], a.schedules[i+1:]...)
					break
				}
			}
		}
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		a.dirty = true
		writeJSON(w, map[string]bool{"ok": true})
	})
}

type scheduleView struct {
	*Schedule
	When string `json:"when"`
}

func (a *App) scheduleViews() []scheduleView {
	out := []scheduleView{}
	for _, s := range a.schedules {
		out = append(out, scheduleView{s, a.when(s)})
	}
	return out
}
