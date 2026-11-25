package main

// Fewer permission questions: read-only tools and commands are allowed without asking,
// and "Always allow" remembers a command (e.g. "npm install") or a tool for good.

import (
	"regexp"
	"strings"
)

// Tools that only read or plan never need a question.
var safeTools = map[string]bool{"Read": true, "Glob": true, "Grep": true, "LS": true, "WebSearch": true, "WebFetch": true,
	"TodoWrite": true, "TodoRead": true, "NotebookRead": true, "ExitPlanMode": true, "BashOutput": true, "Task": true}

// Commands that only look at things. Checked per part of a pipeline / chain.
var safeCmds = map[string]bool{
	// PowerShell
	"get-childitem": true, "get-content": true, "get-location": true, "get-item": true, "get-itemproperty": true, "test-path": true,
	"select-string": true, "select-object": true, "where-object": true, "sort-object": true, "measure-object": true, "format-table": true,
	"format-list": true, "out-string": true, "resolve-path": true, "split-path": true, "join-path": true, "get-command": true, "get-process": true,
	"get-date": true, "get-help": true, "write-output": true, "write-host": true, "get-filehash": true, "convertfrom-json": true, "convertto-json": true,
	"foreach-object": true, "get-psdrive": true, "get-computerinfo": true, "get-netipaddress": true,
	// both / unix / cmd
	"ls": true, "dir": true, "cat": true, "type": true, "pwd": true, "cd": true, "echo": true, "where": true, "which": true, "head": true, "tail": true,
	"wc": true, "grep": true, "findstr": true, "find": true, "tree": true, "sort": true, "uniq": true, "file": true, "stat": true, "du": true, "df": true,
	"whoami": true, "hostname": true, "date": true, "env": true, "printenv": true, "ver": true, "systeminfo": true, "ipconfig": true,
}

// Commands that are safe with certain first arguments.
var safeSub = map[string][]string{
	"git":    {"status", "log", "diff", "show", "branch", "remote", "rev-parse", "ls-files", "blame", "config --get", "tag"},
	"npm":    {"-v", "--version", "ls", "list", "view", "outdated", "test", "run test", "run lint", "run build", "run typecheck", "audit"},
	"node":   {"-v", "--version", "-e"},
	"npx":    {"tsc", "vitest", "jest", "eslint", "prettier --check"},
	"python": {"--version", "-v", "-m pytest", "-m unittest"},
	"py":     {"--version", "-m pytest"},
	"go":     {"version", "test", "vet", "build", "env", "list"},
	"cargo":  {"check", "test", "build", "--version"},
	"dotnet": {"--version", "build", "test"},
	"claude": {"--version", "mcp list"},
}

var reSplit = regexp.MustCompile(`\|\||&&|[|;\n]`)
var risky = regexp.MustCompile(`(?i)(>|remove-item|\brm\b|\bdel\b|rmdir|\brd\b|format-|set-content|out-file|move-item|rename-item|new-item|copy-item|invoke-webrequest|invoke-restmethod|iwr|irm|curl|wget|start-process|invoke-expression|iex|reg |set-executionpolicy|stop-process|kill|shutdown|restart-computer)`)

// commandIsSafe reports whether every part of a shell command only reads.
func commandIsSafe(cmd string) bool {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" || risky.MatchString(cmd) {
		return false
	}
	for _, part := range reSplit.Split(cmd, -1) {
		f := strings.Fields(strings.TrimSpace(part))
		if len(f) == 0 {
			continue
		}
		name := strings.ToLower(strings.TrimSuffix(strings.TrimSuffix(f[0], ".exe"), ".cmd"))
		name = strings.TrimPrefix(name, "&")
		if safeCmds[name] {
			continue
		}
		subs, ok := safeSub[name]
		if !ok {
			return false
		}
		rest := strings.ToLower(strings.Join(f[1:], " "))
		hit := false
		for _, s := range subs {
			if rest == s || strings.HasPrefix(rest, s+" ") {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	return true
}

// ruleFor turns a request into what "Always allow" remembers:
// "PowerShell: npm install" for commands (first two words), or just the tool name.
func ruleFor(tool string, in map[string]any) string {
	if c := strings.TrimSpace(str(in, "command")); c != "" {
		f := strings.Fields(strings.ToLower(c))
		if len(f) > 2 {
			f = f[:2]
		}
		if len(f) == 2 && strings.HasPrefix(f[1], "-") == false && strings.ContainsAny(f[1], `/\.:"'`) {
			f = f[:1] // second word is a path or value, not a subcommand
		}
		return tool + ": " + strings.Join(f, " ")
	}
	return tool
}

// autoAllowed reports whether a permission request needs no question. Caller holds a.mu.
func (a *App) autoAllowed(ag *Agent, tool string, in map[string]any) bool {
	if safeTools[tool] || ag.TrustAll {
		return true
	}
	if c := str(in, "command"); c != "" && (tool == "Bash" || tool == "PowerShell") && commandIsSafe(c) {
		return true
	}
	rule := ruleFor(tool, in)
	for _, r := range a.cfg.AllowRules {
		if strings.EqualFold(r, rule) || strings.EqualFold(r, tool) {
			return true
		}
		// a remembered command also covers longer forms: "npm install" covers "npm install react"
		if strings.Contains(r, ": ") && strings.HasPrefix(strings.ToLower(tool+": "+strings.TrimSpace(str(in, "command"))), strings.ToLower(r)) {
			return true
		}
	}
	return false
}

// permOptions are the answers of a permission question.
func (a *App) permOptions() []string {
	return []string{a.T("allow"), a.T("allow_always"), a.T("allow_agent"), a.T("deny")}
}
