package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type Config struct {
	Lang           string          `json:"lang"`
	SetupDone      bool            `json:"setupDone"`
	BotToken       string          `json:"botToken"`
	ChatID         string          `json:"chatId"`
	TgUpdates      bool            `json:"tgUpdates"`
	TgSilent       bool            `json:"tgSilent"`
	TgDecisions    bool            `json:"tgDecisions"`
	TgCritical     bool            `json:"tgCritical"`
	TgChatter      bool            `json:"tgChatter"`
	ClaudePath     string          `json:"claudePath"`
	Workspace      string          `json:"workspace"`
	PermissionMode string          `json:"permissionMode"` // ask | edits | full
	PermVersion    int             `json:"permVersion"`
	GitHubToken    string          `json:"githubToken"`
	AllowRules     []string        `json:"allowRules"` // "Always allow" answers, e.g. "PowerShell: npm install"
	TgNoImages     bool            `json:"tgNoImages"`
	DefaultModel   string          `json:"defaultModel"`
	MaxAgents      int             `json:"maxAgents"`
	MaxRunning     int             `json:"maxRunning"`
	MaxDepth       int             `json:"maxDepth"`
	MaxTurns       int             `json:"maxTurns"`
	TokenBudget    int64           `json:"tokenBudget"` // 0 = auto (busiest previous block)
	Theme          string          `json:"theme"`       // light | dark
	AutoUpdate     bool            `json:"autoUpdate"`
	ReviewMode     string          `json:"reviewMode"` // off | projects | all
	NetlifyToken   string          `json:"netlifyToken"`
	Accent         string          `json:"accent"`
	Layout         json.RawMessage `json:"layout,omitempty"`
	UpdateRepo     string          `json:"updateRepo"`
	Connectors     []Connector     `json:"connectors"`
	Port           int             `json:"port"`
}

func configDir() string {
	if d := os.Getenv("AGENTHQ_HOME"); d != "" {
		return d
	}
	if d, err := os.UserConfigDir(); err == nil {
		return filepath.Join(d, appName)
	}
	return "."
}

func defaultConfig() Config {
	h, _ := os.UserHomeDir()
	return Config{
		Lang: "en", TgUpdates: true, TgSilent: true, TgDecisions: true, TgCritical: true,
		Workspace: filepath.Join(h, "AgentHQ"), PermissionMode: "full", PermVersion: 2,
		ReviewMode: "all", Theme: "dark", Accent: "orange", AutoUpdate: true, UpdateRepo: defaultUpdateRepo, MaxAgents: 10, MaxRunning: 3, MaxDepth: 3, MaxTurns: 25, Port: 7777,
	}
}

func loadConfig() Config {
	c := defaultConfig()
	if b, err := os.ReadFile(filepath.Join(configDir(), "config.json")); err == nil {
		_ = json.Unmarshal(b, &c)
	}
	d := defaultConfig()
	if c.Port == 0 {
		c.Port = d.Port
	}
	if c.MaxAgents <= 0 {
		c.MaxAgents = d.MaxAgents
	}
	if c.MaxRunning <= 0 {
		c.MaxRunning = d.MaxRunning
	}
	if c.MaxDepth <= 0 {
		c.MaxDepth = d.MaxDepth
	}
	if c.MaxTurns <= 0 {
		c.MaxTurns = d.MaxTurns
	}
	if c.Workspace == "" {
		c.Workspace = d.Workspace
	}
	if c.PermissionMode == "" {
		c.PermissionMode = d.PermissionMode
	}
	if c.Lang == "" {
		c.Lang = "en"
	}
	if c.ReviewMode == "" {
		c.ReviewMode = "all"
	}
	if c.UpdateRepo == "" || c.UpdateRepo == "mikeey77/agent-hq" { // the repo is called Agenthq
		c.UpdateRepo = defaultUpdateRepo
	}
	if c.PermVersion < 2 { // v2.3: agents act on their own and ask only when it matters
		c.PermissionMode, c.PermVersion = "full", 2
	}
	return c
}

func saveConfig(c Config) error {
	_ = os.MkdirAll(configDir(), 0o755)
	b, _ := json.MarshalIndent(c, "", "  ")
	return os.WriteFile(filepath.Join(configDir(), "config.json"), b, 0o600)
}
