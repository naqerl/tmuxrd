package detect

import (
	"regexp"
	"strings"
	"unicode"
)

// View is what a detector gets to look at: the process that owns the pane and
// the text the user can see.
type View struct {
	// Command is the resolved process name: the pane's pane_current_command, or,
	// when the pane runs a shell, that shell's foreground process.
	Command string
	// Screen is the visible viewport as tmux hands it over (capture-pane -p):
	// no scrollback, exactly what the user is looking at.
	Screen string
}

// AgentDetector recognises the panes of one coding agent and says what the
// agent is doing.
//
// The registry is a slice of these and Detect calls each one in turn, so adding
// an agent is adding a value, not editing a switch. Ownership lives in the
// detector, not in a name list: Detect asks each detector "is this pane yours,
// and what is it doing?". A pane that no detector claims is not an agent at
// all, and never reaches the sidebar.
type AgentDetector interface {
	// Agent is the name the sidebar shows for this detector's panes.
	Agent() string
	// Detect classifies a pane. ok=false means "not my pane" — the dispatcher
	// tries the next detector.
	Detect(v View) (State, bool)
}

// Detectors is the registry, in match order. Order only matters between
// detectors that could claim the same command, which is why each built-in
// agent has one detector of its own — grok in particular must never fall
// through to the generic keyword rules, whose "approve" matches grok's
// "always-approve" footer.
var Detectors = []AgentDetector{
	PiDetector{},
	GrokDetector{},
	genericDetector{"claude"},
	genericDetector{"codex"},
	genericDetector{"opencode"},
	genericDetector{"cursor"},
	genericDetector{"kimi"},
	genericDetector{"hermes"},
	genericDetector{"gemini"},
}

// classify runs the registry. It returns the name of the agent that claimed the
// pane and the state it reported.
func classify(v View) (agent string, st State, ok bool) {
	for _, d := range Detectors {
		if st, ok := d.Detect(v); ok {
			return d.Agent(), st, true
		}
	}
	return "", Unknown, false
}

// tailNonEmpty returns the last n non-empty lines of a screen, with trailing
// blanks trimmed off each. Most rules only care about what is on screen near
// the bottom; anything scrolled up is history.
func tailNonEmpty(screen string, n int) []string {
	var out []string
	for _, l := range strings.Split(screen, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, strings.TrimRight(l, " \t"))
		}
	}
	if len(out) > n {
		out = out[len(out)-n:]
	}
	return out
}

// lastNonEmpty is the final non-empty line, trimmed.
func lastNonEmpty(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.TrimSpace(lines[len(lines)-1])
}

// promptTail reports whether a line ends like a shell or agent prompt, which is
// the strongest idle signal there is: the agent has handed the terminal back.
func promptTail(line string) bool {
	for _, s := range []string{"$ ", "% ", "# ", ">", "❯"} {
		if strings.HasSuffix(line, s) {
			return true
		}
	}
	return false
}

// leadingPrompt reports whether a line starts with a prompt glyph — the tail of
// a TUI that has drawn its input box rather than handed over a shell.
func leadingPrompt(line string) bool {
	return strings.HasPrefix(line, "❯") || strings.HasPrefix(line, ">") || strings.HasPrefix(line, "pi ")
}

// hasBraille reports whether any line carries a braille pattern. Every spinner
// animation in use falls in that block (U+2801..U+28FF): bubbles' MiniDot
// (⠏⠋⠙⠹⠸⠼⠴⠦⠧⠇), Dot (⣾⣽⣻⢿⡿⣟⣯⣷) and friends, so matching the block
// covers every frame without tracking each animation's glyph set. Word patterns
// ("working", "thinking", …) were tried and removed — they matched ordinary
// text such as a token counter or a path.
func hasBraille(lines []string) bool {
	for _, l := range lines {
		if strings.ContainsAny(l, brailleSpinner) {
			return true
		}
	}
	return false
}

// borderSpinnerRe matches a spinner embedded in a horizontal border — the shape
// pi draws in the editor's top border, "── ⠼ Working ──…". The label and the
// rest of the rule change with the state ("Working", "Retrying (1/3)…",
// "Compacting context…"), so the run of rule plus the braille frame is the
// invariant. Up to four dashes and one optional space keep it to the leading
// cells: a braille character deep inside ordinary text does not match.
var borderSpinnerRe = regexp.MustCompile(`─{1,4} ?[` + brailleSpinner + `]`)

// borderSpinner reports whether the screen carries a spinner in a border. It is
// how pi is recognised when the spinner is nowhere near the bottom of the pane:
// pi renders the slash-command menu *below* the editor's bottom border, which
// pushes the border out of the tail window while the turn is still running.
// Without this, opening the `/` menu mid-turn read as Unknown and closed the
// working period (the timer restarted from zero when the menu closed).
func borderSpinner(screen string) bool {
	return borderSpinnerRe.MatchString(screen)
}

// borderRow reports whether a line is a horizontal rule — one of the two rows
// that box an agent's editor ("──────────────"), optionally carrying the scroll
// indicator pi draws in it ("────── ↑ 12 more ──────"). Rendered tables and
// rules in a transcript can look like this, which is why it is only consulted
// after working and blocked have been ruled out.
func borderRow(line string) bool {
	dashes, other := 0, 0
	for _, r := range strings.TrimSpace(line) {
		switch {
		case r == '─':
			dashes++
		case unicode.IsSpace(r):
		default:
			other++
		}
	}
	// pi's editor box is 9 dashes wide even in a narrow pane; the noise budget
	// (arrows, a digit count) must stay a small fraction of the rule.
	return dashes >= 8 && other <= dashes/4
}

// restingState is the shared tail of the rule order: an agent that is neither
// blocked nor working is idle when it has drawn a resting prompt or its
// shortcuts footer, and unknown when the screen said nothing at all.
func restingState(lines []string) State {
	joined := strings.ToLower(strings.Join(lines, "\n"))
	if strings.Contains(joined, "ctrl+.:shortcuts") {
		return Idle
	}
	if leadingPrompt(lastNonEmpty(lines)) {
		return Idle
	}
	return Unknown
}

// brailleSpinner is the whole braille block as a string, for ContainsAny.
var brailleSpinner = func() string {
	var b strings.Builder
	for r := rune(0x2801); r <= 0x28FF; r++ {
		b.WriteRune(r)
	}
	return b.String()
}()

// squeeze removes every whitespace character, so text a narrow terminal wrapped
// mid-word still matches and spacing differences are irrelevant.
func squeeze(s string) string { return strings.Join(strings.Fields(s), "") }
