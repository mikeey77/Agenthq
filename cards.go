package main

// Telegram image cards: small dark status cards rendered with the same headless browser
// that powers the in-app browser. If that browser is unavailable the text is sent instead.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"mime/multipart"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/page"
	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

var cardMu sync.Mutex // one card at a time

var fontCSS = sync.OnceValue(func() string {
	var b strings.Builder
	for _, f := range [][2]string{{"Inter", "inter-latin-wght-normal.woff2"}, {"Mono", "jetbrains-mono-latin-wght-normal.woff2"}} {
		data, err := fontFS.ReadFile("fonts/" + f[1])
		if err == nil {
			fmt.Fprintf(&b, `@font-face{font-family:%q;font-weight:100 900;src:url(data:font/woff2;base64,%s) format("woff2")}`, f[0], base64.StdEncoding.EncodeToString(data))
		}
	}
	return b.String()
})

// RenderHTML renders a self-contained HTML snippet to a PNG at 2x.
func (b *Browser) RenderHTML(doc string, width int) ([]byte, error) {
	cardMu.Lock()
	defer cardMu.Unlock()
	b.mu.Lock()
	err := b.start()
	root := b.root
	b.mu.Unlock()
	if err != nil {
		return nil, err
	}
	ctx, cancel := chromedp.NewContext(root)
	defer cancel()
	ctx, cancel2 := context.WithTimeout(ctx, 20*time.Second)
	defer cancel2()
	var h float64
	var buf []byte
	err = chromedp.Run(ctx,
		emulation.SetDeviceMetricsOverride(int64(width), 400, 2, false),
		chromedp.Navigate("about:blank"),
		chromedp.ActionFunc(func(c context.Context) error {
			tree, err := page.GetFrameTree().Do(c)
			if err != nil {
				return err
			}
			return page.SetDocumentContent(tree.Frame.ID, doc).Do(c)
		}),
		chromedp.Sleep(250*time.Millisecond),
		chromedp.Evaluate(`document.fonts.ready.then(()=>document.querySelector('.card').getBoundingClientRect().height)`, &h, func(p *cdpruntime.EvaluateParams) *cdpruntime.EvaluateParams { return p.WithAwaitPromise(true) }),
		chromedp.ActionFunc(func(c context.Context) error {
			if err := emulation.SetDeviceMetricsOverride(int64(width), int64(h), 2, false).Do(c); err != nil {
				return err
			}
			var err error
			buf, err = page.CaptureScreenshot().WithFormat(page.CaptureScreenshotFormatPng).Do(c)
			return err
		}),
	)
	return buf, err
}

const cardCSS = `*{box-sizing:border-box;margin:0}body{background:#07080b;font-family:Inter,sans-serif;color:#e9edf3}
.card{width:100%;padding:26px 28px 24px;background:radial-gradient(120% 90% at 100% 0,rgba(255,138,76,.16),transparent 55%),#0e1117;border-top:3px solid var(--k,#ff8a4c)}
.top{display:flex;align-items:center;gap:12px;font:600 12px Mono,monospace;letter-spacing:.12em;color:#ff8a4c}
.top span{margin-left:auto;color:#7d8697;letter-spacing:0;font-weight:500}
h1{font-size:25px;line-height:1.2;margin:14px 0 6px;font-weight:700;letter-spacing:-.01em}
.sub{color:#9aa3b2;font-size:15px;line-height:1.45}
.sum{margin-top:16px;padding:14px 16px;border-radius:12px;background:#151922;font-size:15px;line-height:1.5;color:#d5dbe5;white-space:pre-wrap}
.team{display:flex;flex-wrap:wrap;gap:8px;margin-top:16px}
.m{display:flex;align-items:center;gap:7px;padding:5px 10px 5px 5px;border-radius:10px;background:#151922;font-size:13px}
.m svg{width:26px;height:26px}.m small{color:#7d8697;font-size:11.5px}
.stats{display:flex;gap:26px;margin-top:18px;padding-top:16px;border-top:1px solid #1f2430}
.stats div{font:600 20px Mono,monospace}.stats small{display:block;font:500 11px Inter;color:#7d8697;margin-top:2px;letter-spacing:.04em}
.bar{height:10px;border-radius:6px;background:#1c212b;overflow:hidden;margin-top:14px}.bar i{display:block;height:100%;border-radius:6px}
.row{display:grid;grid-template-columns:34px 1fr auto;gap:10px;align-items:center;padding:8px 0;border-bottom:1px solid #191d26;font-size:14px}
.row svg{width:30px;height:30px}.row small{display:block;color:#7d8697;font-size:12px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;max-width:430px}
.st{font:600 11px Mono,monospace;padding:3px 8px;border-radius:6px}
.files{margin-top:12px;font:12.5px Mono,monospace;color:#9aa3b2;line-height:1.7}`

