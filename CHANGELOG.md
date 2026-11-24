# Changelog

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
