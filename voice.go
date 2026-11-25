package main

// Voice messages: Telegram voice notes are turned into text on your own PC with whisper.cpp
// (no API costs). The first time, Agent HQ downloads whisper.cpp, a speech model and ffmpeg
// (about 235 MB) into its own folder.

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

var voiceURLs = struct{ whisper, ffmpeg, model string }{
	whisper: "https://github.com/ggml-org/whisper.cpp/releases/download/v1.8.2/whisper-bin-x64.zip",
	ffmpeg:  "https://github.com/BtbN/FFmpeg-Builds/releases/download/latest/ffmpeg-master-latest-win64-lgpl-shared.zip",
	model:   "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/models/ggml-base.bin",
}

type VoiceState struct {
	Ready      bool   `json:"ready"`
	Installing bool   `json:"installing"`
	Step       string `json:"step,omitempty"`
	Pct        int    `json:"pct"`
	Err        string `json:"err,omitempty"`
	mu         sync.Mutex
}

func voiceDir() string { return filepath.Join(configDir(), "voice") }

func exeName(n string) string {
	if runtime.GOOS == "windows" {
		return n + ".exe"
	}
	return n
}

// voiceTools finds whisper-cli, ffmpeg and the model. Empty strings mean missing.
func voiceTools() (whisper, ffmpeg, model string) {
	d := voiceDir()
	if p := filepath.Join(d, "whisper", exeName("whisper-cli")); fileExists(p) {
		whisper = p
	} else if p, err := exec.LookPath("whisper-cli"); err == nil {
		whisper = p
	}
	if p := filepath.Join(d, "ffmpeg", exeName("ffmpeg")); fileExists(p) {
		ffmpeg = p
	} else if p, err := exec.LookPath("ffmpeg"); err == nil {
		ffmpeg = p
	}
	if p := filepath.Join(d, "ggml-base.bin"); fileExists(p) {
		model = p
	}
	return
}

func fileExists(p string) bool { st, err := os.Stat(p); return err == nil && !st.IsDir() }

func (a *App) voiceStatus() VoiceState {
	a.voice.mu.Lock()
	defer a.voice.mu.Unlock()
	w, f, m := voiceTools()
	a.voice.Ready = w != "" && f != "" && m != ""
	return VoiceState{Ready: a.voice.Ready, Installing: a.voice.Installing, Step: a.voice.Step, Pct: a.voice.Pct, Err: a.voice.Err}
}

func (a *App) voiceProgress(step string, pct int) {
	a.voice.mu.Lock()
	a.voice.Step, a.voice.Pct = step, pct
	a.voice.mu.Unlock()
}

// download fetches url into memory (or a file when dst is set), reporting progress.
func (a *App) download(url, step, dst string) ([]byte, error) {
	resp, err := (&http.Client{Timeout: 30 * time.Minute}).Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%s: %s", step, resp.Status)
	}
	var w io.Writer
	var buf bytes.Buffer
	var f *os.File
	if dst != "" {
		if f, err = os.Create(dst + ".part"); err != nil {
			return nil, err
		}
		w = f
	} else {
		w = &buf
	}
	total, done := resp.ContentLength, int64(0)
	chunk := make([]byte, 256<<10)
	for {
		n, rerr := resp.Body.Read(chunk)
		if n > 0 {
			if _, err := w.Write(chunk[:n]); err != nil {
				return nil, err
			}
			done += int64(n)
			if total > 0 {
				a.voiceProgress(step, int(done*100/total))
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			if f != nil {
				f.Close()
			}
			return nil, rerr
		}
	}
	if f != nil {
		f.Close()
		return nil, os.Rename(dst+".part", dst)
	}
	return buf.Bytes(), nil
}

// unzipInto extracts the files whose base name passes keep into dir (flattened).
func unzipInto(data []byte, dir string, keep func(string) bool) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, f := range zr.File {
		name := filepath.Base(f.Name)
		if f.FileInfo().IsDir() || !keep(name) {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o755); err != nil {
			return err
		}
	}
	return nil
}

