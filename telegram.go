package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

var httpClient = &http.Client{Timeout: 45 * time.Second}

type TelegramState struct {
	Configured bool   `json:"configured"`
	Linked     bool   `json:"linked"`
	BotName    string `json:"botName"`
	LastError  string `json:"lastError"`
}

func (a *App) tgCall(method string, payload map[string]any) (map[string]any, error) {
	a.mu.Lock()
	token := a.cfg.BotToken
	a.mu.Unlock()
	if token == "" {
		return nil, errors.New("no bot token")
	}
	b, _ := json.Marshal(payload)
	resp, err := httpClient.Post(tgAPI+"/bot"+token+"/"+method, "application/json", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if ok, _ := out["ok"].(bool); !ok {
		return out, fmt.Errorf("telegram: %v", out["description"])
	}
	return out, nil
}

func (a *App) setTgErr(err error) {
	a.mu.Lock()
	if err != nil {
		a.tg.LastError = err.Error()
	} else {
		a.tg.LastError = ""
	}
	a.mu.Unlock()
}

func (a *App) chatID() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg.ChatID
}

func (a *App) tgSendTo(chat, text string, silent bool, kb any) (int64, error) {
	if len([]rune(text)) > 3900 {
		text = string([]rune(text)[:3900]) + "…"
	}
	p := map[string]any{"chat_id": chat, "text": text, "disable_web_page_preview": true, "disable_notification": silent}
	if kb != nil {
		p["reply_markup"] = kb
	}
	out, err := a.tgCall("sendMessage", p)
	a.setTgErr(err)
	if err != nil {
		return 0, err
	}
	res, _ := out["result"].(map[string]any)
	id, _ := res["message_id"].(float64)
	return int64(id), nil
}

func (a *App) tgSendText(text string, silent bool, kb any) {
	if chat := a.chatID(); chat != "" {
		_, _ = a.tgSendTo(chat, text, silent, kb)
	}
}

var tgAPI = "https://api.telegram.org"

var numEmoji = []string{"1️⃣", "2️⃣", "3️⃣", "4️⃣", "5️⃣", "6️⃣", "7️⃣", "8️⃣", "9️⃣", "🔟"}

var yesWords = []string{"yes", "ja", "ok", "allow", "toestaan", "approve", "akkoord", "doorgaan", "go", "proceed", "continue", "ship", "do it", "doe maar", "sure", "prima"}
var noWords = []string{"no", "nee", "deny", "weiger", "cancel", "annuleer", "stop", "don't", "niet", "skip", "make it private", "maak privé", "later"}

func hasWord(s string, words []string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	for _, w := range words {
		if s == w || strings.HasPrefix(s, w+" ") || strings.HasPrefix(s, w+",") || strings.HasPrefix(s, w+".") || strings.HasPrefix(s, w+"!") || strings.HasPrefix(s, w+":") {
			return true
		}
	}
	return false
}

// yesNo reports whether a decision is a plain yes/no question, answered with ✅ / ❌.
func yesNo(opts []string) bool {
	return len(opts) == 2 && hasWord(opts[0], yesWords) && hasWord(opts[1], noWords)
}

func optionEmojis(opts []string) []string {
	if yesNo(opts) {
		return []string{"✅", "❌"}
	}
	return numEmoji[:min(len(opts), len(numEmoji))]
}

// matchOption turns an emoji, number or yes/no word into the option it picks.
func matchOption(opts []string, text string) (int, bool) {
	t := strings.TrimSpace(strings.NewReplacer("\uFE0F", "", "\u20E3", "").Replace(text))
	if t == "🔟" {
		t = "10"
	}
	if n, err := strconv.Atoi(t); err == nil && n >= 1 && n <= len(opts) {
		return n - 1, true
	}
	if len(opts) == 2 {
		switch strings.ToLower(t) {
		case "✅", "✔", "👍", "👌", "y", "j", "yes", "ja", "ok", "oké":
			return 0, true
		case "❌", "✖", "👎", "n", "no", "nee":
			return 1, true
		}
	}
	for i, o := range opts {
		if strings.EqualFold(t, o) {
			return i, true
		}
	}
	return 0, false
}

func (a *App) decisionText(d *Decision) string {
	var b strings.Builder
	fmt.Fprintf(&b, "⚠️ %s — %s\n\n%s", a.T("decision"), d.AgentName, d.Question)
	if d.Detail != "" {
		fmt.Fprintf(&b, "\n\n%s", d.Detail)
	}
	if len(d.Options) > 0 {
		b.WriteString("\n")
		em := optionEmojis(d.Options)
		for i, o := range d.Options {
			if i < len(em) {
				fmt.Fprintf(&b, "\n%s %s", em[i], o)
			}
		}
	}
	return b.String()
}

