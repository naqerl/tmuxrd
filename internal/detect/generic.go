package detect

import (
	"strings"
)

// genericDetector is the fallback for the agents tmuxrd knows by name but has
// no screen rule of their own for. It is one value per agent name, so the
// registry stays a plain list and adding a dedicated detector is a one-line
// swap; until then these agents keep the shared rules.
//
// The list is unchanged by the detector split on purpose: these agents have no
// captures here to check a tightened rule against, and the one loose word that
// did bite — "approve", from grok's "always-approve" permission-mode footer —
// is now unreachable because GrokDetector claims grok panes first.
type genericDetector struct{ name string }

func (d genericDetector) Agent() string { return d.name }

func (d genericDetector) Detect(v View) (State, bool) {
	if !strings.EqualFold(v.Command, d.name) {
		return Unknown, false
	}
	tail := tailNonEmpty(v.Screen, 6)
	if promptTail(lastNonEmpty(tail)) {
		return Idle, true
	}
	if blockedKeywords(tail) {
		return Blocked, true
	}
	if hasBraille(tail) {
		return Working, true
	}
	return restingState(tail), true
}

// blockedKeywords is the shared fallback for agents without a structural rule:
// the strings below are unambiguous enough that they only appear in a prompt
// that is waiting for the user.
func blockedKeywords(lines []string) bool {
	joined := strings.ToLower(strings.Join(lines, "\n"))
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
