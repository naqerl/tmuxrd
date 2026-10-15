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
}

// Detect classifies every agent pane of the server, in tmux order. A pane no
// detector claims is not an agent and does not appear at all — that is the
// difference between "a pane" and "an agent's pane".
func Detect(s tmux.Server) []Result {
	panes, err := s.ListPanes()
	if err != nil {
		return nil
	}
	var out []Result
	for _, p := range panes {
		fc := foregroundCommand(p.PID)
		v := View{Command: resolveCommand(p.Cmd, fc), Screen: s.Capture(p.ID)}
		agent, st, ok := classify(v)
		if !ok {
			continue
		}
		// The foreground process is gone, or is back at a shell: whatever the
		// screen still shows, nobody is working — a leftover TUI frame must not
		// keep claiming "working".
		if fc == "" || isShell(fc) {
			st = Idle
		}
		// Promote an idle pinned pane to the finished state. Working and blocked
		// win: a live turn or a question is never masked by the pin.
		if p.Pinned && (st == Idle || st == Unknown) {
			st = Finished
		}
		out = append(out, Result{
			PaneID: p.ID, Agent: agent, State: st, Path: p.Path,
			Session: p.Session, Window: p.Window, WindowName: p.WindowName,
			Pinned: p.Pinned, Viewed: p.Viewed,
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
