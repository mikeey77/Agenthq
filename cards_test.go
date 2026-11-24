package main

import (
	"os"
	"testing"
	"time"
)

func TestRenderCards(t *testing.T) {
	if findBrowserExe() == "" || os.Getenv("AGENTHQ_CARDS") == "" {
		t.Skip("set AGENTHQ_CARDS=1 with Chrome installed")
	}
	a := &App{cfg: defaultConfig(), agents: map[string]*Agent{}, decisions: nil, usage: newUsageTracker(), browser: newBrowser(), demo: true, tgMsgs: map[int64]string{}}
	a.cfg.Lang = "nl"
	a.seedDemo()
	defer a.browser.Shutdown()
	a.agents["a1"].Summary = "Web prototype werkt: foto → bedrag, datum en winkel, export naar CSV. 18 van 20 testbonnen goed herkend; de 2 missers waren verkreukelde bonnen."
	a.limitUntil = time.Now().Add(72 * time.Minute)
	for name, doc := range map[string]string{"done": a.doneCard(a.agents["a1"]), "status": a.statusCard(), "limit": a.limitCard()} {
		img, err := a.browser.RenderHTML(doc, 560)
		if err != nil || len(img) < 1000 {
			t.Fatalf("%s: %v", name, err)
		}
		_ = os.WriteFile("/tmp/card_"+name+".png", img, 0o644)
	}
}