func (a *App) tgSendDecision(d *Decision) {
	a.mu.Lock()
	hint := a.T("decision_hint_num")
	if yesNo(d.Options) {
		hint = a.T("decision_hint_yn")
	}
	text := a.decisionText(d) + "\n\n" + hint
	em := optionEmojis(d.Options)
	var rows [][]map[string]string
	var row []map[string]string
	perRow := 2
	if !yesNo(d.Options) && len(d.Options) > 2 {
		perRow = 1
	}
	for i, o := range d.Options {
		if i >= len(em) {
			break
		}
		label := em[i] + " " + truncate(o, 36)
		row = append(row, map[string]string{"text": label, "callback_data": "d:" + d.ID + ":" + strconv.Itoa(i)})
		if len(row) == perRow {
			rows = append(rows, row)
			row = nil
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	chat := a.cfg.ChatID
	a.mu.Unlock()
	id, err := a.tgSendTo(chat, text, false, map[string]any{"inline_keyboard": rows})
	if err != nil {
		return
	}
	a.mu.Lock()
	d.tgMsgID = id
	a.tgMsgs[id] = d.ID
	a.mu.Unlock()
}

func (a *App) tgMarkAnswered(d *Decision) {
	a.mu.Lock()
	text := a.decisionText(d)
	if d.Status == "answered" {
		mark := "👉"
		if i, ok := matchOption(d.Options, d.Answer); ok && i < len(optionEmojis(d.Options)) {
			mark = optionEmojis(d.Options)[i]
		}
		text += "\n\n" + mark + " " + a.T("answered", d.Answer)
	} else {
		text += "\n\n⌛ —"
	}
	chat, id := a.cfg.ChatID, d.tgMsgID
	a.mu.Unlock()
	_, _ = a.tgCall("editMessageText", map[string]any{"chat_id": chat, "message_id": id, "text": text})
}

func (a *App) telegramLoop() {
	offset := 0
	lastToken := ""
	for {
		a.mu.Lock()
		token := a.cfg.BotToken
		a.mu.Unlock()
		if token == "" || a.demo {
			time.Sleep(3 * time.Second)
			continue
		}
		if token != lastToken {
			me, err := a.tgCall("getMe", map[string]any{})
			if err != nil {
				a.setTgErr(err)
				time.Sleep(10 * time.Second)
				continue
			}
			lastToken, offset = token, 0
			r, _ := me["result"].(map[string]any)
			a.mu.Lock()
			a.tg.BotName = str(r, "username")
			a.mu.Unlock()
			a.setTgErr(nil)
			go a.tgSetCommands()
		}
		res, err := a.tgCall("getUpdates", map[string]any{"offset": offset, "timeout": 25,
			"allowed_updates": []string{"message", "callback_query", "message_reaction"}})
		if err != nil {
			a.setTgErr(err)
			time.Sleep(5 * time.Second)
			continue
		}
		ups, _ := res["result"].([]any)
		for _, u := range ups {
			um, _ := u.(map[string]any)
			if id, ok := um["update_id"].(float64); ok {
				offset = int(id) + 1
			}
			a.handleUpdate(um)
		}
	}
}

func (a *App) handleUpdate(um map[string]any) {
	if cq, ok := um["callback_query"].(map[string]any); ok {
		msg, _ := cq["message"].(map[string]any)
		chat, _ := msg["chat"].(map[string]any)
		if str(chat, "id") != a.chatID() {
			return
		}
		data := strings.Split(str(cq, "data"), ":")
		note := "✓"
		a.mu.Lock()
		if len(data) == 3 && data[0] == "d" {
			for _, d := range a.decisions {
				if d.ID == data[1] {
					i, _ := strconv.Atoi(data[2])
					if i >= 0 && i < len(d.Options) {
						if err := a.answerDecision(d.ID, d.Options[i], "telegram"); err != nil {
							note = err.Error()
						} else {
							note = d.Options[i]
						}
					}
				}
			}
		} else if len(data) == 3 && data[0] == "a" {
			if ag := a.agents[data[1]]; ag != nil {
				switch data[2] {
				case "pause":
					a.pauseAgent(ag)
				case "stop":
					a.stopAgent(ag)
				case "resume":
					a.resumeAgent(ag)
				case "talk":
					a.tgFocus = ag.ID
				case "log":
					go a.tgSendText(a.agentLogText(ag), true, nil)
				}
				note = ag.Name + ": " + a.T("s_"+ag.Status)
				if data[2] == "talk" {
					note = a.T("tg_talking", ag.Name)
					go a.tgSendText("💬 "+a.T("tg_talking", ag.Name)+"\n"+a.T("tg_talk_hint"), true, nil)
				}
			}
		}
		a.mu.Unlock()
		_, _ = a.tgCall("answerCallbackQuery", map[string]any{"callback_query_id": str(cq, "id"), "text": note})
		return
	}
	if re, ok := um["message_reaction"].(map[string]any); ok {
		chat, _ := re["chat"].(map[string]any)
		if str(chat, "id") != a.chatID() {
			return
		}
		mid, _ := re["message_id"].(float64)
		nr, _ := re["new_reaction"].([]any)
		if len(nr) == 0 {
			return
		}
		r0, _ := nr[0].(map[string]any)
		emoji := str(r0, "emoji")
		a.mu.Lock()
		if did, ok := a.tgMsgs[int64(mid)]; ok {
			for _, d := range a.decisions {
				if d.ID == did && len(d.Options) > 0 {
					idx := -1
					switch emoji {
					case "👍", "❤", "🔥", "👌", "💯", "🎉", "🤝", "⚡", "🏆":
						idx = 0
					case "👎", "💩", "🤬", "🙈", "😡":
						idx = 1
					}
					if idx >= 0 && idx < len(d.Options) {
						_ = a.answerDecision(d.ID, d.Options[idx], "telegram")
					}
				}
			}
		}
		a.mu.Unlock()
		return
	}
	a.handleMessage(um)
}
