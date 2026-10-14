package detect

import (
	"strings"

	"github.com/naqerl/terdr/internal/tmux"
)

type State string

const (
	Idle    State = "idle"
	Working State = "working"
	Blocked State = "blocked"
	Unknown State = "unknown"
)

type Result struct {
	PaneID string
	Agent  string
	State  State
	Path   string
}

var agents = []string{"pi", "claude", "codex", "opencode", "cursor", "kimi", "hermes", "gemini"}

func isAgentCmd(cmd string) string {
	for _, a := range agents {
		if strings.EqualFold(cmd, a) {
			return a
		}
	}
	return ""
}

func classifyTail(tail string) State {
	lines := strings.Split(tail, "\n")
	// last 6 non-empty
	var nz []string
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			nz = append(nz, strings.TrimRight(l, " \t"))
		}
	}
	if len(nz) > 6 {
		nz = nz[len(nz)-6:]
	}
	joined := strings.ToLower(strings.Join(nz, "\n"))
	// blocked strict patterns
	blockedPats := []string{"[y/n]", "yes/no", "approve", "allow?", "permission", "select:", "❯ 1.", "1.", "option"}
	for _, p := range blockedPats {
		if strings.Contains(joined, p) {
			// but ensure a choice/prompt present; strict
			return Blocked
		}
	}
	// working
	workPats := []string{"working", "thinking", "…", "...", "running", "executing"}
	for _, p := range workPats {
		if strings.Contains(joined, p) {
			return Working
		}
	}
	// spinner glyphs
	if len(nz) > 0 {
		for _, r := range nz[len(nz)-1:] {
			if strings.ContainsAny(r, "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏ ") {
				return Working
			}
		}
	}
	// idle prompt
	last := ""
	if len(nz) > 0 {
		last = strings.TrimSpace(nz[len(nz)-1])
	}
	if strings.HasPrefix(last, "❯") || strings.HasPrefix(last, ">") || strings.HasPrefix(last, "pi ") {
		return Idle
	}
	return Unknown
}

func Detect() []Result {
	panes, err := tmux.ListPanes()
	if err != nil {
		return nil
	}
	var out []Result
	for _, p := range panes {
		agent := isAgentCmd(p.Cmd)
		if agent == "" {
			continue
		}
		screen := tmux.Capture(p.ID)
		out = append(out, Result{PaneID: p.ID, Agent: agent, State: classifyTail(screen), Path: p.Path})
	}
	return out
}