package detect

import (
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/naqerl/tmuxrd/internal/tmux"
)

// Result is one agent pane the sidebar can draw.
type Result struct {
	PaneID     string
	Agent      string
	State      State
	Path       string
	Session    string
	Window     string
	WindowName string
	// Worked is how long the current working period has run: it is reset each
	// time the agent starts working, and frozen when it stops. The daemon owns
	// it; Detect leaves it zero.
	Worked time.Duration
	// Pinned is the pane's @tmuxrd_finished option: the daemon sets it the first
	// time it sees the pane work, so a pane the user has not looked at can be
	// drawn as Finished rather than Idle. It is a tmux fact (it survives a
	// daemon restart and dies with the pane), which is why it is a field of its
	// own and not part of State: the daemon still has to clear it when the pane
	// is viewed.
	Pinned bool
	// Viewed mirrors tmux.Pane.Viewed: the pane is in the window a client is
	// looking at, so the daemon can consume the pin there.
	Viewed bool
	// Unread is true when the detector could not name the state (it answered
	// Unknown) and what State holds is a guess — the pin may have promoted it to
	// Finished. The daemon uses it to tell "the agent stopped" from "the screen
	// said nothing": a working period is held open across an Unread tick instead
	// of being closed, so a transient overlay cannot reset the timer.
	Unread bool
}

// Detect classifies every agent pane of the server, in tmux order. A pane no
// detector claims is not an agent and does not appear at all — that is the
// difference between "a pane" and "an agent's pane".
//
// It is a thin wrapper over DetectPanes for callers that hold no pane list; it
// fetches the panes itself. Callers that already have one (the daemon refresh)
// use DetectPanes so the server is listed once per pass, not once per phase.
func Detect(s tmux.Server) []Result {
	panes, err := s.ListPanes()
	if err != nil {
		return nil
	}
	return DetectPanes(s, panes)
}

// captureFresh is how long a cached classification may stand in for
// capture-pane when the pane's fingerprint has not moved. A TUI redraws in
// place without touching cursor or history (and tmux has no pane_activity
// format), so a match is not proof the screen is the same; the bound keeps a
// state change visible within a second while an idle pane is not captured on
// every 500ms tick.
const captureFresh = time.Second

// screenEntry is one pane's last successful classification.
type screenEntry struct {
	fp    string
	at    time.Time
	agent string
	state State
}

// ScreenCache remembers the last classification per pane so a refresh can skip
// capture-pane while the fingerprint holds. It is not safe for concurrent use;
// the daemon calls it from the single refresh goroutine.
type ScreenCache struct {
	entries map[string]screenEntry
}

// NewScreenCache returns an empty cache.
func NewScreenCache() *ScreenCache {
	return &ScreenCache{entries: map[string]screenEntry{}}
}

func (c *ScreenCache) lookup(id, fp string, now time.Time) (screenEntry, bool) {
	if c == nil {
		return screenEntry{}, false
	}
	e, ok := c.entries[id]
	if !ok || e.fp != fp || now.Sub(e.at) >= captureFresh {
		return screenEntry{}, false
	}
	return e, true
}

func (c *ScreenCache) store(id, fp string, now time.Time, agent string, st State, ok bool) {
	if c == nil || !ok {
		return
	}
	if c.entries == nil {
		c.entries = map[string]screenEntry{}
	}
	c.entries[id] = screenEntry{fp: fp, at: now, agent: agent, state: st}
}

func (c *ScreenCache) prune(panes []tmux.Pane) {
	if c == nil || len(c.entries) == 0 {
		return
	}
	live := make(map[string]struct{}, len(panes))
	for _, p := range panes {
		live[p.ID] = struct{}{}
	}
	for id := range c.entries {
		if _, ok := live[id]; !ok {
			delete(c.entries, id)
		}
	}
}

// DetectPanes classifies the agent panes of an already-fetched pane list.
//
// It captures a pane's screen only when the pane could be an agent: the pane is
// not a tmuxrd sidebar, and resolveCommand — which reads /proc, never tmux —
// names a command the detector registry claims. capture-pane is the expensive
// step (one tmux process per pane), so skipping it for shells, sleeps, htop and
// the sidebars themselves turns the cost of a pass from "every pane on the
// server" into "every agent pane".
func DetectPanes(s tmux.Server, panes []tmux.Pane) []Result {
	return DetectPanesCached(s, panes, nil, time.Now(), nil)
}

