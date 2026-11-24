package main

// Auto-updater: checks GitHub releases, downloads AgentHQ-windows.zip, verifies its
// SHA-256 and swaps the running exe. Installs by itself when no agent is working.

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const defaultUpdateRepo = "mikeey77/agent-hq"

type UpdateInfo struct {
	Current   string    `json:"current"`
	Latest    string    `json:"latest"`
	Available bool      `json:"available"`
	Notes     string    `json:"notes"`
	Page      string    `json:"page"`
	State     string    `json:"state"` // idle | checking | downloading | ready | installing | error
	Error     string    `json:"error"`
	Checked   time.Time `json:"checked"`

	zipURL, sumURL, exePath string
}

// newer reports whether version a is higher than b ("2.10.0" > "2.9.3").
func newer(a, b string) bool {
	parse := func(v string) []int {
		v = strings.TrimPrefix(strings.TrimSpace(v), "v")
		v, _, _ = strings.Cut(v, "-")
		var out []int
		for _, p := range strings.Split(v, ".") {
			n, _ := strconv.Atoi(p)
			out = append(out, n)
		}
		for len(out) < 3 {
			out = append(out, 0)
		}
		return out
	}
	x, y := parse(a), parse(b)
	for i := 0; i < 3; i++ {
		if x[i] != y[i] {
			return x[i] > y[i]
		}
	}
	return false
}

func (a *App) updateAPI() string {
	if u := os.Getenv("AGENTHQ_UPDATE_API"); u != "" {
		return u
	}
	repo := a.cfg.UpdateRepo
	if repo == "" {
		repo = defaultUpdateRepo
	}
	return "https://api.github.com/repos/" + repo + "/releases/latest"
}

