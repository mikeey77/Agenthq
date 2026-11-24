package main

// In-app browser: one headless Edge/Chrome instance with a tab per agent. Agents drive it
// through the browser_* MCP tools, and the user watches and takes over in the Browser view.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"
)

const viewW, viewH = 1280, 860

type bTab struct {
	mu      sync.Mutex
	ctx     context.Context
	cancel  context.CancelFunc
	ID      string    `json:"id"`
	Owner   string    `json:"owner"`
	URL     string    `json:"url"`
	Title   string    `json:"title"`
	Busy    bool      `json:"busy"`
	Updated time.Time `json:"updated"`
	shot    []byte
	shotAt  time.Time
}

type Browser struct {
	mu         sync.Mutex
	alloc      context.Context
	stopAlloc  context.CancelFunc
	root       context.Context
	stopRoot   context.CancelFunc
	tabs       map[string]*bTab
	Err        string
	onActivity func(id, text string)
}

func newBrowser() *Browser { return &Browser{tabs: map[string]*bTab{}} }

func findBrowserExe() string {
	var c []string
	if runtime.GOOS == "windows" {
		c = []string{
			os.Getenv("ProgramFiles(x86)") + `\Microsoft\Edge\Application\msedge.exe`,
			os.Getenv("ProgramFiles") + `\Microsoft\Edge\Application\msedge.exe`,
			os.Getenv("ProgramFiles") + `\Google\Chrome\Application\chrome.exe`,
			os.Getenv("LocalAppData") + `\Google\Chrome\Application\chrome.exe`,
		}
	} else {
		c = []string{"/usr/bin/google-chrome", "/usr/bin/chromium", "/usr/bin/chromium-browser", "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"}
	}
	for _, p := range c {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func (b *Browser) start() error {
	if b.root != nil {
		return nil
	}
	exe := findBrowserExe()
	if exe == "" {
		b.Err = "Edge or Chrome not found"
		return errors.New(b.Err)
	}
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(exe),
		chromedp.Flag("headless", "new"),
		chromedp.UserDataDir(filepath.Join(configDir(), "browser")),
		chromedp.WindowSize(viewW, viewH),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.UserAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36 Edg/131.0.0.0"),
	)
	if os.Geteuid() == 0 {
		opts = append(opts, chromedp.NoSandbox)
	}
	b.alloc, b.stopAlloc = chromedp.NewExecAllocator(context.Background(), opts...)
	quiet := chromedp.WithErrorf(func(string, ...any) {})
	b.root, b.stopRoot = chromedp.NewContext(b.alloc, quiet, chromedp.WithLogf(func(string, ...any) {}))
	if err := chromedp.Run(b.root); err != nil { // launches the browser
		b.stopAlloc()
		b.root, b.alloc = nil, nil
		b.Err = err.Error()
		return err
	}
	b.Err = ""
	return nil
}

func (b *Browser) tab(id, owner string) (*bTab, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if t, ok := b.tabs[id]; ok {
		return t, nil
	}
	if err := b.start(); err != nil {
		return nil, err
	}
	ctx, cancel := chromedp.NewContext(b.root)
	t := &bTab{ctx: ctx, cancel: cancel, ID: id, Owner: owner, Updated: time.Now()}
	if err := chromedp.Run(ctx, chromedp.EmulateViewport(viewW, viewH)); err != nil {
		cancel()
		return nil, err
	}
	b.tabs[id] = t
	return t, nil
}

func (b *Browser) get(id string) *bTab {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.tabs[id]
}

func (b *Browser) do(t *bTab, timeout time.Duration, actions ...chromedp.Action) error {
	t.mu.Lock()
	t.Busy = true
	defer func() { t.Busy = false; t.mu.Unlock() }()
	ctx, cancel := context.WithTimeout(t.ctx, timeout)
	defer cancel()
	err := chromedp.Run(ctx, actions...)
	b.snapLocked(t)
	return err
}

// snapLocked refreshes url, title and screenshot. Caller holds t.mu.
func (b *Browser) snapLocked(t *bTab) {
	ctx, cancel := context.WithTimeout(t.ctx, 5*time.Second)
	defer cancel()
	var u, title string
	var buf []byte
	_ = chromedp.Run(ctx, chromedp.Location(&u), chromedp.Title(&title), chromedp.ActionFunc(func(c context.Context) error {
		var err error
		buf, err = page.CaptureScreenshot().WithFormat(page.CaptureScreenshotFormatJpeg).WithQuality(72).Do(c)
		return err
	}))
	if u != "" {
		t.URL, t.Title = u, title
	}
	if len(buf) > 0 {
		t.shot, t.shotAt = buf, time.Now()
	}
	t.Updated = time.Now()
}

func host(u string) string {
	if p, err := url.Parse(u); err == nil && p.Host != "" {
		return strings.TrimPrefix(p.Host, "www.")
	}
	return u
}

func normURL(u string) string {
	u = strings.TrimSpace(u)
	if u == "" {
		return "about:blank"
	}
	if !strings.Contains(u, "://") && u != "about:blank" {
		if strings.Contains(u, ".") && !strings.Contains(u, " ") {
			return "https://" + u
		}
		return "https://duckduckgo.com/?q=" + url.QueryEscape(u)
	}
	return u
}

func jsq(s string) string { b, _ := json.Marshal(s); return string(b) }

const jsClick = `(function(q){var ql=q.toLowerCase().trim();var els=[].slice.call(document.querySelectorAll('a,button,input[type=submit],input[type=button],[role=button],[role=link],[role=tab],[role=menuitem],label,summary,[onclick]'));var best=null;
for(var i=0;i<els.length;i++){var e=els[i],r=e.getBoundingClientRect();if(!r.width||!r.height)continue;var tx=(e.innerText||e.value||e.getAttribute('aria-label')||e.title||'').toLowerCase().replace(/\s+/g,' ').trim();if(!tx)continue;if(tx===ql){best=e;break}if(!best&&tx.indexOf(ql)>=0)best=e}
if(!best){try{best=document.querySelector(q)}catch(err){}}
if(!best)return 'NOT FOUND';best.scrollIntoView({block:'center'});best.click();return 'clicked: '+((best.innerText||best.value||best.getAttribute('aria-label')||'').trim().replace(/\s+/g,' ').slice(0,80))})(%s)`

const jsFocus = `(function(q){var ql=q.toLowerCase();var f=[].slice.call(document.querySelectorAll('input:not([type=hidden]),textarea,[contenteditable=true],select')).filter(function(e){var r=e.getBoundingClientRect();return r.width&&r.height});var el=null;
if(!ql)el=f[0];else{for(var i=0;i<f.length;i++){var e=f[i];var lab=((e.labels&&e.labels[0])?e.labels[0].innerText:'')+' '+(e.placeholder||'')+' '+(e.name||'')+' '+(e.id||'')+' '+(e.getAttribute('aria-label')||'');if(lab.toLowerCase().indexOf(ql)>=0){el=e;break}}if(!el){try{el=document.querySelector(q)}catch(err){}}}
if(!el)return 'NOT FOUND';el.scrollIntoView({block:'center'});el.focus();if('value' in el&&el.tagName!=='SELECT'){el.value='';el.dispatchEvent(new Event('input',{bubbles:true}))}return 'ok'})(%s)`

const jsRead = `(function(){var t=document.body?document.body.innerText:'';var links=[].slice.call(document.querySelectorAll('a[href]')).filter(function(a){return a.innerText.trim()}).slice(0,60).map(function(a){return '- '+a.innerText.trim().replace(/\s+/g,' ').slice(0,80)+' -> '+a.href});
var inputs=[].slice.call(document.querySelectorAll('input:not([type=hidden]),textarea,select')).slice(0,20).map(function(e){return '- '+(e.placeholder||e.name||e.id||e.type)});
return JSON.stringify({title:document.title,url:location.href,text:t.slice(0,12000),links:links,inputs:inputs})})()`

func (b *Browser) summary(t *bTab) string {
	return fmt.Sprintf("Now at %s (%s). Use browser_read to read the page or browser_screenshot to see it.", t.URL, t.Title)
}

func (b *Browser) Open(id, owner, u string) (string, error) {
	t, err := b.tab(id, owner)
	if err != nil {
		return "", err
	}
	u = normURL(u)
	err = b.do(t, 45*time.Second, chromedp.Navigate(u), chromedp.WaitReady("body", chromedp.ByQuery), chromedp.Sleep(700*time.Millisecond))
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return "", err
	}
	return b.summary(t), nil
}