func robotSVG(color string) string {
	return fmt.Sprintf(`<svg viewBox="0 0 40 40"><rect x="12.5" y="25" width="15" height="11" rx="3" fill="%s" stroke="#12151b" stroke-width="1.4"/><line x1="20" y1="6" x2="20" y2="10.5" stroke="#cfd5de" stroke-width="1.5"/><circle cx="20" cy="5" r="2.8" fill="%s"/><rect x="9.5" y="10.5" width="21" height="15" rx="4.5" fill="#eef1ee" stroke="#12151b" stroke-width="1.4"/><rect x="12.5" y="13.5" width="15" height="8.5" rx="2.6" fill="#12151b"/><rect x="15" y="16" width="2.6" height="3.2" rx=".9" fill="#8ff0a8"/><rect x="22.4" y="16" width="2.6" height="3.2" rx=".9" fill="#8ff0a8"/></svg>`, color, color)
}

var statusColor = map[string]string{"running": "#3ecf8e", "waiting": "#f5c451", "queued": "#6aa7ff", "paused": "#9aa3b2", "error": "#ff6b6b", "done": "#3ecf8e", "idle": "#7d8697", "stopped": "#7d8697"}

func esc(s string) string { return html.EscapeString(s) }

func cardDoc(body string) string {
	return `<!doctype html><html><head><meta charset="utf-8"><style>` + fontCSS() + cardCSS + `</style></head><body><div class="card">` + body + `</div></body></html>`
}

func durText(d time.Duration) string {
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()+0.5))
	}
	return fmt.Sprintf("%dh%02d", int(d.Hours()), int(d.Minutes())%60)
}

// doneCard renders a finished mission with its team. Caller holds a.mu.
func (a *App) doneCard(ag *Agent) string {
	team := []*Agent{}
	var cost float64
	tools := 0
	files := []string{}
	var walk func(x *Agent)
	walk = func(x *Agent) {
		team = append(team, x)
		cost += x.Cost
		tools += x.ToolCalls
		for _, f := range x.Files {
			if len(files) < 5 {
				files = append(files, f)
			}
		}
		for _, id := range a.order {
			if c := a.agents[id]; c != nil && c.ParentID == x.ID {
				walk(c)
			}
		}
	}
	walk(ag)
	var tb strings.Builder
	for _, m := range team {
		fmt.Fprintf(&tb, `<div class="m">%s<div>%s<br><small>%s</small></div></div>`, robotSVG(m.Color), esc(m.Name), esc(a.T("k_"+m.Kind)))
	}
	proj := ""
	if p := a.projectByID(ag.ProjectID); p != nil {
		d, t := p.progress()
		proj = fmt.Sprintf(`<div class="sub" style="margin-top:12px">📁 %s · %d/%d %s</div>`, esc(p.Name), d, t, esc(a.T("card_tasks")))
	}
	fl := ""
	if len(files) > 0 {
		short := make([]string, len(files))
		for i, f := range files {
			short[i] = "✎ " + esc(baseName(f))
		}
		fl = `<div class="files">` + strings.Join(short, "<br>") + `</div>`
	}
	return cardDoc(fmt.Sprintf(`<div class="top">AGENT HQ <span>%s</span></div><h1>✅ %s</h1><div class="sub">%s</div><div class="sum">%s</div>%s%s<div class="team">%s</div>
<div class="stats"><div>%s<small>%s</small></div><div>%d<small>%s</small></div><div>%d<small>%s</small></div><div>$%.2f<small>%s</small></div></div>`,
		time.Now().Format("15:04"), esc(a.T("card_done")), esc(truncate(ag.Mission, 180)), esc(truncate(ag.Summary, 700)), proj, fl, tb.String(),
		durText(time.Since(ag.Created)), esc(a.T("card_time")), len(team), esc(a.T("card_agents")), tools, esc(a.T("card_steps")), cost, esc(a.T("card_cost"))))
}

