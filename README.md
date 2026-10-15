# tmuxrd

An agent-status sidebar for tmux.

A daemon watches every pane, works out which coding agents are running (pi, claude, codex,
opencode, cursor, kimi, hermes, gemini, grok) and what each one is doing, and every window gets a
narrow sidebar that shows it:

```
tmuxrd
────────────────────────────────
main
  ⠴ 0:pi           1m26s  user/   ← highlighted: the window you are looking at
  ✓ 2:pi                  user/
  ? 3:claude             api/
  ○ 4:grok                 web/
```

- `⠴` (an animated braille spinner) — the agent is **working**, with a timer for the current run
- `✓` — the agent **finished a run** and you have not looked at it since (a notification pin),
  with the run's duration
- `?` — the agent is **waiting on you** (a question/approval prompt)
- `○` — the agent has **never started** (a new session), has already been looked at, or its state
  could not be determined
- the **accent background** marks the row of the window you are looking at; when that window has
  no agent, no row is highlighted

`✓` is a per-pane tmux option (`@tmuxrd_finished`) the daemon sets the first time it sees the
agent work, so it survives a daemon restart and dies with the pane. It is consumed the moment the
agent's window becomes the one a client is viewing, so the checkmark is a notification: an agent
that finishes in the window you are watching never pins, and one that pins stops pinning when you
switch to it.

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
  columns are the scarce resource and rows are not. The strip lays the agents out as padded
  **tabs**; row 2 describes the selected agent. `1`–`9` switch to an agent in one keystroke.

Both form factors share the same palette: the sidebar has a background of its own so it stands out
from the agent pane, each agent row/tab sits on a secondary background, the row/tab you are looking
at is highlighted on an accent background, and the rest of the pane is filled with the sidebar
background. The accent background is the selection marker in both shapes — there is no `>`
pointer, so a desktop row starts one column further left and uses the width for its content.

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
put a one-line census (`agents=3 idle=1 working=1 blocked=1 finished=1`) there yourself.

## How state is detected

Detection is a registry of **`AgentDetector`** values (`internal/detect`). Every pane is offered to
each detector in turn as a `View{Command, Screen}` — the pane's resolved process name and its
visible screen — and the first detector that claims the pane names the agent and its state. A pane
no detector claims is **not an agent** and never reaches the sidebar, which is what keeps a shell,
`htop` or a plain `sleep` out of the list. Adding an agent is adding a value to the slice, not
editing a name list and a `switch`.

A state is an iota — `Unknown`, `Idle`, `Working`, `Blocked`, `Finished` — rather than a string,
so the display code switches over a closed set. The rules each detector applies:

- **pi** (`PiDetector`) — blocked is structural: the invariant opening of the `ask-user-question`
  dialog's hint line, `Enter to select · ↑/↓ to navigate`. Every variant of that dialog leads with
  those two parts — a multi-select question adds `Space to toggle`, a multi-question one adds `Tab
  to switch questions`, and `n to add notes` drops while a text input has the keyboard; the Submit
  tab lists no options at all but is still waiting on you — so the prefix covers them all. It also
  works in a narrow pane, because the TUI clips the hint line from the right and keeps its prefix
  (the earlier exact-footer match needed ~110 columns). Options alone are never enough: an agent's
  own markdown checklist is not a question, and an answered question is rendered into the
  transcript as a scalar summary, so the hint line does not linger. (English locale only.)
- **grok** (`GrokDetector`) — rules taken from captures of the real CLI (they are the fixtures in
  `features/testdata/grok-*.txt`): blocked is the two cards that hold a turn open — an approval
  card, which counts its options (`1/4:select │ Tab:next option │ …`), and the question tool,
  which draws no counter (its rows are `1 (◇) …`) and is matched by its footer
  (`Tab:next answer │ Esc:scrollback`) and its status line (`Waiting on answers for …`). A live
  status line carrying `[stop]` under a braille spinner is working; the resting hint line
  (`Ctrl+x:shortcuts`) under the prompt box is idle. grok has a detector of its own because its
  footer names the permission mode — `Grok 4.7 (high) · always-approve` — and the shared keyword
  rules match `approve`.
