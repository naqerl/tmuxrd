package sessions

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Live is a process that currently holds an agent session.
type Live struct {
	Agent     string
	SessionID string
	PID       int
	PIDStart  int64
}

// Action is one resume decision. Command is empty when the row is skipped.
type Action struct {
	Record  Record
	Command string
	Skip    string
}

// Plan decides which ledger rows to start. A row whose process is still the
// one we recorded is left alone. A session id that some other live process
// already holds was resumed already.
func Plan(rows []Record, alive func(pid int, start int64) bool, open func(agent, sessionID string) bool) []Action {
	sorted := append([]Record(nil), rows...)
	sort.Slice(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if a.Session != b.Session {
			return a.Session < b.Session
		}
		ai, aerr := strconv.Atoi(a.Window)
		bi, berr := strconv.Atoi(b.Window)
		if aerr == nil && berr == nil && ai != bi {
			return ai < bi
		}
		if a.Window != b.Window {
			return a.Window < b.Window
		}
		return a.Agent < b.Agent
	})
	var out []Action
	for _, r := range sorted {
		if r.SessionID == "" || (r.Agent != "pi" && r.Agent != "grok") {
			continue
		}
		act := Action{Record: r, Command: Command(r)}
		if alive != nil && alive(r.PID, r.PIDStart) {
			act.Command = ""
			act.Skip = "still running"
		} else if open != nil && open(r.Agent, r.SessionID) {
			act.Command = ""
			act.Skip = "session is already open"
		}
		out = append(out, act)
	}
	return out
}

// Command is the process tmux should run in the restored pane.
func Command(r Record) string {
	switch r.Agent {
	case "grok":
		return "grok --resume " + shellQuote(r.SessionID)
	case "pi":
		arg := r.SessionID
		if r.SessionFile != "" {
			arg = r.SessionFile
		}
		return "pi --session " + shellQuote(arg)
	default:
		return ""
	}
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if strings.ContainsAny(s, " \t\n'\"\\$`;&|<>()[]{}*?!") {
		return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
	}
	return s
}

// OpenSessions lists conversation ids that a live process still holds, from
// grok's registry and from pi's per-pid reports.
func OpenSessions(roots Roots) []Live {
	var out []Live
	rows, err := readGrok(roots.GrokHome)
	if err == nil {
		for _, r := range rows {
			if r.SessionID == "" || !Alive(roots.proc(), r.PID, r.PIDStart) {
				continue
			}
			out = append(out, Live{Agent: "grok", SessionID: r.SessionID, PID: r.PID, PIDStart: r.PIDStart})
		}
	}
	dir := filepath.Join(roots.StateDir, "pi")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSuffix(name, ".json"))
		if err != nil {
			continue
		}
		rep, ok := readPiReport(roots.StateDir, pid, 0)
		if !ok || !Alive(roots.proc(), pid, rep.PIDStart) {
			continue
		}
		out = append(out, Live{Agent: "pi", SessionID: rep.SessionID, PID: pid, PIDStart: rep.PIDStart})
	}
	return out
}