// installVoice downloads whatever is missing. It runs in the background.
func (a *App) installVoice() {
	a.voice.mu.Lock()
	if a.voice.Installing {
		a.voice.mu.Unlock()
		return
	}
	a.voice.Installing, a.voice.Err = true, ""
	a.voice.mu.Unlock()
	err := func() error {
		if runtime.GOOS != "windows" {
			return errors.New("automatic install is for Windows; install whisper-cli and ffmpeg yourself and put ggml-base.bin in " + voiceDir())
		}
		d := voiceDir()
		w, f, m := voiceTools()
		if w == "" {
			data, err := a.download(voiceURLs.whisper, "whisper", "")
			if err != nil {
				return err
			}
			if err := unzipInto(data, filepath.Join(d, "whisper"), func(n string) bool {
				return n == "whisper-cli.exe" || strings.HasSuffix(n, ".dll")
			}); err != nil {
				return err
			}
		}
		if f == "" {
			data, err := a.download(voiceURLs.ffmpeg, "ffmpeg", "")
			if err != nil {
				return err
			}
			if err := unzipInto(data, filepath.Join(d, "ffmpeg"), func(n string) bool {
				return n == "ffmpeg.exe" || strings.HasSuffix(n, ".dll")
			}); err != nil {
				return err
			}
		}
		if m == "" {
			if err := os.MkdirAll(d, 0o755); err != nil {
				return err
			}
			if _, err := a.download(voiceURLs.model, "model", filepath.Join(d, "ggml-base.bin")); err != nil {
				return err
			}
		}
		return nil
	}()
	a.voice.mu.Lock()
	a.voice.Installing, a.voice.Step, a.voice.Pct = false, "", 0
	if err != nil {
		a.voice.Err = err.Error()
	}
	a.voice.mu.Unlock()
	a.mu.Lock()
	if err != nil {
		a.emit("critical", "", a.T("voice_failed", err.Error()))
	} else {
		a.emit("update", "", a.T("voice_ready"))
	}
	a.mu.Unlock()
}

// transcribe turns an audio file (Telegram .oga/.ogg, mp3, m4a, wav) into text.
func transcribe(audio string) (string, error) {
	w, f, m := voiceTools()
	if w == "" || f == "" || m == "" {
		return "", errors.New("voice is not installed")
	}
	wav := strings.TrimSuffix(audio, filepath.Ext(audio)) + ".wav"
	defer os.Remove(wav)
	if out, err := runIn(filepath.Dir(audio), 2*time.Minute, f, "-y", "-loglevel", "error", "-i", audio, "-ar", "16000", "-ac", "1", "-c:a", "pcm_s16le", wav); err != nil {
		return "", fmt.Errorf("ffmpeg: %s", truncate(out, 300))
	}
	threads := runtime.NumCPU()
	if threads > 8 {
		threads = 8
	}
	out, err := runIn(filepath.Dir(audio), 5*time.Minute, w, "-m", m, "-f", wav, "-l", "auto", "-nt", "-np", "-t", fmt.Sprint(threads))
	if err != nil {
		return "", fmt.Errorf("whisper: %s", truncate(out, 300))
	}
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "whisper_") || strings.HasPrefix(l, "system_info") || strings.HasPrefix(l, "main:") {
			continue
		}
		lines = append(lines, l)
	}
	text := strings.TrimSpace(strings.Join(lines, " "))
	if text == "" || text == "[BLANK_AUDIO]" {
		return "", errors.New("no speech found")
	}
	return text, nil
}

func (a *App) voiceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/voice", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			if a.demo {
				writeJSON(w, map[string]bool{"ok": true})
				return
			}
			go a.installVoice()
		}
		writeJSON(w, a.voiceStatus())
	})
}