- **other agents** (`genericDetector`, one per name) — the shared rules: a braille frame is working,
  a small set of unambiguous prompt strings (`[y/n]`, `yes/no`, …) is blocked, a prompt tail or a
  shortcuts footer is idle. Word matching was tried and removed: it fired on ordinary text such as
  token counters and paths.
- **idle** everywhere — a shell prompt, or a pane whose foreground process is no longer the agent.

On top of the screen state, the daemon records whether the pane's agent has **worked at all**: the
first tick it sees a pane working it sets the pane option `@tmuxrd_finished`. `Detect` reads that
back and promotes an idle pinned pane to the **Finished** state, which is the `✓`. The pin is a
**notification**, so the daemon clears it as soon as the pane's window is the current window of a
session that has an attached client (`tmux.Pane.Viewed`) — what you are looking at needs no pin,
and a detached session's current window is not "viewed" by anyone, so its pins survive until you
attach. `Working` and `Blocked` win over `Finished`: a live turn or a question is never masked by
the pin.

The timer is owned by the daemon and measures the **current** working period: it starts when the
agent starts working, is frozen when it stops, and restarts from zero the next time the agent
starts working. Unlike the pin it is not persisted, so a daemon restart resets the timer (but not
the checkmark).

## Development

```sh
make build        # bin/tmuxrd + cmd/fakeagent/fakeagent
make integration  # the godog suite (58 scenarios)
make vet
```

`make integration` is hermetic: every scenario runs on its own `tmux -L godog-*` server with
`-f /dev/null`, so it never touches a real tmux server. It needs `tmux` and `go` on `PATH`.

A scenario can pin a pane to a synthetic state (`working`, `blocked`, …) or replay a real pane
capture: a `screen` column in an `agent windows` table makes the fake agent render that file
verbatim (`fakeagent -screen-file`), so detection is asserted against the bytes a real TUI drew.
`features/testdata/pi-question-2026-10-15.txt` is a live pi question and
`features/testdata/grok-{idle,working,blocked}.txt` are captures of the real grok CLI; the grok
ones are what `GrokDetector` was written from and what `features/agent_detection.feature` asserts.

## Architecture

The pieces, and the split between them: a daemon in `cmd/tmuxrd` is the only writer — it polls
`internal/tmux` (the single tmux boundary) every 500 ms, turns each screen into a state with
`internal/detect`, reshapes sidebars with `internal/layout`, and pushes snapshots down a Unix
socket. The sidebar panes run `tmuxrd client`, which only draws; `tmuxenv` and `cmd/fakeagent` are
the harness that runs the real daemon and client against an isolated server in the tests.

A [citydiff](https://github.com/naqerl/citydiff) tour walks that split step by step — dependency
level, declarations, then call paths. Two are published on the hub at
<https://citydiff.naqerl.com/o/71ce56db-1794-4eb7-af57-474f8196c483/>:

- **tmuxrd: how the sidebar knows** — the architecture, walked over the tree at `main` (a
  citydiff *snapshot*: one ref, nothing to diff): the daemon/client split, the tmux boundary, the
  detector registry and the state iota, pi's question rule, grok's captures, the glyph layer,
  adaptive layout, navigation and the test harness.
  <https://citydiff.naqerl.com/o/71ce56db-1794-4eb7-af57-474f8196c483/?tour=tmuxrd-how-the-sidebar-knows>
- **tmuxrd: one detector per agent** (`v0.3.0..v0.4.0`) — the same ground as a *change*: what the
  detector registry replaced, and why grok needed a detector of its own.
  <https://citydiff.naqerl.com/o/71ce56db-1794-4eb7-af57-474f8196c483/?tour=tmuxrd-one-detector-per-agent>

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

- pi's blocked detection matches the dialog's English hint line; a non-English locale would not be
  recognised. While the dialog is collapsed (`Ctrl+]`) it renders a single `… to expand · Esc to
  cancel` row, which is not specific enough to anchor on and is reported unknown.
- Working detection is braille-spinner only. An agent that shows no braille spinner while working
  will not be reported as working.
- The `✓` marker is one per **pane**, not per agent process: quitting pi and starting a new one in
  the same pane keeps the `✓` until it works again or you view the pane.
- "Viewed" is per session: a window counts as seen when it is its session's current window *and*
  that session has an attached client. Agents running in detached sessions keep their `✓`.
- The timer's values are in the daemon's memory, so restarting the daemon resets them.