func (b *Browser) Click(id, owner, target string) (string, error) {
	t, err := b.tab(id, owner)
	if err != nil {
		return "", err
	}
	var res string
	err = b.do(t, 30*time.Second, chromedp.Evaluate(fmt.Sprintf(jsClick, jsq(target)), &res), chromedp.Sleep(1200*time.Millisecond))
	if err != nil {
		return "", err
	}
	if res == "NOT FOUND" {
		return "", fmt.Errorf("nothing clickable matches %q; use browser_read to see the links and buttons", target)
	}
	return res + ". " + b.summary(t), nil
}

func (b *Browser) ClickXY(id string, x, y float64) error {
	t := b.get(id)
	if t == nil {
		return errors.New("no such tab")
	}
	return b.do(t, 15*time.Second, chromedp.MouseClickXY(x*viewW, y*viewH), chromedp.Sleep(600*time.Millisecond))
}

func (b *Browser) Type(id, owner, field, text string, submit bool) (string, error) {
	t, err := b.tab(id, owner)
	if err != nil {
		return "", err
	}
	var res string
	acts := []chromedp.Action{chromedp.Evaluate(fmt.Sprintf(jsFocus, jsq(field)), &res)}
	err = b.do(t, 30*time.Second, acts...)
	if err != nil {
		return "", err
	}
	if res == "NOT FOUND" {
		return "", fmt.Errorf("no input field matches %q", field)
	}
	acts = []chromedp.Action{chromedp.KeyEvent(text)}
	if submit {
		acts = append(acts, chromedp.KeyEvent(kb.Enter), chromedp.Sleep(1500*time.Millisecond))
	}
	if err := b.do(t, 30*time.Second, acts...); err != nil {
		return "", err
	}
	return "Typed. " + b.summary(t), nil
}

