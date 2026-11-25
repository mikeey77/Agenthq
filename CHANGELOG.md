# Changelog

## 2.7.0

- **Memory per project.** Agents save decisions, your preferences and lessons with `remember`; every new agent in that project reads them first. There is also a general memory for every project. Manage it in the new Library page or via Memory on a project.
- **Review agent.** After a mission changes files, a reviewer checks the work, runs the tests, fixes small problems and ends with ✅ Approved or ⚠️ Needs work. Project tasks only move to Check after the review. Off / projects only / all missions in Settings.
- **Skills library.** Agents save proven step-by-step ways of working (`save_skill`) and load them when a task matches (`use_skill`). Write your own in Library.
- **Voice messages on Telegram.** Talk to your agents: voice notes are turned into text on your own PC with whisper.cpp (free, private). One-time download of about 235 MB, started from Telegram or Settings.
- **Usage per project.** New Usage page with tokens per day per project (7/14/30 days), totals and value per project.
- **Put online.** Deploy a project to Netlify or GitHub Pages with one click, with /deploy on Telegram, or by an agent. Builds first when there is a build script; .env files are never uploaded.

## 2.6.0

- **Telegram is a full terminal.** Type what you want and an agent starts; keep typing to talk to it. Reply to any agent message to talk to that agent. Send photos and files. /agents shows buttons to talk, pause, stop and read the log; Telegram's own "/" menu lists every command.
- **Planner.** Missions that run every day, on chosen weekdays, every few hours or once. Agents can plan them too ("every Monday at 9…"). Planned missions wait for the usage reset.
- **New first-run setup** in six steps: Claude Code check with install help, agent permissions, Telegram with live linking, GitHub, and a choice to run a test mission or open the demo.
- **Demo mode** from the setup or Settings: a second window with fake agents to try every screen.
- Clearer screens: short explanations at the top of each page, icons per page, smoother animations.

## 2.5.0

- **Projects.** Every project gets a folder and a task board (To do, Doing, Check, Done). Drag tasks between columns, press Start to put an agent on a task, and agents keep the board up to date themselves.
- **GitHub.** Agents can create a repository and push their work (private by default; public only after you confirm). Add a token in Settings.
- **Files.** Folder buttons on agents and projects open Explorer, and the Files map shows what the agents changed. Click a file to show it in Explorer.
- **Picture cards on Telegram.** Finished missions, /status and the usage limit arrive as images; agents can attach a screenshot of the browser page.
- **Emoji answers.** Questions on Telegram use 1️⃣ 2️⃣ 3️⃣ or ✅ ❌: tap a button or just send the emoji.
- **Security:** other websites can no longer send commands to Agent HQ.

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