// DetectPanesCached is DetectPanes with a fingerprint cache. A nil cache
// captures every agent pane, which is what summary and the client's fallback
// want. The daemon passes the cache it keeps across ticks.
//
// stop, when non-nil, is checked before each pane. A true result drops the
// rest of the pass so a newer refresh can re-list instead of finishing a
// snapshot that a window switch has already made stale.
func DetectPanesCached(s tmux.Server, panes []tmux.Pane, cache *ScreenCache, now time.Time, stop func() bool) []Result {
	if cache != nil {
		defer cache.prune(panes)
	}
	var out []Result
	for _, p := range panes {
		if stop != nil && stop() {
			return out
		}
		// tmuxrd's own sidebar panes are never agents. Skipping them here (as
		// well as before the capture below) means the pass never even considers
		// them, which is what the @tmuxrd_sidebar tag is for.
		if p.Sidebar {
			continue
		}
		fc := foregroundCommand(p.PID)
		cmd := resolveCommand(p.Cmd, fc)
		// The command filter is the whole point: a pane no detector could claim
		// is skipped before its screen is captured. ClaimsCommand is derived
		// from the same registry classify uses, so this can never disagree with
		// which panes are agents.
		if !ClaimsCommand(cmd) {
			continue
		}
		var (
			agent string
			st    State
			ok    bool
		)
		if e, hit := cache.lookup(p.ID, p.ContentFP, now); hit {
			agent, st, ok = e.agent, e.state, true
		} else {
			agent, st, ok = classify(View{Command: cmd, Screen: s.Capture(p.ID)})
			cache.store(p.ID, p.ContentFP, now, agent, st, ok)
		}
		if !ok {
			continue
		}
		// The foreground process is gone, or is back at a shell: whatever the
		// screen still shows, nobody is working — a leftover TUI frame must not
		// keep claiming "working".
		if fc == "" || isShell(fc) {
			st = Idle
		}
		unread := st == Unknown
		// Promote an idle pinned pane to the finished state. Working and blocked
		// win: a live turn or a question is never masked by the pin. Unknown also
		// promotes — a pane whose screen the detector cannot read has still
		// stopped, and losing the ✓ for it would be worse than the guess. Unread
		// keeps that guess distinguishable, so a transient overlay over a live run
		// does not close its working period (see the daemon's unknownGrace).
		if p.Pinned && (st == Idle || st == Unknown) {
			st = Finished
		}
		out = append(out, Result{
			PaneID: p.ID, Agent: agent, State: st, Path: p.Path,
			Session: p.Session, Window: p.Window, WindowName: p.WindowName,
			Pinned: p.Pinned, Viewed: p.Viewed, Unread: unread,
		})
	}
	return out
}

// resolveCommand names the process that owns the pane. A pane running a shell
// has the agent as that shell's foreground process; anywhere else the pane's own
// command is the truth.
func resolveCommand(paneCmd, foreground string) string {
	if foreground == "" {
		return paneCmd
	}
	if paneCmd == "" || isShell(paneCmd) {
		return foreground
	}
	return paneCmd
}

func isShell(s string) bool {
	switch strings.ToLower(s) {
	case "bash", "zsh", "fish", "sh", "dash":
		return true
	}
	return false
}

// foregroundCommand returns the name of the process in the foreground process
// group of pid, or "" when the group is gone (or unreadable).
func foregroundCommand(shellPid string) string {
	pid, err := strconv.Atoi(shellPid)
	if err != nil || pid <= 0 {
		return ""
	}
	stat, err := os.ReadFile(fmtProc(pid) + "/stat")
	if err != nil {
		return ""
	}
	r := strings.LastIndex(string(stat), ")")
	if r < 0 || r+2 >= len(stat) {
		return ""
	}
	fields := strings.Fields(string(stat)[r+2:])
	if len(fields) < 6 {
		return ""
	}
	if fields[0] == "Z" || fields[0] == "X" {
		return ""
	}
	tpgid, err := strconv.Atoi(fields[5])
	if err != nil || tpgid <= 0 {
		return ""
	}
	targetStat, err := os.ReadFile(fmtProc(tpgid) + "/stat")
	if err != nil {
		return ""
	}
	tr := strings.LastIndex(string(targetStat), ")")
	if tr < 0 || tr+2 >= len(targetStat) {
		return ""
	}
	tfields := strings.Fields(string(targetStat)[tr+2:])
	if len(tfields) == 0 || tfields[0] == "Z" || tfields[0] == "X" {
		return ""
	}
	data, err := os.ReadFile(fmtProc(tpgid) + "/comm")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func fmtProc(pid int) string {
	return "/proc/" + strconv.Itoa(pid)
}