// Key sends what the user types in the Browser view to the focused element.
func (b *Browser) Key(id, text, key string) error {
	t := b.get(id)
	if t == nil {
		return errors.New("no such tab")
	}
	var a chromedp.Action
	switch key {
	case "Enter":
		a = chromedp.KeyEvent(kb.Enter)
	case "Backspace":
		a = chromedp.KeyEvent(kb.Backspace)
	case "Tab":
		a = chromedp.KeyEvent(kb.Tab)
	default:
		a = chromedp.KeyEvent(text)
	}
	return b.do(t, 15*time.Second, a, chromedp.Sleep(300*time.Millisecond))
}

func (b *Browser) Read(id, owner string) (string, error) {
	t, err := b.tab(id, owner)
	if err != nil {
		return "", err
	}
	var res string
	if err := b.do(t, 20*time.Second, chromedp.Evaluate(jsRead, &res)); err != nil {
		return "", err
	}
	var r struct {
		Title, URL, Text string
		Links, Inputs    []string
	}
	_ = json.Unmarshal([]byte(res), &r)
	out := fmt.Sprintf("URL: %s\nTitle: %s\n\n%s", r.URL, r.Title, r.Text)
	if len(r.Inputs) > 0 {
		out += "\n\nInput fields:\n" + strings.Join(r.Inputs, "\n")
	}
	if len(r.Links) > 0 {
		out += "\n\nLinks:\n" + strings.Join(r.Links, "\n")
	}
	return out, nil
}

func (b *Browser) Scroll(id, owner string, dy int) (string, error) {
	t, err := b.tab(id, owner)
	if err != nil {
		return "", err
	}
	if err := b.do(t, 10*time.Second, chromedp.Evaluate(fmt.Sprintf("window.scrollBy(0,%d)", dy), nil), chromedp.Sleep(400*time.Millisecond)); err != nil {
		return "", err
	}
	return "Scrolled.", nil
}

func (b *Browser) Back(id, owner string) (string, error) {
	t, err := b.tab(id, owner)
	if err != nil {
		return "", err
	}
	_ = b.do(t, 20*time.Second, chromedp.NavigateBack(), chromedp.Sleep(800*time.Millisecond))
	return b.summary(t), nil
}

func (b *Browser) Reload(id string) error {
	t := b.get(id)
	if t == nil {
		return errors.New("no such tab")
	}
	return b.do(t, 30*time.Second, chromedp.Reload(), chromedp.Sleep(600*time.Millisecond))
}

func (b *Browser) Screenshot(id, owner string) ([]byte, error) {
	t, err := b.tab(id, owner)
	if err != nil {
		return nil, err
	}
	t.mu.Lock()
	b.snapLocked(t)
	shot := t.shot
	t.mu.Unlock()
	return shot, nil
}

// Shot returns a recent screenshot for the UI, refreshing it when it is older than a second.
func (b *Browser) Shot(id string) []byte {
	t := b.get(id)
	if t == nil {
		return nil
	}
	if t.mu.TryLock() {
		if time.Since(t.shotAt) > time.Second {
			b.snapLocked(t)
		}
		t.mu.Unlock()
	}
	return t.shot
}

func (b *Browser) Close(id string) {
	b.mu.Lock()
	t := b.tabs[id]
	delete(b.tabs, id)
	b.mu.Unlock()
	if t != nil {
		t.cancel()
	}
}

func (b *Browser) Shutdown() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.stopRoot != nil {
		b.stopRoot()
		b.stopAlloc()
		b.root, b.alloc = nil, nil
	}
}

type TabInfo struct {
	ID      string    `json:"id"`
	Owner   string    `json:"owner"`
	URL     string    `json:"url"`
	Title   string    `json:"title"`
	Busy    bool      `json:"busy"`
	Updated time.Time `json:"updated"`
}

