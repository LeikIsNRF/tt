# tt cheat sheet

✅ built · 🔜 planned (issue number) · 💭 proposed — key not decided in the plan yet

## Getting to the board

| Key / command        | What                                               |        |
| -------------------- | -------------------------------------------------- | ------ |
| `prefix t`           | Board as a popup over the current window           | ✅     |
| `prefix 0`           | Pinned board (if tt runs in window 0)              | ✅     |
| `prefix T`           | Quick-capture popup: one-line todo                 | ✅     |

`prefix t` replaces tmux's default clock-mode binding.

## Board

### Move & jump

| Key       | What                                                        |       |
| --------- | ----------------------------------------------------------- | ----- |
| `← →`     | Move between columns (or along the Untracked row)           | ✅    |
| `↑ ↓`     | Move between cards; `↓` past the last card → Untracked row  | ✅    |
| `Enter`   | Jump to the task's window (popup closes)                    | ✅    |
| `Enter`   | On a dormant task: recreate window + resume picker          | ✅    |
| `Esc`     | Close the popup (no-op on the pinned board)                 | ✅    |
| `q`       | Quit                                                        | ✅    |

### Tasks

| Key  | What                                                              |       |
| ---- | ----------------------------------------------------------------- | ----- |
| `n`  | New task: name, ticket, cwd → new window with `TT_TASK`, jump     | ✅    |
| `a`  | Adopt the selected Untracked window as a task                     | ✅    |
| `d`  | Done: confirm if working/needs-you, close window, archive notes   | ✅    |
| `o`  | Open the task's ticket (e.g. `ABC-123`) in the browser           | ✅    |
| `b`  | Pick the task's stack branch from local branches (type to filter) | ✅    |
| `r`  | Refresh PR/CI state from GitHub now (else every 2 min)            | ✅    |

### Todos & notes

| Key     | What                                               |       |
| ------- | -------------------------------------------------- | ----- |
| `tab`   | Focus the selected card's todo list (`tab`/`esc` back) | ✅ |
| `space` | Toggle the selected todo                           | ✅    |
| `e`     | Open the task's notes file in `$VISUAL`/`$EDITOR`  | ✅    |
| `l`     | Launch Claude on the selected todo (see Agents)    | ✅    |
| `i`     | Inbox view                                         | ✅    |
| `m`     | Move an inbox todo to a task                       | ✅    |

### Agents

| Key      | What                                                                  |       |
| -------- | --------------------------------------------------------------------- | ----- |
| `l`      | Launch Claude on the task: editable prompt (name, ticket, open todos) → confirm → new pane in its window | ✅ |
| `l`      | In the todo list: launch on that todo; Claude ticks it off with `tt todo done` | ✅ |
| `l`      | In the inbox: promote the todo to a new task (it moves there), then launch | ✅ |
| `ctrl+g` | In the new-task form: draft name, ticket, todos, notes from a description | ✅ |

The launch prompt: `enter` next · `alt+enter` newline · `esc` cancel. The
confirm screen prints the exact command, warns if another agent is 🟢 working
in the same cwd (two agents would switch branches under each other), and `y`
launches. A dormant task's window is revived first. Launch never passes a
permission-bypass flag, so permission prompts turn the tab red as usual; the
pane closes when Claude exits.

### Finding & help

| Key | What                                                         |       |
| --- | ------------------------------------------------------------ | ----- |
| `/` | Filter by name, ticket or label as you type (also finds old Done tasks); `enter` keeps it, `esc` clears | ✅ |
| `:` | Command line (below); every key above is also a `:` command  | ✅    |
| `?` | Help: every key and command (`↑↓` / `space` scroll)          | ✅    |

The bottom of the board **previews** the selected task's Claude pane (its
most urgent session; else the window's active pane), refreshed every second.
`Enter` still jumps to the real window.

### Forms (new task / adopt)

