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

### One command (recommended)

```sh
curl -fsSL https://raw.githubusercontent.com/naqerl/tmuxrd/main/install.sh | bash
```

That downloads the latest release for your OS/architecture, installs it (default `~/.tmuxrd`),
wires `~/.tmux.conf` to load it, and — if a tmux server is running — restarts the daemon and
rebuilds the sidebar panes, so the new version is live immediately. Re-run the very same command to
update.

Overrides: `TMUXRD_ROOT` (install dir), `TMUXRD_CONF` (config to wire), `TMUXRD_VERSION`
(a tag such as `v0.1.0`, default `latest`). Set these before `bash`, e.g.
`curl … | TMUXRD_ROOT=/opt/tmuxrd bash`.

Run `./install.sh` from a checkout instead and it builds and installs *that* checkout.

> `mouse on` is not set for you (it changes tmux globally). If the installer warns that it is off,
> add `set -g mouse on` to your config to enable click/tap selection in the sidebar.

### From a release tarball (manual)

```sh
version=v0.0.1
curl -fsSL "https://github.com/naqerl/tmuxrd/releases/download/$version/tmuxrd_linux_amd64.tar.gz" \
  | tar -xz -C "$HOME"
mv "$HOME/tmuxrd_linux_amd64" "$HOME/.tmuxrd"
```

### From source (manual)

```sh
git clone https://github.com/naqerl/tmuxrd.git ~/.tmuxrd
cd ~/.tmuxrd && make build          # builds bin/tmuxrd and the test fakeagent
```

### Load it (manual installs)

The one-command installer does this for you; a manual install needs these two lines in
`~/.tmux.conf`:

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

### Updating

Re-run the one-command installer. It replaces the binary atomically, restarts the daemon for the
running server(s), and rebuilds the sidebar panes so they run the new client. Nothing else to do.

## Keys

| Key | Action |
|---|---|
| `prefix` + `b` | show/hide the sidebar (in every window) |
| `j` / `k` (or `↓`/`↑`, `Tab`/`Shift+Tab`, wheel) | go to the next/previous agent — **switches the window/session** and keeps the keyboard on the sidebar so you can keep stepping |
| `1`–`9` | jump straight to the Nth agent and put the keyboard in its pane |
| click / tap | select the agent on that row (desktop) or chip (mobile strip), and put the keyboard in its pane |
| `Enter` | move the keyboard into the selected agent's pane |
| `q` / `Ctrl-C` | close a sidebar pane |

Relative navigation (`j`/`k`, `Tab`, the wheel) deliberately leaves the keyboard on the sidebar, so
repeated presses keep stepping. Direct selection (`1`–`9`, a click or a tap) instead lands you in the
agent pane, ready to type.

### Mouse and touch

Clicks and taps need **both** sides to agree:

- **tmux must have `mouse on`.** tmuxrd does not force it (it would change tmux's behaviour
  globally). Without it tmux never forwards mouse events into panes, so a tap cannot reach the
  sidebar. Add `set -g mouse on` to `~/.tmux.conf` if you have not.
- The sidebar client asks for the mouse itself, so once tmux forwards events a tap on a row or chip
  selects that agent — the same as `1`-`9`. Most mobile terminals begin reporting taps as soon as
  the app asks; a few have a “mouse” setting of their own to enable.

## Mobile: the sidebar adapts to the window's width

A tmux window's layout is shared by every attached client, so a phone and a desktop cannot each
have their own shape at the same moment. tmuxrd adapts to whichever client the window currently
fits (`window-size latest`, tmux's default):

- **60 columns or more** — a 32-column left sidebar that lists one row per agent.
- **fewer than 60 columns** — a **2-row full-width strip across the top**, because on a phone
  columns are the scarce resource and rows are not. The strip is its own surface: a full-width bar
  with an alternate background, on which each agent is a padded **tab**; the selected tab is
  highlighted (that highlight replaces the desktop sidebar's `>`). Row 2 describes the selected
  agent. `1`–`9` switch to an agent in one keystroke.

The shape is re-checked every time the window resizes and by the daemon (every tick), so attaching
from a phone flips the sidebar to the strip and attaching from a desktop flips it back. A window
whose sidebar is already the right shape is left alone — except that the strip is kept at exactly
`@tmuxrd_strip_height` rows (a window resize squeezes tmux's smallest pane first, which would
otherwise leave a one-line strip with no detail line).

| Option | Default | Meaning |
|---|---|---|
| `@tmuxrd_mobile_width` | `60` | window width below which the top strip is used |
| `@tmuxrd_strip_height` | `2` | rows the mobile strip takes |
| `@tmuxrd_side_width` | `32` | columns the desktop sidebar takes |

Set them before loading `tmuxrd.tmux` (or with `set -g` later and run `tmuxrd adapt`):

```tmux
set -g @tmuxrd_mobile_width 50
```

## Configuration

| Option | Meaning |
|---|---|
| `@tmuxrd_root` | where tmuxrd is installed (all paths are resolved from it) |
| `@tmuxrd_enabled` | set to `0` to stop new windows from getting a sidebar |
| `@tmuxrd_mobile_width` | window width below which the sidebar becomes a top strip (default `60`) |
| `@tmuxrd_strip_height` | rows the mobile strip takes (default `2`) |
| `@tmuxrd_side_width` | columns the desktop sidebar takes (default `32`) |

tmuxrd does not touch your status line. `tmuxrd summary` is available as a command if you want to
put a one-line census (`agents=3 idle=1 working=1 blocked=1`) there yourself.

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
make integration  # the godog suite (46 scenarios)
make vet
```

`make integration` is hermetic: every scenario runs on its own `tmux -L godog-*` server with
`-f /dev/null`, so it never touches a real tmux server. It needs `tmux` and `go` on `PATH`.

## Commands

| Command | Purpose |
|---|---|
| `tmuxrd daemon` | the watcher; also keeps every sidebar in the right shape |
| `tmuxrd client` | one sidebar pane's UI (auto-detects side vs strip) |
| `tmuxrd adapt [--create]` | reshape sidebars to match their window; `--create` also adds a missing one |
| `tmuxrd notify` | tell the daemon to re-detect now |
| `tmuxrd stop` | shut down this server's daemon (a scoped restart; the installer uses it) |
| `tmuxrd summary` | one-line agent census, e.g. for a custom status line |

## Known limits

- pi's blocked detection keys on its footer help, which needs a pane roughly 110 columns wide. In a
  much narrower pane the agent's TUI truncates that footer, and then the state cannot be detected.
- Working detection is braille-spinner only. An agent that shows no braille spinner while working
  will not be reported as working.
- The timer's values are in the daemon's memory, so restarting the daemon resets them.
