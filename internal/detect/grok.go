package detect

import (
	"regexp"
	"strings"
)

// GrokDetector recognises the grok CLI (Grok Build, ~/.grok/bin/grok).
//
// The rules come from captures of the real TUI, kept as fixtures in
// features/testdata/grok-*.txt:
//
//	working  ⠼ Sleep eight seconds then print DONE… 2.0s   4.0s ⇣18.9k [↓][stop]
//	blocked  ┃  ›  Allow Edit to /tmp/grokkit/hello.txt?
//	         ┃  1 (●) Yes, and don't ask again for anything (always-approve mode)
//	         ┃  4 (○) No, reject (type to add feedback)
//	         1/4:select  │  Tab:next option  │  Ctrl+o:always-approve  │  …
//	         ◇ Waiting on answers for What should we work on next?   7.3s ⇣19.7k [stop]
//	         ┃  1 (◇) A coding task (Recommended)
//	         Tab:next answer  │  Esc:scrollback  │  Shift+x:dismiss
//	idle     │ ❯                                                                  │
//	         ╰──────────────────────── Grok 4.7 (high) · always-approve ────────╯
//	         Shift+Tab:mode  │  Ctrl+x:shortcuts
//
// The footer naming the permission mode ("always-approve") is exactly why grok
// must have a detector of its own: the generic keyword rules below match
// "approve", and a real grok pane at its prompt was reported as blocked.
type GrokDetector struct{}

func (GrokDetector) Agent() string { return "grok" }

// grokWaitingRe matches the cards that hold a grok turn open — the two shapes
// the CLI draws when it is waiting on the user:
//
//	approval  1/4:select  │  Tab:next option  │  Ctrl+o:always-approve  │  …
//	question  Tab:next answer  │  Esc:scrollback  │  Shift+x:dismiss
//
// The question tool has no option counter (its rows are "1 (◇) …"), so the
// counter alone missed it and a live question read as unknown; its own footer
// and its live status line ("◇ Waiting on answers for …") are what match it.
// Both cards are "waiting for you", which is grok's own word for the state
// (docs/user-guide/05-configuration.md, the OSC 7501 program-status section).
var grokWaitingRe = regexp.MustCompile(`\d+/\d+:select|Tab:next answer|Waiting on answers for`)

// grokStopAffordance is the cancel button on grok's live status line — present
// only while a turn is open. Combined with a braille glyph it distinguishes a
// live turn from a transcript that merely quotes "[stop]".
const grokStopAffordance = "[stop]"

// grokShortcuts is the resting hint line under the prompt box. A working turn
// draws the same line with extra affordances ("Ctrl+c:cancel"), which is why
// the working rule is checked first.
const grokShortcuts = "Ctrl+x:shortcuts"

func (GrokDetector) Detect(v View) (State, bool) {
	if !strings.EqualFold(v.Command, "grok") {
		return Unknown, false
	}
	if grokWaitingRe.MatchString(v.Screen) {
		return Blocked, true
	}
	if strings.Contains(v.Screen, grokStopAffordance) && hasBraille(tailNonEmpty(v.Screen, 15)) {
		return Working, true
	}
	if strings.Contains(v.Screen, grokShortcuts) {
		return Idle, true
	}
	return Unknown, true
}
