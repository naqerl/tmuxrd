package detect

import (
	"strings"
	"unicode"
)

// PiDetector recognises pi and says what its pane is doing.
//
// pi is the one agent whose "waiting on you" UI is matched structurally rather
// than by keywords: its ask-user-question dialog is the only thing it draws
// that can hold the turn open.
type PiDetector struct{}

func (PiDetector) Agent() string { return "pi" }

func (PiDetector) Detect(v View) (State, bool) {
	if !strings.EqualFold(v.Command, "pi") {
		return Unknown, false
	}
	tail := tailNonEmpty(v.Screen, 6)
	// Idle first: a prompt tail at the bottom means pi has handed the terminal
	// back, whatever is above it.
	if promptTail(lastNonEmpty(tail)) {
		return Idle, true
	}
	// Blocked before working, so a question is never masked by an incidental
	// spinner.
	if piAwaitingAnswer(tail) {
		return Blocked, true
	}
	if hasBraille(tail) {
		return Working, true
	}
	return restingState(tail), true
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

// piAwaitingAnswer reports whether pi's ask-user-question dialog is open. tail
// is the pane's last non-empty lines (Detect's own window): the dialog is drawn
// at the bottom of the pane, so its hint row is in there.
//
// The hint line's invariant prefix is all it keys on, because the rest of the
// line changes with the question's shape:
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
// The words themselves are not unique to the dialog: ordinary content can quote
// them — the write-up of this detector, `cat internal/detect/pi.go`, a diff of
// this file — and a pane showing any of those used to read as blocked while the
// rule substring-searched the whole viewport. So the hint must also be shaped
// like a row the dialog drew:
//
//   - it begins the row, once the padding and box chrome are trimmed, and
//   - the prefix is followed by the next "·"-separated hint, or by the end of
//     the row.
//
// Prose satisfies neither: "…invariant prefix (\"Enter to select · ↑/↓ to
// navigate\") with all whitespace squeezed out…" sits mid-sentence, and a
// wrapped quote that does begin a row is followed by the closing quote.
//
// Rows are glued together before the test, so a hint the pane wrapped mid-word
// still matches: the prefix spans the row break and squeeze removes it.
//
// The one state this still misses is a collapsed dialog (Ctrl+]), whose single
// row reads "<key> to expand · Esc to cancel" — "Esc to cancel" alone is too
// generic to anchor on.
func piAwaitingAnswer(tail []string) bool {
	rows := make([]string, len(tail))
	for i, line := range tail {
		rows[i] = trimRowChrome(line)
	}
	for i := range rows {
		joined := squeeze(strings.Join(rows[i:], "\n"))
		if !strings.HasPrefix(joined, piDialogHintKey) {
			continue
		}
		if rest := joined[len(piDialogHintKey):]; rest == "" || strings.HasPrefix(rest, "·") {
			return true
		}
	}
	return false
}

// trimRowChrome drops the padding a dialog row can carry — leading whitespace,
// and the box drawing of a bordered dialog — so the hint still begins the row.
func trimRowChrome(line string) string {
	return strings.TrimLeftFunc(line, func(r rune) bool {
		return unicode.IsSpace(r) || r == '|' || (r >= 0x2500 && r <= 0x259f)
	})
}