func (a *App) checkUpdate() {
	a.mu.Lock()
	if a.upd.State == "downloading" || a.upd.State == "installing" {
		a.mu.Unlock()
		return
	}
	a.upd.State, a.upd.Error = "checking", ""
	api := a.updateAPI()
	a.mu.Unlock()

	var rel struct {
		Tag     string `json:"tag_name"`
		Body    string `json:"body"`
		HTMLURL string `json:"html_url"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	err := func() error {
		req, _ := http.NewRequest("GET", api, nil)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("User-Agent", "AgentHQ/"+version)
		resp, err := httpClient.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode == 404 {
			return errors.New("no release published yet")
		}
		if resp.StatusCode != 200 {
			return fmt.Errorf("GitHub answered %d", resp.StatusCode)
		}
		return json.NewDecoder(resp.Body).Decode(&rel)
	}()

	a.mu.Lock()
	defer a.mu.Unlock()
	a.upd.Checked = time.Now()
	if err != nil {
		a.upd.State, a.upd.Error = "idle", err.Error()
		return
	}
	a.upd.Latest = strings.TrimPrefix(rel.Tag, "v")
	a.upd.Notes, a.upd.Page = truncate(rel.Body, 600), rel.HTMLURL
	a.upd.zipURL, a.upd.sumURL = "", ""
	for _, as := range rel.Assets {
		switch as.Name {
		case "AgentHQ-windows.zip":
			a.upd.zipURL = as.URL
		case "AgentHQ-windows.zip.sha256":
			a.upd.sumURL = as.URL
		}
	}
	a.upd.Available = newer(a.upd.Latest, version) && a.upd.zipURL != ""
	if !a.upd.Available {
		a.upd.State = "idle"
		return
	}
	if a.upd.exePath != "" && a.upd.State == "ready" {
		return
	}
	a.upd.State = "idle"
	if a.cfg.AutoUpdate && runtime.GOOS == "windows" {
		go a.downloadUpdate(true)
	}
}

func fetch(url string, w io.Writer) error {
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("User-Agent", "AgentHQ/"+version)
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}
	_, err = io.Copy(w, resp.Body)
	return err
}

// downloadUpdate fetches and verifies the release; autoInstall installs when no agent is busy.
func (a *App) downloadUpdate(autoInstall bool) {
	a.mu.Lock()
	if a.upd.State == "downloading" || !a.upd.Available {
		a.mu.Unlock()
		return
	}
	a.upd.State, a.upd.Error = "downloading", ""
	zipURL, sumURL, latest := a.upd.zipURL, a.upd.sumURL, a.upd.Latest
	a.mu.Unlock()

	exe, err := func() (string, error) {
		dir := filepath.Join(configDir(), "update")
		_ = os.RemoveAll(dir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
		zipPath := filepath.Join(dir, "AgentHQ-windows.zip")
		f, err := os.Create(zipPath)
		if err != nil {
			return "", err
		}
		h := sha256.New()
		err = fetch(zipURL, io.MultiWriter(f, h))
		f.Close()
		if err != nil {
			return "", err
		}
		if sumURL != "" {
			var sb strings.Builder
			if err := fetch(sumURL, &sb); err != nil {
				return "", err
			}
			want := strings.ToLower(strings.Fields(sb.String() + " x")[0])
			if got := hex.EncodeToString(h.Sum(nil)); got != want {
				return "", errors.New("checksum mismatch, update refused")
			}
		}
		zr, err := zip.OpenReader(zipPath)
		if err != nil {
			return "", err
		}
		defer zr.Close()
		for _, zf := range zr.File {
			if strings.EqualFold(filepath.Base(zf.Name), "AgentHQ.exe") {
				rc, err := zf.Open()
				if err != nil {
					return "", err
				}
				out := filepath.Join(dir, "AgentHQ.exe")
				w, err := os.Create(out)
				if err != nil {
					rc.Close()
					return "", err
				}
				_, err = io.Copy(w, rc)
				rc.Close()
				w.Close()
				return out, err
			}
		}
		return "", errors.New("AgentHQ.exe not found in the release zip")
	}()

	a.mu.Lock()
	if err != nil {
		a.upd.State, a.upd.Error = "error", err.Error()
		a.mu.Unlock()
		return
	}
	a.upd.State, a.upd.exePath = "ready", exe
	busy := false
	for _, ag := range a.agents {
		if isActive(ag.Status) {
			busy = true
		}
	}
	if !(autoInstall && !busy) {
		a.emit("update", "", a.T("update_ready", latest))
	}
	a.mu.Unlock()
	if autoInstall && !busy {
		a.installUpdate()
	}
}

// installUpdate swaps the exe and restarts. Running agents pause and resume after the restart.
func (a *App) installUpdate() error {
	a.mu.Lock()
	src := a.upd.exePath
	if a.upd.State != "ready" || src == "" {
		a.mu.Unlock()
		return errors.New("no downloaded update")
	}
	a.upd.State = "installing"
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		a.upd.State, a.upd.Error = "error", err.Error()
		a.mu.Unlock()
		return err
	}
	old := exe + ".old"
	_ = os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		a.upd.State, a.upd.Error = "error", err.Error()
		a.mu.Unlock()
		return err
	}
	if err := copyFile(src, exe); err != nil {
		_ = os.Rename(old, exe) // roll back
		a.upd.State, a.upd.Error = "error", err.Error()
		a.mu.Unlock()
		return err
	}
	for _, ag := range a.agents {
		if isActive(ag.Status) || ag.Status == "queued" {
			ag.NeedsContinue = ag.SessionID != ""
			ag.ResumeAfterRestart = true
			a.killAgent(ag)
			ag.Status = "paused"
		}
	}
	a.emit("info", "", a.T("update_installing", a.upd.Latest))
	a.saveState()
	a.mu.Unlock()
	log.Printf("updating %s -> %s", version, exe)
	args := []string{"--background", "--updated"}
	if a.window {
		args = append(args, "--window") // reopen the app window after updating
	}
	cmd := hiddenCmd(exe, args...)
	if err := cmd.Start(); err != nil {
		_ = os.Rename(old, exe)
		return err
	}
	go func() { time.Sleep(200 * time.Millisecond); a.browser.Shutdown(); os.Exit(0) }()
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// cleanupOldExe removes the previous exe left behind by an update.
func cleanupOldExe() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	for i := 0; i < 10; i++ {
		if err := os.Remove(exe + ".old"); err == nil || os.IsNotExist(err) {
			return
		}
		time.Sleep(time.Second)
	}
}

func (a *App) updateLoop() {
	if a.demo {
		return
	}
	time.Sleep(8 * time.Second)
	for {
		a.checkUpdate()
		time.Sleep(6 * time.Hour)
	}
}

func (a *App) updateRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/update", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		u := a.upd
		u.Current = version
		a.mu.Unlock()
		writeJSON(w, u)
	})
	mux.HandleFunc("/api/update/check", func(w http.ResponseWriter, r *http.Request) {
		a.checkUpdate()
		a.mu.Lock()
		u := a.upd
		u.Current = version
		a.mu.Unlock()
		writeJSON(w, u)
	})
	mux.HandleFunc("/api/update/install", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		st := a.upd.State
		a.mu.Unlock()
		if runtime.GOOS != "windows" && os.Getenv("AGENTHQ_UPDATE_ANYOS") == "" {
			http.Error(w, "updates install on Windows only", 400)
			return
		}
		if st != "ready" {
			go a.downloadUpdate(false)
			go func() { // install as soon as the download is ready
				for i := 0; i < 600; i++ {
					time.Sleep(time.Second)
					a.mu.Lock()
					s := a.upd.State
					a.mu.Unlock()
					if s == "ready" {
						_ = a.installUpdate()
						return
					}
					if s == "error" {
						return
					}
				}
			}()
			writeJSON(w, map[string]string{"state": "downloading"})
			return
		}
		if err := a.installUpdate(); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, map[string]string{"state": "installing"})
	})
}
