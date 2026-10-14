# tmuxrd

An agent-status sidebar for tmux.

A daemon watches every pane, works out which coding agents are running (pi, claude, codex,
opencode, cursor, kimi, hermes, gemini, grok) and what each one is doing, and every window gets a
narrow sidebar that shows it:

```
tmuxrd
────────────────────────────────
main
> ⠴ 0:pi           1m26s  user/
  ○ 2:pi                  user/
  ? 3:claude             api/
```

- `⠴` (an animated braille spinner) — the agent is **working**, with a timer for the current run
- `?` — the agent is **waiting on you** (a question/approval prompt)
- `○` — idle, or the state could not be determined
- the marker `>` tracks the window you are looking at; a window with no agent shows no marker

Nothing is installed into the agents. State is inferred from what tmux already exposes (process
name plus the visible screen), so it works with any agent in any pane.

## Requirements

- tmux 3.2+ (developed against tmux next-3.9)
- Go 1.22+ to build (only if you are not using a release tarball)

## Install

### From a release tarball

```sh
version=v0.0.1
curl -fsSL "https://github.com/naqerl/tmuxrd/releases/download/$version/tmuxrd_linux_amd64.tar.gz" \
  | tar -xz -C "$HOME"
mv "$HOME/tmuxrd_linux_amd64" "$HOME/.tmuxrd"
```

### From source

```sh
git clone https://github.com/naqerl/tmuxrd.git ~/.tmuxrd
cd ~/.tmuxrd && make build          # builds bin/tmuxrd and the test fakeagent
```

### Load it

Add this to `~/.tmux.conf`:

```
# @tmuxrd_root is where you installed tmuxrd (the directory holding tmuxrd.tmux)
set -g @tmuxrd_root "$HOME/.tmuxrd"
source-file "$HOME/.tmuxrd/tmuxrd.tmux"
```

then reload:

```sh
tmux source-file ~/.tmux.conf
```

The sidebar appears in every window. `tmuxrd` resolves all of its paths from `@tmuxrd_root`, so
the checkout can live anywhere — set it whenever you install outside the default root.

## Keys

| Key | Action |
|---|---|
| `prefix` + `b` | show/hide the sidebar (in every window) |
| `j` / `k` (or `↓`/`↑`) | go to the next/previous agent — this **switches the window/session** to it |
| `Enter` | move the keyboard into that agent's pane |
| `q` / `Ctrl-C` | close a sidebar pane |

Navigation moves the view but keeps the keyboard on the sidebar's pane, so you can keep stepping
through agents; `Enter` is what actually hands you the pane.

## Configuration

| Option | Meaning |
|---|---|
| `@tmuxrd_root` | where tmuxrd is installed (all paths are resolved from it) |
| `@tmuxrd_enabled` | set to `0` to stop new windows from getting a sidebar |

The status line shows a one-line summary via `tmuxrd summary`:
`agents=3 idle=1 working=1 blocked=1`.

## How state is detected

- **working** — the agent's braille spinner animation is on screen (any Unicode braille frame).
  Word matching was tried and removed: it fired on ordinary text such as token counters and paths.
- **blocked (pi)** — pi's multiple-choice prompt, matched structurally: pi's exact footer help
  *and* at least two numbered `[ ]` options. Options alone are not enough, so an agent's own
  markdown checklist does not count.
- **blocked (other agents)** — a small set of unambiguous prompt strings (`[y/n]`, `yes/no`, …).
- **idle** — a shell prompt, or a pane whose foreground process is no longer the agent.

The timer is owned by the daemon and measures the **current** working period: it starts when the
agent starts working, is frozen when it stops, and restarts from zero the next time the agent
starts working.

## Development

```sh
make build        # bin/tmuxrd + cmd/fakeagent/fakeagent
make integration  # the godog suite (34 scenarios / 261 steps)
make vet
```

`make integration` is hermetic: every scenario runs on its own `tmux -L godog-*` server with
`-f /dev/null`, so it never touches a real tmux server. It needs `tmux` and `go` on `PATH`.

## Known limits

- pi's blocked detection keys on its footer help, which needs a pane roughly 110 columns wide. In a
  much narrower pane the agent's TUI truncates that footer, and then the state cannot be detected.
- Working detection is braille-spinner only. An agent that shows no braille spinner while working
  will not be reported as working.
- The timer's values are in the daemon's memory, so restarting the daemon resets them.
