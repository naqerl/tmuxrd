// Package sessions records which coding-agent conversation is running in
// which tmux session, so `tmuxrd resume` can start them again after the tmux
// server dies.
//
// Grok already publishes every open tab in ~/.grok/active_sessions.json, keyed
// by pid and the process starttime. Pi keeps its session id in memory, so the
// installer adds an extension that writes one JSON file per pi process. The
// daemon joins either source to the pane list and rewrites agents.json only
// when the set changes. The file is left in place when the daemon exits: a
// dead tmux server is the case it exists for.
package sessions

import (
	"os"
	"path/filepath"
	"time"
)

// Socket names the tmux server a record belongs to. Kind is "path" (-S),
// "name" (-L), or "default" (a bare tmux).
type Socket struct {
	Kind  string
	Value string
}

func (s Socket) key() string {
	if s.Kind == "" {
		s.Kind = "default"
	}
	return s.Kind + "\x00" + s.Value
}

// Record is one agent pane that can be started again.
type Record struct {
	Agent       string    `json:"agent"`
	SessionID   string    `json:"session_id"`
	SessionFile string    `json:"session_file,omitempty"`
	Cwd         string    `json:"cwd"`
	PID         int       `json:"pid"`
	PIDStart    int64     `json:"pid_start"`
	SocketKind  string    `json:"socket_kind"`
	SocketValue string    `json:"socket,omitempty"`
	Session     string    `json:"tmux_session"`
	Window      string    `json:"window"`
	WindowName  string    `json:"window_name,omitempty"`
	PaneID      string    `json:"pane_id,omitempty"`
	Seen        time.Time `json:"seen"`
}

func (r Record) socket() Socket { return Socket{Kind: r.SocketKind, Value: r.SocketValue} }

// Pane is the slice of a tmux pane the ledger needs. It is not internal/tmux.Pane,
// so this package can be tested without a server.
type Pane struct {
	ID         string
	PID        string
	Cmd        string
	Path       string
	Session    string
	Window     string
	WindowName string
	Title      string
	Sidebar    bool
}

// Agent is one pane the detector claimed.
type Agent struct {
	PaneID     string
	Agent      string
	Session    string
	Window     string
	WindowName string
	Path       string
}

// Roots is where session facts live. Tests point them at a temp directory.
type Roots struct {
	StateDir string
	GrokHome string
	Proc     string
}

// DefaultRoots reads the real grok home, the tmuxrd state dir, and /proc.
func DefaultRoots() Roots {
	return Roots{StateDir: defaultStateDir(), GrokHome: defaultGrokHome(), Proc: "/proc"}
}

func defaultStateDir() string {
	if d := os.Getenv("TMUXRD_STATE_DIR"); d != "" {
		return d
	}
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "tmuxrd")
}

func defaultGrokHome() string {
	if h := os.Getenv("GROK_HOME"); h != "" {
		return h
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".grok")
}

// LedgerPath is agents.json, the file `tmuxrd resume` reads after the tmux
// server is gone.
func (r Roots) LedgerPath() string { return filepath.Join(r.StateDir, "agents.json") }

func (r Roots) proc() string {
	if r.Proc != "" {
		return r.Proc
	}
	return "/proc"
}
