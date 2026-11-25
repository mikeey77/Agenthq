package main

// GitHub: agents can create a repository and push their work with one tool call.
// The token comes from Settings (GitHub token) or from the GitHub connector.

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var githubAPI = "https://api.github.com"

var githubTools = []map[string]any{
	{"name": "github_create_repo", "description": "Create a GitHub repository on the user's account and (by default) push your working folder to it. Private unless the user asked for public. Returns the repo URL.",
		"inputSchema": schema(map[string]any{
			"name":        sProp("Repository name, e.g. my-app"),
			"description": sProp("One-line description"),
			"private":     map[string]any{"type": "boolean", "description": "Default true"},
			"push":        map[string]any{"type": "boolean", "description": "Commit and push the working folder. Default true"},
		}, "name")},
	{"name": "github_push", "description": "Commit everything in your working folder and push it to the project's GitHub repo.",
		"inputSchema": schema(map[string]any{"message": sProp("Commit message")}, "message")},
}

// githubToken finds a token. Caller holds a.mu.
func (a *App) githubToken() string {
	if t := strings.TrimSpace(a.cfg.GitHubToken); t != "" {
		return t
	}
	for _, c := range a.cfg.Connectors {
		if c.Preset == "github" {
			for k, v := range c.Headers {
				if strings.EqualFold(k, "Authorization") {
					return strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(v, "Bearer "), "token "))
				}
			}
			for _, k := range []string{"GITHUB_PERSONAL_ACCESS_TOKEN", "GITHUB_TOKEN"} {
				if v := c.Env[k]; v != "" {
					return v
				}
			}
		}
	}
	return ""
}

func ghReq(token, method, path string, body any) (map[string]any, int, error) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, githubAPI+path, rd)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	cl := &http.Client{Timeout: 30 * time.Second}
	resp, err := cl.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if resp.StatusCode >= 300 {
		msg := str(out, "message")
		if errs, ok := out["errors"].([]any); ok && len(errs) > 0 {
			if e, ok := errs[0].(map[string]any); ok && str(e, "message") != "" {
				msg += ": " + str(e, "message")
			}
		}
		return out, resp.StatusCode, fmt.Errorf("GitHub %d: %s", resp.StatusCode, msg)
	}
	return out, resp.StatusCode, nil
}

func ghLogin(token string) (string, error) {
	u, _, err := ghReq(token, "GET", "/user", nil)
	if err != nil {
		return "", err
	}
	return str(u, "login"), nil
}

func gitRun(dir string, args ...string) (string, error) {
	cmd := hiddenCmd("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	s := strings.TrimSpace(string(out))
	if err != nil {
		return s, fmt.Errorf("git %s: %s", args[0], truncate(s, 300))
	}
	return s, nil
}

// gitPush commits everything in dir and pushes it to https://github.com/<full>.
// The token is passed per command, so it is never stored in the repository config.
func gitPush(dir, full, token, login, msg string) (string, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return "", errors.New("git is not installed (install Git for Windows)")
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		if _, err := gitRun(dir, "init", "-b", "main"); err != nil {
			return "", err
		}
	}
	gi := filepath.Join(dir, ".gitignore")
	if _, err := os.Stat(gi); err != nil {
		_ = os.WriteFile(gi, []byte("node_modules/\n.env\n*.log\n.agenthq*\n"), 0o644)
	}
	if _, err := gitRun(dir, "add", "-A"); err != nil {
		return "", err
	}
	_, _ = gitRun(dir, "-c", "user.name=Agent HQ", "-c", "user.email=agenthq@users.noreply.github.com", "commit", "-m", msg)
	url := "https://github.com/" + full + ".git"
	if _, err := gitRun(dir, "remote", "get-url", "origin"); err != nil {
		_, _ = gitRun(dir, "remote", "add", "origin", url)
	}
	auth := basicAuth(login, token)
	out, err := gitRun(dir, "-c", "http.https://github.com/.extraheader=AUTHORIZATION: basic "+auth, "push", "-u", "origin", "HEAD:main")
	return out, err
}

func basicAuth(user, pass string) string {
	return base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
}

// githubTool runs a github_* tool. Called without a.mu held (it may wait for a decision).
func (a *App) githubTool(r *http.Request, ag *Agent, name string, args map[string]any) (string, bool) {
	a.mu.Lock()
	token, dir := a.githubToken(), ag.Cwd
	p := a.projectByID(ag.ProjectID)
	a.mu.Unlock()
	if token == "" {
		return "No GitHub token set. Tell the user to add one in Agent HQ → Settings → GitHub (a fine-grained token with Administration and Contents write access).", true
	}
	login, err := ghLogin(token)
	if err != nil {
		return "GitHub token does not work: " + err.Error(), true
	}
	switch name {
	case "github_create_repo":
		repo := slug(str(args, "name"))
		if repo == "" {
			return "name is empty", true
		}
		private := str(args, "private") != "false"
		push := str(args, "push") != "false"
		if !private { // publishing is visible to everyone: let the user confirm
			a.mu.Lock()
			d := a.newDecision(ag, "decision", a.T("gh_public_q", login+"/"+repo), str(args, "description"), []string{a.T("yes_public"), a.T("make_private")})
			a.mu.Unlock()
			ans, ok := a.waitDecision(r, d, 10*time.Minute)
			if !ok || ans == "" {
				return "The user did not confirm a public repository yet. Ask again later or create it private.", true
			}
			if ans != a.T("yes_public") {
				private = true
			}
		}
		out, code, err := ghReq(token, "POST", "/user/repos", map[string]any{"name": repo, "description": str(args, "description"), "private": private})
		if err != nil && code != 422 {
			return err.Error(), true
		}
		if code == 422 { // already exists
			return fmt.Sprintf("Repository %s/%s already exists. Pick another name, or use github_push if it is this project's repo.", login, repo), true
		}
		full := str(out, "full_name")
		url := str(out, "html_url")
		a.mu.Lock()
		if p != nil && p.Repo == "" {
			p.Repo = url
			a.dirty = true
		}
		ag.ToolKind, ag.ToolTarget = "app", "github"
		a.emit("update", ag.ID, a.T("gh_created", ag.Name, url))
		a.mu.Unlock()
		res := fmt.Sprintf("Created %s (%s).", url, map[bool]string{true: "private", false: "public"}[private])
		if push {
			if _, err := gitPush(dir, full, token, login, "Initial commit from Agent HQ"); err != nil {
				return res + " Pushing failed: " + err.Error(), false
			}
			res += " Pushed the working folder to the main branch."
		}
		return res, false
	case "github_push":
		full := ""
		if p != nil && strings.Contains(p.Repo, "github.com/") {
			full = strings.TrimSuffix(strings.SplitN(p.Repo, "github.com/", 2)[1], ".git")
		}
		if full == "" {
			if u, err := gitRun(dir, "remote", "get-url", "origin"); err == nil && strings.Contains(u, "github.com") {
				full = strings.TrimSuffix(strings.TrimPrefix(strings.SplitN(u, "github.com", 2)[1], "/"), ".git")
				full = strings.TrimPrefix(full, ":")
			}
		}
		if full == "" {
			return "This folder has no GitHub repo yet. Use github_create_repo.", true
		}
		msg := str(args, "message")
		if msg == "" {
			msg = "Update from Agent HQ"
		}
		if _, err := gitPush(dir, full, token, login, msg); err != nil {
			return err.Error(), true
		}
		return "Pushed to https://github.com/" + full, false
	}
	return "unknown tool", true
}
