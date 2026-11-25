package main

import "testing"

func TestAutoAllow(t *testing.T) {
	safe := []string{"Get-ChildItem -Path C:\\x -Recurse | Select-Object Name", "git status", "npm test", "dir", "Test-Path .\\package.json", "npm run build && npm test", "cat package.json | grep vite"}
	unsafe := []string{"Remove-Item -Recurse dist", "npm install", "git push", "echo hi > a.txt", "Invoke-WebRequest http://x", "rm -rf /", "node server.js", "Set-Content a.txt x", "git status; Remove-Item x"}
	for _, c := range safe {
		if !commandIsSafe(c) {
			t.Errorf("should be safe: %s", c)
		}
	}
	for _, c := range unsafe {
		if commandIsSafe(c) {
			t.Errorf("should ask: %s", c)
		}
	}
	a := &App{cfg: defaultConfig()}
	ag := &Agent{}
	in := map[string]any{"command": "npm install react-router"}
	if a.autoAllowed(ag, "PowerShell", in) {
		t.Fatal("asked nothing yet")
	}
	r := ruleFor("PowerShell", in)
	if r != "PowerShell: npm install" {
		t.Fatal(r)
	}
	a.cfg.AllowRules = []string{r}
	if !a.autoAllowed(ag, "PowerShell", map[string]any{"command": "npm install zod"}) || a.autoAllowed(ag, "PowerShell", map[string]any{"command": "npm uninstall zod"}) {
		t.Fatal("rule matching")
	}
	if ruleFor("PowerShell", map[string]any{"command": "python C:\\x\\run.py"}) != "PowerShell: python" {
		t.Fatal(ruleFor("PowerShell", map[string]any{"command": "python C:\\x\\run.py"}))
	}
	if !a.autoAllowed(&Agent{TrustAll: true}, "Write", nil) || !a.autoAllowed(ag, "Read", nil) {
		t.Fatal("trust/safe tools")
	}
	opts := []string{"Allow", "Always allow", "Trust this agent", "Deny"}
	if e := optionEmojis(opts); e[1] != "♾️" {
		t.Fatal(e)
	}
	if i, _ := matchOption(opts, "♾️"); i != 1 {
		t.Fatal(i)
	}
	if i, _ := matchOption(opts, "❌"); i != 3 {
		t.Fatal(i)
	}
}
