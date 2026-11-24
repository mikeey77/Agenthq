package main

import (
	"fmt"
	"strings"
)

var dict = map[string]map[string]string{
	"en": {
		"started":         "Agent HQ started",
		"mission_new":     "New mission for %s: %s",
		"spawned":         "%s created %s (%s)",
		"spawned_user":    "%s joined (%s)",
		"done":            "%s finished: %s",
		"replied":         "%s: %s",
		"crashed":         "%s crashed: %s",
		"paused":          "%s paused",
		"resumed":         "%s resumed",
		"stopped":         "%s stopped",
		"turn_limit":      "%s hit the turn limit (%d) and was paused. Resume it to let it continue.",
		"limit_agents":    "Agent limit reached (%d). Stop or delete agents first.",
		"limit_depth":     "Max depth reached (%d): this agent cannot create more sub-agents.",
		"claude_missing":  "Claude Code CLI not found. Set the path in Settings.",
		"decision":        "Decision needed",
		"decision_hint":   "Tap an option, react 👍/👎, or reply to this message with your own answer.",
		"answered":        "Answered: %s",
		"perm_q":          "%s wants to use %s",
		"allow":           "Allow",
		"deny":            "Deny",
		"critical":        "Critical",
		"update":          "Update",
		"from":            "from %s",
		"btn_pause":       "⏸ Pause",
		"btn_stop":        "⏹ Stop",
		"tg_linked":       "✅ Linked to Agent HQ!\n\nCommands:\n/mission <text> – start a new mission\n/status – what your agents are doing\n/agents – list agents\n/msg <agent> <text> – message an agent\n/pause <agent|all>\n/resume <agent|all>\n/stop <agent|all>\n/usage – token usage",
		"tg_other_chat":   "This bot is already linked to another chat.",
		"tg_unknown":      "Unknown command. Try /help",
		"tg_test":         "👋 Test from Agent HQ — notifications work!",
		"no_agents":       "No agents yet. Start one with /mission <text>",
		"unknown_agent":   "Unknown agent: %s",
		"ok":              "OK",
		"queued_msg":      "Message queued for %s",
		"usage_line":      "Tokens this 5h block: %s (%d%%) · resets in %s\nThis week: %s",
		"status_head":     "🤖 Agent HQ — %d running, %d waiting on you",
		"help":            "Commands:\n  <text>                start a new mission\n  mission <text>        start a new mission\n  @agent <text>         send a message to an agent\n  spawn <name>: <task>  start an extra top-level agent\n  pause <agent|all>     pause\n  resume <agent|all>    resume\n  stop <agent|all>      stop (incl. sub-agents)\n  delete <agent>        remove a stopped agent\n  list                  list agents\n  usage                 token usage\n  clear                 clear the terminal",
		"term_mission":    "Mission started → %s",
		"term_list_empty": "No agents.",
		"stop_reason":     "Stopped by user",
		"agent_finished_parent": "[Sub-agent %s finished] %s",
		"agent_reply_parent":    "[Sub-agent %s reported] %s",
		"user_msg":        "[Message from the user] %s",
		"agent_msg":       "[Message from agent %s] %s",
		"decision_answer": "[Answer to your earlier decision \"%s\"] %s",
		"continue":        "You were paused by the user and are now resumed. Continue where you left off.",
	},
	"nl": {
		"started":         "Agent HQ gestart",
		"mission_new":     "Nieuwe missie voor %s: %s",
		"spawned":         "%s heeft %s gemaakt (%s)",
		"spawned_user":    "%s doet mee (%s)",
		"done":            "%s is klaar: %s",
		"replied":         "%s: %s",
		"crashed":         "%s is gecrasht: %s",
		"paused":          "%s gepauzeerd",
		"resumed":         "%s hervat",
		"stopped":         "%s gestopt",
		"turn_limit":      "%s heeft de beurtlimiet (%d) bereikt en is gepauzeerd. Hervat om door te gaan.",
		"limit_agents":    "Agentlimiet bereikt (%d). Stop of verwijder eerst agents.",
		"limit_depth":     "Maximale diepte bereikt (%d): deze agent mag geen sub-agents meer maken.",
		"claude_missing":  "Claude Code CLI niet gevonden. Stel het pad in bij Instellingen.",
		"decision":        "Beslissing nodig",
		"decision_hint":   "Tik op een optie, reageer met 👍/👎, of beantwoord dit bericht met je eigen antwoord.",
		"answered":        "Beantwoord: %s",
		"perm_q":          "%s wil %s gebruiken",
		"allow":           "Toestaan",
		"deny":            "Weigeren",
		"critical":        "Kritiek",
		"update":          "Update",
		"from":            "van %s",
		"btn_pause":       "⏸ Pauzeer",
		"btn_stop":        "⏹ Stop",
		"tg_linked":       "✅ Gekoppeld aan Agent HQ!\n\nCommando's:\n/mission <tekst> – start een nieuwe missie\n/status – wat je agents doen\n/agents – lijst met agents\n/msg <agent> <tekst> – stuur een agent een bericht\n/pause <agent|all>\n/resume <agent|all>\n/stop <agent|all>\n/usage – tokenverbruik",
		"tg_other_chat":   "Deze bot is al gekoppeld aan een andere chat.",
		"tg_unknown":      "Onbekend commando. Probeer /help",
		"tg_test":         "👋 Test vanuit Agent HQ — meldingen werken!",
		"no_agents":       "Nog geen agents. Start er een met /mission <tekst>",
		"unknown_agent":   "Onbekende agent: %s",
		"ok":              "OK",
		"queued_msg":      "Bericht staat klaar voor %s",
		"usage_line":      "Tokens dit 5u-blok: %s (%d%%) · reset over %s\nDeze week: %s",
		"status_head":     "🤖 Agent HQ — %d bezig, %d wachten op jou",
		"help":            "Commando's:\n  <tekst>                start een nieuwe missie\n  mission <tekst>        start een nieuwe missie\n  @agent <tekst>         stuur een bericht naar een agent\n  spawn <naam>: <taak>   start een extra hoofdagent\n  pause <agent|all>      pauzeren\n  resume <agent|all>     hervatten\n  stop <agent|all>       stoppen (incl. sub-agents)\n  delete <agent>         verwijder een gestopte agent\n  list                   lijst met agents\n  usage                  tokenverbruik\n  clear                  terminal leegmaken",
		"term_mission":    "Missie gestart → %s",
		"term_list_empty": "Geen agents.",
		"stop_reason":     "Gestopt door gebruiker",
		"agent_finished_parent": "[Sub-agent %s is klaar] %s",
		"agent_reply_parent":    "[Sub-agent %s meldt] %s",
		"user_msg":        "[Bericht van de gebruiker] %s",
		"agent_msg":       "[Bericht van agent %s] %s",
		"decision_answer": "[Antwoord op je eerdere beslissing \"%s\"] %s",
		"continue":        "Je was gepauzeerd door de gebruiker en bent nu hervat. Ga verder waar je gebleven was.",
	},
}

func (a *App) T(key string, args ...any) string {
	lang := a.cfg.Lang
	if dict[lang] == nil {
		lang = "en"
	}
	s, ok := dict[lang][key]
	if !ok {
		s = dict["en"][key]
	}
	if len(args) > 0 {
		return fmt.Sprintf(s, args...)
	}
	return s
}

func fmtTokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/1e6), ".0") + "M"
	case n >= 1000:
		return fmt.Sprintf("%dk", n/1000)
	}
	return fmt.Sprint(n)
}