// statusCard renders all live agents and usage. Caller holds a.mu.
func (a *App) statusCard() string {
	var rows strings.Builder
	n := 0
	for _, id := range a.order {
		x := a.agents[id]
		if x == nil || x.Status == "stopped" || n >= 12 {
			continue
		}
		n++
		c := statusColor[x.Status]
		fmt.Fprintf(&rows, `<div class="row">%s<div>%s<small>%s</small></div><span class="st" style="color:%s;background:%s22">%s</span></div>`,
			robotSVG(x.Color), esc(x.Name), esc(truncate(x.Activity, 80)), c, c, esc(strings.ToUpper(a.T("s_"+x.Status))))
	}
	if n == 0 {
		rows.WriteString(`<div class="sub">` + esc(a.T("card_none")) + `</div>`)
	}
	u := a.usage.Summary(a.cfg.TokenBudget)
	bar := ""
	if u.Ready && u.BlockActive && u.Percent >= 0 {
		col := "#3ecf8e"
		if u.Percent >= 90 {
			col = "#ff6b6b"
		} else if u.Percent >= 60 {
			col = "#f5c451"
		}
		bar = fmt.Sprintf(`<div class="bar"><i style="width:%d%%;background:%s"></i></div><div class="sub" style="margin-top:8px">%d%% %s · %s %s</div>`,
			min(u.Percent, 100), col, u.Percent, esc(a.T("card_used")), esc(a.T("card_reset")), u.ResetAt.Format("15:04"))
	}
	if a.limited() {
		bar = fmt.Sprintf(`<div class="sub" style="margin-top:12px;color:#f5c451">⏳ %s</div>`, esc(a.T("limit_wait", a.limitUntil.Format("15:04"))))
	}
	return cardDoc(fmt.Sprintf(`<div class="top">AGENT HQ <span>%s</span></div><h1>%s</h1>%s<div style="margin-top:10px">%s</div>`,
		time.Now().Format("15:04"), esc(a.T("card_status")), bar, rows.String()))
}

// limitCard renders the "usage limit reached" message. Caller holds a.mu.
func (a *App) limitCard() string {
	left := time.Until(a.limitUntil)
	return cardDoc(fmt.Sprintf(`<div class="top" style="color:#f5c451">AGENT HQ <span>%s</span></div><h1>⏳ %s</h1><div class="sub">%s</div>
<div class="stats"><div>%s<small>%s</small></div><div>%s<small>%s</small></div></div>`,
		time.Now().Format("15:04"), esc(a.T("card_limit")), esc(a.T("card_limit_d")), a.limitUntil.Format("15:04"), esc(a.T("card_reset")), fmtLeft(left), esc(a.T("card_left"))))
}

func baseName(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	return p[strings.LastIndex(p, "/")+1:]
}

// tgSendPhoto sends an image with a caption (and optional buttons).
func (a *App) tgSendPhoto(img []byte, caption string, silent bool, kb any) (int64, error) {
	a.mu.Lock()
	token, chat := a.cfg.BotToken, a.cfg.ChatID
	a.mu.Unlock()
	if token == "" || chat == "" {
		return 0, fmt.Errorf("telegram not linked")
	}
	if r := []rune(caption); len(r) > 1000 {
		caption = string(r[:1000]) + "…"
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("chat_id", chat)
	_ = mw.WriteField("caption", caption)
	if silent {
		_ = mw.WriteField("disable_notification", "true")
	}
	if kb != nil {
		b, _ := json.Marshal(kb)
		_ = mw.WriteField("reply_markup", string(b))
	}
	fw, _ := mw.CreateFormFile("photo", "agenthq.png")
	_, _ = fw.Write(img)
	_ = mw.Close()
	resp, err := httpClient.Post("https://api.telegram.org/bot"+token+"/sendPhoto", mw.FormDataContentType(), &body)
	if err != nil {
		a.setTgErr(err)
		return 0, err
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if ok, _ := out["ok"].(bool); !ok {
		err := fmt.Errorf("telegram: %v", out["description"])
		a.setTgErr(err)
		return 0, err
	}
	a.setTgErr(nil)
	res, _ := out["result"].(map[string]any)
	id, _ := res["message_id"].(float64)
	return int64(id), nil
}

// tgCard renders a card and sends it, falling back to plain text.
func (a *App) tgCard(doc, caption string, silent bool, kb any) {
	a.mu.Lock()
	images := !a.cfg.TgNoImages
	a.mu.Unlock()
	if images {
		if img, err := a.browser.RenderHTML(doc, 560); err == nil && len(img) > 0 {
			if _, err := a.tgSendPhoto(img, caption, silent, kb); err == nil {
				return
			}
		}
	}
	a.tgSendText(caption, silent, kb)
}
