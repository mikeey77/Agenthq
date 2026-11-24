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
	resp, err := httpClient.Post("https://api.telegram.org/bot"+token+"/"+method, "application/json", bytes.NewReader(b))
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

func (a *App) decisionText(d *Decision) string {
	var b strings.Builder
	fmt.Fprintf(&b, "⚠️ %s — %s\n\n%s", a.T("decision"), d.AgentName, d.Question)
	if d.Detail != "" {
		fmt.Fprintf(&b, "\n\n%s", d.Detail)
	}
	return b.String()
}

func (a *App) tgSendDecision(d *Decision) {
	a.mu.Lock()
	text := a.decisionText(d) + "\n\n" + a.T("decision_hint")
	var rows [][]map[string]string
	var row []map[string]string
	for i, o := range d.Options {
		row = append(row, map[string]string{"text": truncate(o, 40), "callback_data": "d:" + d.ID + ":" + strconv.Itoa(i)})
		if len(row) == 2 {
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
		text += "\n\n✅ " + a.T("answered", d.Answer)
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
				}
				note = ag.Name + ": " + ag.Status
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
					case "👍", "❤", "🔥", "👌", "💯":
						idx = 0
					case "👎", "💩", "🤬":
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
	msg, _ := um["message"].(map[string]any)
	if msg == nil {
		return
	}
	chat, _ := msg["chat"].(map[string]any)
	chatID := str(chat, "id")
	text := strings.TrimSpace(str(msg, "text"))
	cmd := strings.ToLower(strings.SplitN(strings.SplitN(text, " ", 2)[0], "@", 2)[0])
	a.mu.Lock()
	linked := a.cfg.ChatID
	if linked == "" && cmd == "/start" {
		a.cfg.ChatID = chatID
		_ = saveConfig(a.cfg)
		linked = chatID
		a.emit("info", "", "Telegram linked")
	}
	a.mu.Unlock()
	if chatID != linked {
		_, _ = a.tgSendTo(chatID, a.T("tg_other_chat"), false, nil)
		return
	}
	// reply to a decision message = custom answer
	if rt, ok := msg["reply_to_message"].(map[string]any); ok {
		mid, _ := rt["message_id"].(float64)
		a.mu.Lock()
		did, isDec := a.tgMsgs[int64(mid)]
		var err error
		if isDec {
			err = a.answerDecision(did, text, "telegram")
		}
		a.mu.Unlock()
		if isDec {
			if err != nil {
				_, _ = a.tgSendTo(chatID, "❌ "+err.Error(), true, nil)
			} else {
				_, _ = a.tgSendTo(chatID, "✅ "+a.T("answered", text), true, nil)
			}
			return
		}
	}
	rest := ""
	if parts := strings.SplitN(text, " ", 2); len(parts) == 2 {
		rest = strings.TrimSpace(parts[1])
	}
	var out []string
	switch cmd {
	case "/start", "/help":
		out = []string{a.T("tg_linked")}
	case "/status":
		out = []string{a.statusText()}
	case "/agents", "/list":
		out = a.runCommand("list")
	case "/usage":
		out = a.runCommand("usage")
	case "/mission", "/m":
		out = a.runCommand("mission " + rest)
	case "/msg":
		out = a.runCommand("@" + rest)
	case "/pause", "/resume", "/stop":
		out = a.runCommand(strings.TrimPrefix(cmd, "/") + " " + rest)
	default:
		if strings.HasPrefix(text, "/") {
			out = []string{a.T("tg_unknown")}
		} else if text != "" {
			out = a.runCommand(text) // plain text = new mission or @agent message
		}
	}
	if len(out) > 0 {
		_, _ = a.tgSendTo(chatID, strings.Join(out, "\n"), false, nil)
	}
}