| Key                | What           |    |
| ------------------ | -------------- | -- |
| `Tab` / `↓`        | Next field     | ✅ |
| `Shift+Tab` / `↑`  | Previous field | ✅ |
| `Enter`            | Save           | ✅ |
| `Esc`              | Cancel         | ✅ |
| `Ctrl+G`           | New task: draft name, ticket, todos and notes from **Describe** (previewed; written on save) | ✅ |

The draft is one plain `claude -p` call (no tools, so no Jira or code). Set
the model and timeout in `~/.tt/config.toml`:

```toml
[task]
cwd = "~/src/app"                                        # pre-fills a new task's cwd; default "~"
ticket_url = "https://example.atlassian.net/browse/%s"  # how `o` opens a ticket ID; no default

[draft]
model = "haiku"   # default
timeout = "30s"   # default
```

## `:` commands

| Command               | What                                         |       |
| --------------------- | -------------------------------------------- | ----- |
| `:new [name]`         | Same as `n`, name filled in                  | ✅    |
| `:adopt [name]`       | Same as `a`                                  | ✅    |
| `:done` `:edit` `:open` `:jump` `:todos` `:launch` `:refresh` `:inbox` `:help` `:quit` | Same as `d` `e` `o` `enter` `tab` `l` `r` `i` `?` `q` | ✅ |
| `:delete`             | Delete the task: closes its windows, deletes its notes and history (asks first) | ✅ |
| `:filter [text]`      | Set the filter; no text → `/` prompt         | ✅    |
| `:branch [name]`      | Set the task's stack branch (any in the stack); no name → picker | ✅ |
| `:label <text>`       | Add a manual label to the card               | ✅    |
| `:unlabel <text>`     | Remove a label                               | ✅    |
| `:ticket [id]`        | Set (or clear) the task's ticket             | ✅    |
| `:context off` / `on` | Stop / resume injecting task context into new Claude sessions (card shows `ctx off`) | ✅ |

## Shell

| Command                   | What                                                    |       |
| ------------------------- | ------------------------------------------------------- | ----- |
| `tt`                      | Open the board                                          | ✅    |
| `tt install [-y]`         | Merge hooks into `~/.claude/settings.json`, install the `tt` skill, write `~/.tt/tmux.conf` | ✅ |
| `tt uninstall`            | Remove exactly tt's hooks and skill                     | ✅    |
| `tt doctor`               | Check hooks, skill + tmux config                        | ✅    |
| `tt help`                 | Usage                                                   | ✅    |
| `tt add "<text>"`         | Todo → current task, else inbox                         | ✅    |
| `tt add --inbox "<text>"` | Force inbox                                             | ✅    |
| `tt todo list`            | Open todos, numbered                                    | ✅    |
| `tt todo done <n\|text>`  | Tick a todo off                                         | ✅    |
| `--task <slug>`           | On any todo command: target another task                | ✅    |
| `tt resume [slug]`        | Resume picker for this window's task (revive runs it)   | ✅    |

`tt hook` and `tt --popup` exist but are called by Claude Code and tmux, not by you.

## Claude

A new Claude session in a task window starts with a short `tt task:` block:
name, ticket, stack + PR state (from the board's cache, never a live fetch),
open todos and the notes file. Re-injected after `/compact`. Ask it to
"add a todo to <task>" and it runs `tt todo add --task …` via the `tt` skill.

## Reading the board

**Columns:** 🔴 Needs you · 🟡 Your turn · 🟢 Working · 💤 Idle · ✓ Done (last 7 days).
A card sits in the column of its most urgent session. A task whose window is gone
is **dormant** (Idle, greyed); its sessions stay linked and show in the resume picker.
Done tasks' notes move to `~/.tt/tasks/done/`.

**tmux tabs:** red = a Claude needs you · yellow = your turn · `status-right` shows the needs-you count.

**Stack strip**, one glyph per branch, bottom → top (most actionable state wins):

`✓` merged · `◆` approved · `●` in review · `✎` changes requested ·
`⏳` CI running · `✗` CI failing · `○` draft / no PR

Merged branches stay in the strip after `gt sync` deletes them, until the whole
stack lands. tt then offers once to mark the task done (`y`; any other key dismisses).
