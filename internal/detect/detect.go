package detect

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/naqerl/tmuxrd/internal/tmux"
)

type State string

const (
	Idle    State = "idle"
	Working State = "working"
	Blocked State = "blocked"
	Unknown State = "unknown"
)

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
}

var agents = []string{"pi", "claude", "codex", "opencode", "cursor", "kimi", "hermes", "gemini", "grok"}

func isAgentCmd(cmd string) string {
	for _, a := range agents {
		if strings.EqualFold(cmd, a) {
			return a
		}
	}
	return ""
}

// piQuestionFooter is the exact last line of pi's multiple-choice prompt,
// captured verbatim from a real pi question. Together with the numbered "[ ]"
// options above it, it is the only state in which pi is waiting on the user.
const piQuestionFooter = "Enter to select · ↑/↓ to navigate · Space to toggle · n to add notes · Esc to cancel · Ctrl+] to collapse"

// piOptionRe matches one numbered checkbox option, e.g. "❯ 1. [ ] Tier 1" or
// "  2. [x] Tier 2". \s matches newlines too, so an option that wraps between
// the number and the box still counts.
var piOptionRe = regexp.MustCompile(`\d+\.\s*\[[ xX]\]`)

// piAwaitingAnswer reports whether the screen is pi's multi-select question.
//
// It requires BOTH pi's exact footer help AND at least two numbered "[ ]"
// options. Requiring both is what makes it precise: an agent's own markdown
// checklist has the options but never the footer, and the footer alone is
// specific to this prompt. Loose keyword matching ("option", "select", "1.",
// "working") matched ordinary text on screen and produced false positives.
//
// Whitespace is ignored when matching the footer so a terminal that wraps it
// mid-word still matches. Note the limit this cannot cover: if the pane is too
// narrow for the footer the TUI *truncates* it, and then it is genuinely not on
// screen (pi's own footer needs ~110 columns).
func piAwaitingAnswer(screen string) bool {
	if !strings.Contains(squeeze(screen), piQuestionFooterKey) {
		return false
	}
	return len(piOptionRe.FindAllString(screen, -1)) >= 2
}

// squeeze removes every whitespace character, so text that the terminal wrapped
// mid-word still matches and spacing differences are irrelevant.
func squeeze(s string) string { return strings.Join(strings.Fields(s), "") }

// piQuestionFooterKey is the footer with all whitespace removed.
var piQuestionFooterKey = squeeze(piQuestionFooter)

func classifyScreen(screen, agent string) State {
	lines := strings.Split(screen, "\n")
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
	// Strong idle: shell prompt visible at the end.
	if len(nz) > 0 {
		last := strings.TrimSpace(nz[len(nz)-1])
		if strings.HasSuffix(last, "$ ") || strings.HasSuffix(last, "% ") || strings.HasSuffix(last, "# ") || strings.HasSuffix(last, ">") || strings.HasSuffix(last, "❯") {
			return Idle
		}
	}
	// pi waits on the user in exactly one UI, matched structurally; other
	// agents fall back to the keyword set. Blocked is checked before working so
	// a question is never masked by an incidental spinner.
	if agent == "pi" {
		if piAwaitingAnswer(screen) {
			return Blocked
		}
	} else if blockedKeywords(nz) {
		return Blocked
	}
	// Working is the agent's braille spinner animation and nothing else. Word
	// patterns ("working", "thinking", "running", "waiting", "...") matched
	// ordinary text on screen — a token counter, a path, a file scrolled past —
	// so panes were reported as working when they were not. Scanning only the
	// tail avoids matching old output that has scrolled up.
	for _, l := range nz {
		if strings.ContainsAny(l, brailleSpinner) {
			return Working
		}
	}
	joined := strings.ToLower(strings.Join(nz, "\n"))
	// idle when the herdr-style footer is present
	if strings.Contains(joined, "ctrl+.:shortcuts") {
		return Idle
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

// blockedKeywords is the fallback for agents other than pi.
func blockedKeywords(nz []string) bool {
	joined := strings.ToLower(strings.Join(nz, "\n"))
	for _, p := range blockedPats {
		if strings.Contains(joined, p) {
			return true
		}
	}
	return false
}

var blockedPats = []string{
	"[y/n]", "yes/no", "approve", "allow?", "permission", "select:",
	"❯ 1.", "choose an option", "action required",
	"yes, proceed", "no, reject", "ctrl+o:yolo",
	"tab:scrollback", "shift+x:dismiss",
}

// brailleSpinner is every Unicode braille pattern (U+2801..U+28FF). All the
// spinner animations in use fall in this range — bubbles' MiniDot
// (⠏⠋⠙⠹⠸⠼⠴⠦⠧⠇), Dot (⣾⣽⣻⢿⡿⣟⣯⣷) and friends — so matching the block
// covers every frame without having to track each animation's glyph set.
var brailleSpinner = func() string {
	var b strings.Builder
	for r := rune(0x2801); r <= 0x28FF; r++ {
		b.WriteRune(r)
	}
	return b.String()
}()

func isShell(s string) bool {
	switch strings.ToLower(s) {
	case "bash", "zsh", "fish", "sh", "dash":
		return true
	}
	return false
}

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

func Detect(s tmux.Server) []Result {
	panes, err := s.ListPanes()
	if err != nil {
		return nil
	}
	var out []Result
	for _, p := range panes {
		cmd := p.Cmd
		if cmd == "" || isAgentCmd(cmd) == "" {
			if fc := foregroundCommand(p.PID); fc != "" {
				cmd = fc
			}
		}
		agent := isAgentCmd(cmd)
		if agent == "" {
			continue
		}
		screen := s.Capture(p.ID)
		st := classifyScreen(screen, agent)
		fc := foregroundCommand(p.PID)
		if fc == "" || isShell(fc) {
			st = Idle
		}
		out = append(out, Result{PaneID: p.ID, Agent: agent, State: st, Path: p.Path, Session: p.Session, Window: p.Window, WindowName: p.WindowName})
	}
	return out
}
