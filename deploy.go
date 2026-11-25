package main

// Deploy: put a project online with one click (or one tool call) on Netlify or GitHub Pages.
// The site is built first when the project has a build script, then the output folder is uploaded.

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

var netlifyAPI = "https://api.netlify.com/api/v1"

type Deploy struct {
	Target string    `json:"target"` // netlify | pages
	URL    string    `json:"url"`
	At     time.Time `json:"at"`
	OK     bool      `json:"ok"`
	Note   string    `json:"note,omitempty"`
}

// netlifyToken finds a token in Settings or the Netlify connector. Caller holds a.mu.
func (a *App) netlifyToken() string {
	if t := strings.TrimSpace(a.cfg.NetlifyToken); t != "" {
		return t
	}
	for _, c := range a.cfg.Connectors {
		if v := c.Env["NETLIFY_PERSONAL_ACCESS_TOKEN"]; v != "" {
			return v
		}
	}
	return ""
}

// publishDir finds the folder to upload, building the site first when needed.
func publishDir(folder string, logf func(string)) (string, error) {
	pkg := filepath.Join(folder, "package.json")
	if b, err := os.ReadFile(pkg); err == nil {
		var p struct{ Scripts map[string]string }
		_ = json.Unmarshal(b, &p)
		if p.Scripts["build"] != "" {
			if _, err := os.Stat(filepath.Join(folder, "node_modules")); err != nil {
				logf("npm install")
				if out, err := runIn(folder, 10*time.Minute, "npm", "install"); err != nil {
					return "", fmt.Errorf("npm install failed: %s", truncate(out, 400))
				}
			}
			logf("npm run build")
			if out, err := runIn(folder, 10*time.Minute, "npm", "run", "build"); err != nil {
				return "", fmt.Errorf("build failed: %s", truncate(out, 600))
			}
		}
	}
	for _, d := range []string{"dist", "build", "out", "public", "_site", "docs", "."} {
		if _, err := os.Stat(filepath.Join(folder, d, "index.html")); err == nil {
			return filepath.Join(folder, d), nil
		}
	}
	return "", errors.New("no index.html found (looked in dist, build, out, public, _site, docs and the project folder)")
}

func isWindows() bool { return runtime.GOOS == "windows" }

func runIn(dir string, timeout time.Duration, name string, args ...string) (string, error) {
	cmd := hiddenCmd(name, args...)
	if isWindows() && (name == "npm" || name == "npx") {
		cmd = hiddenCmd("cmd", append([]string{"/c", name}, args...)...)
	}
	cmd.Dir = dir
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	if err := cmd.Start(); err != nil {
		return err.Error(), err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return buf.String(), err
	case <-time.After(timeout):
		killTree(cmd)
		return buf.String(), errors.New("timed out")
	}
}

var skipDeploy = map[string]bool{".git": true, "node_modules": true, ".agenthq": true, "from-telegram": true}

func zipDir(dir string) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		if d.IsDir() {
			if skipDeploy[d.Name()] && rel != "." {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(d.Name(), ".env") {
			return nil // never publish secrets
		}
		w, err := zw.Create(filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(w, f)
		return err
	})
	if err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	if buf.Len() > 200<<20 {
		return nil, errors.New("site is larger than 200 MB")
	}
	return buf.Bytes(), nil
}

