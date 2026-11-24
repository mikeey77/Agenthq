# Changelog

## 2.4.0

- **Own app window** with a new robot icon instead of a browser tab. Closing it keeps your agents running; start Agent HQ again to reopen it.
- **In-app browser.** Agents open, read, click and type on real web pages; you watch live in the new Browser view and can take over by clicking or typing.
- **Activity icons** next to every robot: browsing, editing a file, running a command, using an app.
- **Colors mean something now:** lead, research, build, test, write, review, web, design, data and ops each have their own color, with a legend.
- **"/" command menu** in the terminal and command bar, with arrow keys, Tab to complete and agent names for pause/stop.
- **Smart permissions by default:** agents just work and only ask before risky steps (deleting, paying, sending, system changes).
- **Usage limits handled:** when your Claude limit runs out, all agents pause, Telegram tells you when it resets, and they continue on their own afterwards. You also get a warning at 90%.
- **More connectors:** Email (Gmail, Outlook, iCloud, any IMAP), Slack, Desktop Commander, Netlify, Supabase, PostgreSQL, SQLite, Stripe, Google Maps.
- New NOMAD-style interface with a customizable widget dashboard.

## 2.2.0
- Auto-updater: checks GitHub releases every 6 hours, verifies the SHA-256 checksum, installs by itself when no agent is working, otherwise shows "Restart and update". Agents that were working continue after the restart.
- Updates section in Settings with release notes and a "Check now" button.

## 2.1.0
- Terminal only treats exact commands as commands; every other sentence starts a mission.
- Replies of mission leads appear in the master terminal.
- "Clear finished" button and `clear done` command.
- Windows notifications for decisions and critical alerts while the window is in the background.
- Markdown in agent output, Ctrl+K jumps to the terminal.

## 2.0.2
- Decisions that were waiting before Telegram was linked are sent right after `/start`.

## 2.0.1
- Fixed: Claude Code rejected the built-in HQ tools (`required: null` in two tool schemas).
- Agents retry once when the HQ tools load late.

## 2.0.0
- Agents run as headless Claude Code processes, can create sub-agents, message each other and ask for decisions.
- Telegram control with 🟢 updates, ⚠️ decisions (buttons, 👍/👎, or a reply) and 🚨 critical alerts.
- Connectors, token usage meter, English and Dutch, robot mascots.
