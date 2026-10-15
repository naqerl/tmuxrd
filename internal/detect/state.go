package detect

import "encoding/json"

// State is what the sidebar draws for one pane. It is an iota rather than a
// string so the display code switches over a closed set — a typo in a string
// constant used to degrade silently to "unknown" instead of failing to build.
type State int

const (
	// Unknown: the pane belongs to an agent, but nothing on its screen said
	// which state it is in.
	Unknown State = iota
	// Idle: the agent is sitting at its prompt, waiting for input.
	Idle
	// Working: the agent is running a turn (its spinner is animating).
	Working
	// Blocked: the agent is waiting on the user — a question, an approval, a
	// sign-in.
	Blocked
	// Finished: the agent finished a run and the user has not looked at it
	// since, which is what the sidebar pins as ✓ (see Result.Pinned). No
	// detector returns this: a detector only sees the screen, and a finished
	// pane looks exactly like an idle one. The daemon knows which panes have
	// worked, records it on the pane, and Detect promotes an idle pinned pane to
	// Finished.
	Finished
)

// String is the log and wire form ("blocked" reads better than 3).
func (s State) String() string {
	switch s {
	case Idle:
		return "idle"
	case Working:
		return "working"
	case Blocked:
		return "blocked"
	case Finished:
		return "finished"
	default:
		return "unknown"
	}
}

// MarshalJSON keeps the daemon→client wire format readable, and unchanged from
// when State was a string type: an old client and a new daemon still agree on
// the state word while the upgrade is rolling through.
func (s State) MarshalJSON() ([]byte, error) { return json.Marshal(s.String()) }

// UnmarshalJSON is the counterpart; anything unrecognised reads as Unknown
// rather than failing the whole snapshot.
func (s *State) UnmarshalJSON(b []byte) error {
	var name string
	if err := json.Unmarshal(b, &name); err != nil {
		return err
	}
	switch name {
	case "idle":
		*s = Idle
	case "working":
		*s = Working
	case "blocked":
		*s = Blocked
	case "finished":
		*s = Finished
	default:
		*s = Unknown
	}
	return nil
}