func netlifyReq(token, method, path, ctype string, body io.Reader) (map[string]any, error) {
	req, _ := http.NewRequest(method, netlifyAPI+path, body)
	req.Header.Set("Authorization", "Bearer "+token)
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	resp, err := (&http.Client{Timeout: 5 * time.Minute}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if resp.StatusCode >= 300 {
		return out, fmt.Errorf("Netlify %d: %v", resp.StatusCode, out["message"])
	}
	return out, nil
}

// deployNetlify uploads dir to the project's Netlify site, creating the site the first time.
func deployNetlify(token string, p *Project, dir string) (string, error) {
	if p.NetlifySite == "" {
		name := slug(p.Name) + "-" + newID()[:4]
		b, _ := json.Marshal(map[string]string{"name": name})
		site, err := netlifyReq(token, "POST", "/sites", "application/json", bytes.NewReader(b))
		if err != nil {
			return "", err
		}
		p.NetlifySite = str(site, "id")
	}
	z, err := zipDir(dir)
	if err != nil {
		return "", err
	}
	d, err := netlifyReq(token, "POST", "/sites/"+p.NetlifySite+"/deploys", "application/zip", bytes.NewReader(z))
	if err != nil {
		return "", err
	}
	id := str(d, "id")
	for i := 0; i < 60; i++ { // wait until it is live
		st := str(d, "state")
		if st == "ready" {
			break
		}
		if st == "error" {
			return "", fmt.Errorf("Netlify: %s", str(d, "error_message"))
		}
		time.Sleep(2 * time.Second)
		if d, err = netlifyReq(token, "GET", "/deploys/"+id, "", nil); err != nil {
			return "", err
		}
	}
	u := str(d, "ssl_url")
	if u == "" {
		u = str(d, "url")
	}
	return u, nil
}

// deployPages pushes dir to the gh-pages branch of the project's repo and turns Pages on.
func deployPages(token string, p *Project, dir string) (string, error) {
	if !strings.Contains(p.Repo, "github.com/") {
		return "", errors.New("this project has no GitHub repo yet; create one first")
	}
	full := strings.TrimSuffix(strings.Trim(strings.SplitN(p.Repo, "github.com/", 2)[1], "/"), ".git")
	login, err := ghLogin(token)
	if err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp("", "agenthq-pages-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	z, err := zipDir(dir) // copy the site via zip so secrets and node_modules stay out
	if err != nil {
		return "", err
	}
	zr, _ := zip.NewReader(bytes.NewReader(z), int64(len(z)))
	for _, f := range zr.File {
		dst := filepath.Join(tmp, filepath.FromSlash(f.Name))
		_ = os.MkdirAll(filepath.Dir(dst), 0o755)
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		_ = os.WriteFile(dst, b, 0o644)
	}
	_ = os.WriteFile(filepath.Join(tmp, ".nojekyll"), nil, 0o644)
	if _, err := gitRun(tmp, "init", "-b", "gh-pages"); err != nil {
		return "", err
	}
	_, _ = gitRun(tmp, "add", "-A")
	if _, err := gitRun(tmp, "-c", "user.name=Agent HQ", "-c", "user.email=agenthq@users.noreply.github.com", "commit", "-m", "Deploy from Agent HQ"); err != nil {
		return "", err
	}
	auth := basicAuth(login, token)
	if _, err := gitRun(tmp, "-c", "http.https://github.com/.extraheader=AUTHORIZATION: basic "+auth, "push", "-f", "https://github.com/"+full+".git", "gh-pages"); err != nil {
		return "", err
	}
	_, code, err := ghReq(token, "POST", "/repos/"+full+"/pages", map[string]any{"source": map[string]string{"branch": "gh-pages", "path": "/"}})
	if err != nil && code != 409 { // 409 = already on
		if code == 422 {
			return "", errors.New("GitHub Pages is not available for this repo (private repos need a paid GitHub plan); make the repo public or use Netlify")
		}
		return "", err
	}
	owner, repo, _ := strings.Cut(full, "/")
	return fmt.Sprintf("https://%s.github.io/%s/", strings.ToLower(owner), repo), nil
}

// runDeploy deploys a project. It does not hold a.mu while building or uploading.
func (a *App) runDeploy(p *Project, target string, logf func(string)) (string, error) {
	a.mu.Lock()
	ntoken, gtoken, folder := a.netlifyToken(), a.githubToken(), p.Folder
	if p.Deploying {
		a.mu.Unlock()
		return "", errors.New("a deploy is already running")
	}
	p.Deploying = true
	a.mu.Unlock()
	defer func() { a.mu.Lock(); p.Deploying = false; a.dirty = true; a.mu.Unlock() }()
	if target == "" {
		target = "netlify"
		if ntoken == "" && gtoken != "" {
			target = "pages"
		}
	}
	if target == "netlify" && ntoken == "" {
		return "", errors.New(a.T("deploy_need_netlify"))
	}
	if target == "pages" && gtoken == "" {
		return "", errors.New(a.T("deploy_need_github"))
	}
	dir, err := publishDir(folder, logf)
	var u string
	if err == nil {
		logf("upload " + target)
		if target == "netlify" {
			u, err = deployNetlify(ntoken, p, dir)
		} else {
			u, err = deployPages(gtoken, p, dir)
		}
	}
	a.mu.Lock()
	d := Deploy{Target: target, URL: u, At: time.Now(), OK: err == nil}
	if err != nil {
		d.Note = truncate(err.Error(), 300)
	}
	p.Deploys = append([]Deploy{d}, p.Deploys...)
	if len(p.Deploys) > 15 {
		p.Deploys = p.Deploys[:15]
	}
	if err == nil {
		p.SiteURL = u
		a.emit("update", "", a.T("deploy_done", p.Name, u))
	} else {
		a.emit("critical", "", a.T("deploy_failed", p.Name, d.Note))
	}
	a.mu.Unlock()
	return u, err
}

var deployTool = map[string]any{"name": "deploy", "description": "Put your project online on Netlify or GitHub Pages and get the public URL. Builds first when package.json has a build script. Use when the user asks to deploy, publish or put it online.",
	"inputSchema": schema(map[string]any{"target": map[string]any{"type": "string", "enum": []string{"netlify", "pages"}, "description": "Default netlify"}})}

func (a *App) deployRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/deploy", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Project, Target string }
		_ = readBody(r, &in)
		a.mu.Lock()
		p := a.projectByID(in.Project)
		a.mu.Unlock()
		if p == nil {
			http.Error(w, "no such project", 404)
			return
		}
		if a.demo {
			a.mu.Lock()
			p.SiteURL = "https://" + slug(p.Name) + ".netlify.app"
			p.Deploys = append([]Deploy{{Target: "netlify", URL: p.SiteURL, At: time.Now(), OK: true}}, p.Deploys...)
			a.mu.Unlock()
			writeJSON(w, map[string]string{"url": p.SiteURL})
			return
		}
		go func() { _, _ = a.runDeploy(p, in.Target, func(string) {}) }()
		writeJSON(w, map[string]bool{"started": true})
	})
}
