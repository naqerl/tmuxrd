package detect

import "strings"

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
	if piAwaitingAnswer(v.Screen) {
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
