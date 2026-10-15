package detect

import (
	"os"
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
	// Finished is true when the pane's agent has already done a working run:
	// the daemon records it as the pane's @tmuxrd_finished option the first
	// time it sees the agent work, so Detect reads it back from tmux and the
	// sidebar can show ✓ for a finished agent and ○ only for one that never
	// started. The daemon clears it again once the pane is viewed, so it is a
	// notification pin, not a permanent badge. It stays true while the agent
	// works again (the spinner wins the glyph); it dies with the pane.
	Finished bool
	// Viewed mirrors tmux.Pane.Viewed: the pane is in the window a client is
	// looking at. The daemon consults it to consume the Finished marker.
	Viewed bool
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

// piDialogHint is the invariant opening of the hint line pi's ask-user-question
// dialog renders at the bottom of every question tab. The rest of the line
// changes with the question's shape — "Space to toggle" appears only when the
// question is multi-select, "Tab to switch questions" only when the
// questionnaire holds more than one question, "n to add notes" drops while the
// notes editor or the "Type something." row holds the keyboard, and the Submit
// tab says "n to add a note" — but these two parts lead every variant.
//
// The strings are the extension's `en` locale (the default); a non-English
// locale is not matched.
const piDialogHint = "Enter to select · ↑/↓ to navigate"

// piDialogHintKey is piDialogHint with every whitespace character removed, so a
// pane that wraps the hint mid-word still matches.
var piDialogHintKey = squeeze(piDialogHint)

// piAwaitingAnswer reports whether the screen is pi's ask-user-question dialog,
// the one state in which pi is waiting on the user.
//
// It keys on the hint line's invariant prefix alone:
//
//   - A single-select question (the default) draws plain "N. label" rows with
//     no "[ ]" checkbox. The earlier matcher required the exact multi-select
//     footer *and* two numbered "[ ]" rows, so a live single-select question
//     was reported as unknown (features/testdata/pi-question-2026-10-15.txt is
//     such a capture).
//   - The Submit tab lists no numbered rows at all, yet the user is still the
//     one who must press Enter, so requiring options would miss it too.
//   - The dialog renders the hint through OneLineClippedText, which clips from
//     the right and keeps the prefix, so this also works in a narrow pane where
//     the old exact-footer match needed ~110 columns.
//
// The prefix is unique to the dialog: an answered question is rendered into the
// transcript as a scalar summary, not as the prompt, so the hint does not
// linger on screen after the user answers. The one state this misses is a
// collapsed dialog (Ctrl+]), whose single row reads "<key> to expand · Esc to
// cancel" — "Esc to cancel" alone is too generic to anchor on.
func piAwaitingAnswer(screen string) bool {
	return strings.Contains(squeeze(screen), piDialogHintKey)
}

// squeeze removes every whitespace character, so text that the terminal wrapped
// mid-word still matches and spacing differences are irrelevant.
func squeeze(s string) string { return strings.Join(strings.Fields(s), "") }

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
		out = append(out, Result{PaneID: p.ID, Agent: agent, State: st, Path: p.Path, Session: p.Session, Window: p.Window, WindowName: p.WindowName, Finished: p.Finished, Viewed: p.Viewed})
	}
	return out
}
