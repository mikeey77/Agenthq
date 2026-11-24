package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestDecisionEmojis(t *testing.T) {
	yn := []string{"Yes", "No"}
	if !yesNo(yn) || !yesNo([]string{"Allow", "Deny"}) || !yesNo([]string{"Ja, doorgaan", "Nee"}) || yesNo([]string{"Vitest", "Jest"}) {
		t.Fatal("yesNo")
	}
	if e := optionEmojis(yn); e[0] != "✅" || e[1] != "❌" {
		t.Fatal(e)
	}
	opts := []string{"Vitest", "Jest", "Both"}
	if e := optionEmojis(opts); e[2] != "3️⃣" {
		t.Fatal(e)
	}
	cases := map[string]int{"1️⃣": 0, "2": 1, "3️⃣": 2, "jest": 1}
	for in, want := range cases {
		if i, ok := matchOption(opts, in); !ok || i != want {
			t.Errorf("%q → %d %v", in, i, ok)
		}
	}
	for in, want := range map[string]int{"✅": 0, "❌": 1, "👍": 0, "nee": 1, "✔️": 0} {
		if i, ok := matchOption(yn, in); !ok || i != want {
			t.Errorf("%q → %d %v", in, i, ok)
		}
	}
	if _, ok := matchOption(opts, "use vitest but also try jest"); ok {
		t.Error("custom answer matched")
	}
}

func TestGithubCreateRepo(t *testing.T) {
	var created map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/user":
			_, _ = w.Write([]byte(`{"login":"mikeey77"}`))
		case "/user/repos":
			_ = json.NewDecoder(r.Body).Decode(&created)
			w.WriteHeader(201)
			_, _ = w.Write([]byte(`{"full_name":"mikeey77/` + created["name"].(string) + `","html_url":"https://github.com/mikeey77/` + created["name"].(string) + `"}`))
		}
	}))
	defer srv.Close()
	githubAPI = srv.URL
	os.Setenv("AGENTHQ_HOME", t.TempDir())
	a := &App{cfg: defaultConfig(), agents: map[string]*Agent{}, tgMsgs: map[int64]string{}, usage: newUsageTracker()}
	a.cfg.Workspace = t.TempDir()
	a.cfg.GitHubToken = "tok"
	a.mu.Lock()
	p, err := a.newProject("Scrap Duel", "game", "")
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	ag := &Agent{ID: "x", Name: "atlas", Cwd: p.Folder, ProjectID: p.ID}
	a.agents["x"] = ag
	req := httptest.NewRequest("POST", "/", nil)
	out, isErr := a.githubTool(req, ag, "github_create_repo", map[string]any{"name": "Scrap Duel", "description": "game", "push": false})
	if isErr || created["private"] != true || created["name"] != "scrap-duel" || p.Repo != "https://github.com/mikeey77/scrap-duel" {
		t.Fatalf("%s %v %v %s", out, isErr, created, p.Repo)
	}
	if filepath.Base(p.Folder) != "scrap-duel" {
		t.Fatal(p.Folder)
	}
	// project tools
	a.mu.Lock()
	r, _ := a.projectTool(ag, "add_task", map[string]any{"title": "Arena map"})
	board, _ := a.projectTool(ag, "project_tasks", nil)
	a.mu.Unlock()
	if r == "" || len(p.Tasks) != 1 || !contains(board, "[todo] Arena map") {
		t.Fatal(board)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOf(s, sub) >= 0)
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
