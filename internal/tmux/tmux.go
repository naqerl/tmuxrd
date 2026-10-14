// Package tmux wraps the tmux CLI behind a Server abstraction.
//
// Every command is issued against an explicit socket so a Server can never
// accidentally address a *different* tmux server than intended: a Server with
// a non-empty Socket passes -L <Socket>, and a Server with an empty Socket
// targets the process' default tmux server with a bare tmux (which is exactly
// what an unset TERDR_TMUX_SOCKET means). This keeps tests hermetic (they set
// Socket to a unique name) while production default-socket use needs no env.
package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// DefaultSocket is the socket name used in daemon socket *file* names for the
// default server. It is not passed to tmux -L (that would be a different,
// nonexistent server); it only namespaces the Unix socket file.
const DefaultSocket = "tmuxrd-default"

// EnvVar selects the socket for a Server resolved from the environment.
const EnvVar = "TERDR_TMUX_SOCKET"

// Server addresses one tmux server by socket name.
// The zero value (Socket == "") addresses the default tmux server (bare tmux).
type Server struct {
	Socket string // empty = default tmux server (bare tmux)
}

// NewServer returns a Server for the named socket. An empty name selects the
// default tmux server; a non-empty name is passed to tmux -L.
func NewServer(socket string) Server {
	return Server{Socket: socket}
}

// NewServerFromEnv resolves a Server from TERDR_TMUX_SOCKET. Absent or empty
// values resolve to the default tmux server (bare tmux).
func NewServerFromEnv() Server {
	return NewServer(os.Getenv(EnvVar))
}

// IsDefault reports whether this server is the process' default tmux instance.
func (s Server) IsDefault() bool { return s.Socket == "" }

// prefix builds the leading argv for a tmux invocation: bare tmux for the
// default server, "-L <Socket>" otherwise.
func (s Server) prefix() []string {
	if s.Socket == "" {
		return nil
	}
	return []string{"-L", s.Socket}
}

// SocketFile returns the name used to namespace this server's daemon socket
// file (the caller prefixes "tmuxrd-"). The default server maps to "default";
// named servers use their name.
func (s Server) SocketFile() string {
	if s.Socket == "" {
		return "default"
	}
	return s.Socket
}

// Run executes tmux against this server.
func (s Server) Run(args ...string) (string, error) {
	full := append(s.prefix(), args...)
	out, err := exec.Command("tmux", full...).Output()
	return strings.TrimRight(string(out), "\n"), err
}

// RunCombined is Run but captures stderr too, which the scripts/tests need to
// distinguish "index in use"-style failures from silence.
func (s Server) RunCombined(args ...string) (string, error) {
	full := append(s.prefix(), args...)
	var sb strings.Builder
	cmd := exec.Command("tmux", full...)
	cmd.Stdout = &sb
	cmd.Stderr = &sb
	err := cmd.Run()
	return strings.TrimRight(sb.String(), "\n"), err
}

// KillServer tears down this server only.
func (s Server) KillServer() { s.Run("kill-server") }

// HasServer reports whether a tmux server is listening for this Server.
func (s Server) HasServer() bool {
	_, err := s.Run("display-message", "-p", "#{session_name}")
	return err == nil
}

type Pane struct {
	ID         string
	PID        string
	Cmd        string
	Path       string
	Session    string
	Window     string
	WindowName string
}

const paneFmt = "#{session_name}\t#{window_index}\t#{window_name}\t#{pane_id}\t#{pane_pid}\t#{pane_current_command}\t#{pane_current_path}"

// ListPanes enumerates every pane on every window of every session.
func (s Server) ListPanes() ([]Pane, error) {
	out, err := s.Run("list-panes", "-a", "-F", paneFmt)
	if err != nil {
		return nil, err
	}
	var panes []Pane
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 7 {
			continue
		}
		panes = append(panes, Pane{
			Session: fields[0], Window: fields[1], WindowName: fields[2],
			ID: fields[3], PID: fields[4], Cmd: fields[5], Path: fields[6],
		})
	}
	return panes, nil
}

// Capture returns the visible contents of a pane (no scrollback).
func (s Server) Capture(paneID string) string {
	out, _ := s.Run("capture-pane", "-p", "-t", paneID)
	return out
}

// CurrentPane returns the id of the pane active for the calling client.
func (s Server) CurrentPane() string {
	out, _ := s.Run("display-message", "-p", "#{pane_id}")
	return strings.TrimSpace(out)
}

// CurrentTarget returns the pane, session and window currently in view. From
// inside a pane tmux resolves the session's *current* window, which is what the
// client is displaying, so this is the single source of truth for "which window
// am I looking at" — shared by every sidebar without any shared state.
func (s Server) CurrentTarget() (paneID, session, window string) {
	out, _ := s.Run("display-message", "-p", "#{pane_id}\t#{session_name}\t#{window_index}")
	f := strings.Split(strings.TrimSpace(out), "\t")
	if len(f) < 3 {
		return "", "", ""
	}
	return f[0], f[1], f[2]
}

// sidebarPaneInWindow returns the sidebar pane of the window holding paneID.
func (s Server) sidebarPaneInWindow(paneID string) string {
	out, _ := s.Run("list-panes", "-t", paneID, "-F", "#{pane_id}\t#{@tmuxrd_sidebar}")
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, "\t")
		if len(f) == 2 && strings.TrimSpace(f[1]) == "1" {
			return f[0]
		}
	}
	return ""
}

// GoToAgent switches the view to the pane's window and session (switch-client
// handles the cross-session case; select-window moves the view when no client
// is attached) but leaves the keyboard on that window's sidebar pane, so the
// sidebar can keep navigating after the view has moved.
func (s Server) GoToAgent(paneID string) {
	s.Run("switch-client", "-t", paneID)
	s.Run("select-window", "-t", paneID)
	if sb := s.sidebarPaneInWindow(paneID); sb != "" {
		s.Run("select-pane", "-t", sb)
		return
	}
	s.Run("select-pane", "-t", paneID)
}

// SelectPane focuses a pane: switch the client to its session, select its
// window, then make it the active pane.
//
// select-window is what actually moves the view. switch-client is a no-op when
// no client is attached (a server driven over ssh, a detached test server), and
// select-pane alone only sets the active pane of a window you are not looking
// at — so without select-window, focusing an agent in another window appears to
// do nothing.
func (s Server) SelectPane(paneID string) {
	s.Run("switch-client", "-t", paneID)
	s.Run("select-window", "-t", paneID)
	s.Run("select-pane", "-t", paneID)
}

// FocusPane is SelectPane under the name the client uses.
func (s Server) FocusPane(paneID string) { s.SelectPane(paneID) }

// Option sets a global option.
func (s Server) Option(name, value string) { s.Run("set-option", "-gq", name, value) }

// ShowOption reads a global option's value (trimmed).
func (s Server) ShowOption(name string) string {
	out, _ := s.Run("show-option", "-gqv", name)
	return strings.TrimSpace(out)
}

// IsTmux reports whether the current process runs inside a tmux pane.
func (s Server) IsTmux() bool { return os.Getenv("TMUX") != "" }

// String renders the server's identity for logs/errors.
func (s Server) String() string {
	if s.Socket == "" {
		return "tmux (default socket)"
	}
	return fmt.Sprintf("tmux -L %s", s.Socket)
}