func (b *Browser) Tabs() []TabInfo {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := []TabInfo{}
	for _, t := range b.tabs {
		out = append(out, TabInfo{t.ID, t.Owner, t.URL, t.Title, t.Busy, t.Updated})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out
}

// ---------- agent tools ----------

var browserTools = []map[string]any{
	{"name": "browser_open", "description": "Open a web page (or search words) in the Agent HQ browser. The user can watch this browser live. Prefer it over other browser tools.",
		"inputSchema": schema(map[string]any{"url": sProp("URL, or words to search for")}, "url")},
	{"name": "browser_read", "description": "Read the text, input fields and links of the current page.", "inputSchema": schema(map[string]any{})},
	{"name": "browser_click", "description": "Click a link or button by its visible text (or a CSS selector).",
		"inputSchema": schema(map[string]any{"target": sProp("Visible text of the link/button, or a CSS selector")}, "target")},
	{"name": "browser_type", "description": "Type into an input field found by its label, placeholder or name (empty = first field). submit=true presses Enter.",
		"inputSchema": schema(map[string]any{"field": sProp("Label, placeholder or name of the field"), "text": sProp("Text to type"), "submit": map[string]any{"type": "boolean"}}, "text")},
	{"name": "browser_scroll", "description": "Scroll the page up or down.",
		"inputSchema": schema(map[string]any{"direction": map[string]any{"type": "string", "enum": []string{"down", "up"}}}, "direction")},
	{"name": "browser_back", "description": "Go back to the previous page.", "inputSchema": schema(map[string]any{})},
	{"name": "browser_screenshot", "description": "See the current page as an image.", "inputSchema": schema(map[string]any{})},
}

// browserTool runs a browser_* tool for an agent. It returns MCP content items.
func (a *App) browserTool(ag *Agent, name string, args map[string]any) ([]map[string]any, bool) {
	text := func(s string) []map[string]any { return []map[string]any{{"type": "text", "text": s}} }
	var out string
	var err error
	switch name {
	case "browser_open":
		out, err = a.browser.Open(ag.ID, ag.Name, str(args, "url"))
	case "browser_read":
		out, err = a.browser.Read(ag.ID, ag.Name)
	case "browser_click":
		out, err = a.browser.Click(ag.ID, ag.Name, str(args, "target"))
	case "browser_type":
		out, err = a.browser.Type(ag.ID, ag.Name, str(args, "field"), str(args, "text"), str(args, "submit") == "true")
	case "browser_scroll":
		dy := 700
		if str(args, "direction") == "up" {
			dy = -700
		}
		out, err = a.browser.Scroll(ag.ID, ag.Name, dy)
	case "browser_back":
		out, err = a.browser.Back(ag.ID, ag.Name)
	case "browser_screenshot":
		shot, e := a.browser.Screenshot(ag.ID, ag.Name)
		if e != nil {
			return text("Browser error: " + e.Error()), true
		}
		b64 := encodeB64(shot)
		return []map[string]any{{"type": "image", "data": b64, "mimeType": "image/jpeg"}}, false
	default:
		return text("unknown browser tool"), true
	}
	if t := a.browser.get(ag.ID); t != nil {
		a.mu.Lock()
		ag.ToolKind, ag.ToolTarget = "browser", host(t.URL)
		a.mu.Unlock()
	}
	if err != nil {
		return text("Browser error: " + err.Error()), true
	}
	return text(out), false
}

func (a *App) browserRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/browser/tabs", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"tabs": a.browser.Tabs(), "error": a.browser.Err, "exe": findBrowserExe() != ""})
	})
	mux.HandleFunc("/api/browser/shot", func(w http.ResponseWriter, r *http.Request) {
		shot := a.browser.Shot(r.URL.Query().Get("id"))
		if len(shot) == 0 {
			http.Error(w, "no screenshot", 404)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(shot)
	})
	mux.HandleFunc("/api/browser/act", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			ID, Action, URL, Text, Key string
			X, Y                       float64
		}
		_ = readBody(r, &in)
		if in.ID == "" {
			in.ID = "you"
		}
		var err error
		switch in.Action {
		case "open":
			_, err = a.browser.Open(in.ID, "you", in.URL)
		case "click":
			err = a.browser.ClickXY(in.ID, in.X, in.Y)
		case "key":
			err = a.browser.Key(in.ID, in.Text, in.Key)
		case "scroll":
			dy, _ := strconv.Atoi(in.Text)
			_, err = a.browser.Scroll(in.ID, "you", dy)
		case "back":
			_, err = a.browser.Back(in.ID, "you")
		case "reload":
			err = a.browser.Reload(in.ID)
		case "close":
			a.browser.Close(in.ID)
		}
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	})
}
