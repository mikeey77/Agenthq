package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func newLibApp(t *testing.T) *App {
	os.Setenv("AGENTHQ_HOME", t.TempDir())
	a := &App{cfg: defaultConfig(), agents: map[string]*Agent{}, tgMsgs: map[int64]string{}, usage: newUsageTracker(), browser: newBrowser(), demo: true}
	a.cfg.Workspace = t.TempDir()
	return a
}

func TestMemorySkillsReviewLedger(t *testing.T) {
	a := newLibApp(t)
	a.mu.Lock()
	defer a.mu.Unlock()
	p, _ := a.newProject("Glovebox", "", "")
	ag := &Agent{ID: "a1", Name: "atlas", ProjectID: p.ID, Cwd: p.Folder, Mission: "Build the parser"}
	a.agents["a1"], a.order = ag, []string{"a1"}

	if _, isErr := a.libraryTool(ag, "remember", map[string]any{"text": "We use Vitest, not Jest", "kind": "decision"}); isErr {
		t.Fatal("remember")
	}
	a.libraryTool(ag, "remember", map[string]any{"text": "We use Vitest, not Jest"}) // duplicate
	if len(p.Memory) != 1 || len(a.memory) != 0 {
		t.Fatal("memory went to the wrong place", len(p.Memory), len(a.memory))
	}
	a.libraryTool(ag, "save_skill", map[string]any{"name": "Deploy Vite to Netlify", "description": "When a Vite app must go online", "steps": "1. npm run build\n2. deploy dist"})
	out, isErr := a.libraryTool(ag, "use_skill", map[string]any{"name": "deploy vite to netlify"})
	if isErr || !strings.Contains(out, "npm run build") || a.skills[0].Uses != 1 {
		t.Fatal("skill", out)
	}
	mt := a.memoryText(ag)
	if !strings.Contains(mt, "(decision) We use Vitest") || !strings.Contains(mt, "Deploy Vite to Netlify: When a Vite app") {
		t.Fatal(mt)
	}

	// review only starts when files changed
	ag.Summary = "Parser done"
	if a.startReview(ag) {
		t.Fatal("review without files")
	}
	ag.Files = []string{filepath.Join(p.Folder, "src/parser.ts")}
	if !a.startReview(ag) {
		t.Fatal("no review")
	}
	var rv *Agent
	for _, x := range a.agents {
		if x.ReviewOf == "a1" {
			rv = x
		}
	}
	if rv == nil || rv.ProjectID != p.ID || !strings.Contains(rv.Mission, "src/parser.ts") || a.startReview(rv) {
		t.Fatal("reviewer wrong", rv)
	}
	a.cfg.ReviewMode = "off"
	if a.startReview(ag) {
		t.Fatal("review while off")
	}

	a.spend(ag, 0.25, 1000)
	a.spend(ag, 0.5, 2000)
	days := a.ledgerView()["days"].([]map[string]any)
	today := days[len(days)-1]["projects"].(map[string]*Spend)[p.ID]
	if today == nil || today.Turns != 2 || today.Tokens != 3000 || today.Cost != 0.75 {
		t.Fatal("ledger", today)
	}
}

func TestTranscribePipeline(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("no ffmpeg")
	}
	a := newLibApp(t)
	_ = a
	d := voiceDir()
	_ = os.MkdirAll(filepath.Join(d, "whisper"), 0o755)
	// a stand-in for whisper-cli that checks it gets a 16 kHz wav
	fake := "#!/bin/sh\nwhile [ $# -gt 0 ]; do [ \"$1\" = -f ] && f=$2; shift; done\nffprobe -v error -show_entries stream=sample_rate -of csv=p=0 \"$f\" | grep -q 16000 || exit 3\necho 'whisper_init: loading'\necho ' Maak een website voor mijn bonnetjes app.'\n"
	_ = os.WriteFile(filepath.Join(d, "whisper", "whisper-cli"), []byte(fake), 0o755)
	_ = os.WriteFile(filepath.Join(d, "ggml-base.bin"), []byte("x"), 0o644)
	in := filepath.Join(t.TempDir(), "voice.oga")
	if out, err := exec.Command("ffmpeg", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=300:duration=1", "-c:a", "libopus", "-f", "ogg", in).CombinedOutput(); err != nil {
		t.Skip("ffmpeg cannot make opus: " + string(out))
	}
	text, err := transcribe(in)
	if err != nil || text != "Maak een website voor mijn bonnetjes app." {
		t.Fatalf("%q %v", text, err)
	}
}
