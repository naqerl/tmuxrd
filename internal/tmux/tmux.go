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
	"path/filepath"
	"strconv"
	"strings"
)

// EnvVar selects the socket for a Server resolved from the environment.
const EnvVar = "TERDR_TMUX_SOCKET"

// Server addresses one tmux server. Both ways of naming one are supported:
//
//   - SocketPath: the server's actual socket path, passed as `-S`. This is how a
//     process inside tmux identifies "the server I am running in" ($TMUX), and it
//     is exact — no name resolution, no ambiguity.
//   - Socket: a socket *name*, passed as `-L` (tmuxrd's TERDR_TMUX_SOCKET).
//
// Both empty addresses the default server with a bare `tmux`, which also honours
// an inherited $TMUX.
type Server struct {
	SocketPath string // -S: full socket path (empty = use Socket or default)
	Socket     string // -L: socket name (empty = default)
}

// NewServer returns a Server for the named socket (-L). An empty name selects
// the default tmux server.
func NewServer(socket string) Server {
	return Server{Socket: socket}
}

// NewServerFromEnv resolves the tmux server to talk to:
//
//   - TERDR_TMUX_SOCKET set -> that socket name (-L);
//   - otherwise $TMUX        -> the server this process is running inside, by its
//     socket path (-S), so a daemon started by tmux server A can never query tmux
//     server B. Without this a `tmux -L foo` server (which also auto-loads the
//     config) started a daemon that hijacked the *default* daemon socket and then
//     queried the wrong server, leaving the real sidebar empty;
//   - otherwise              -> the default server.
func NewServerFromEnv() Server {
	if name := os.Getenv(EnvVar); name != "" {
		return Server{Socket: name}
	}
	return Server{SocketPath: socketPathFromTMUX(os.Getenv("TMUX"))}
}

// socketPathFromTMUX extracts the socket path from $TMUX, which tmux sets to
// "<socket path>,<server pid>,<session id>".
func socketPathFromTMUX(tmux string) string {
	if i := strings.IndexByte(tmux, ','); i >= 0 {
		return tmux[:i]
	}
	return tmux
}

// IsDefault reports whether this server is the default tmux instance.
func (s Server) IsDefault() bool { return s.SocketPath == "" && s.Socket == "" }

// prefix builds the leading argv for a tmux invocation.
func (s Server) prefix() []string {
	switch {
	case s.SocketPath != "":
		return []string{"-S", s.SocketPath}
	case s.Socket != "":
		return []string{"-L", s.Socket}
	}
	return nil
}

// SocketFile names this server's daemon socket file, so two tmux servers never
// share one daemon.
func (s Server) SocketFile() string {
	if s.SocketPath != "" {
		return filepath.Base(s.SocketPath)
	}
	if s.Socket != "" {
		return s.Socket
	}
	return "default"
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

	// Geometry. Window* is the window this pane lives in (shared by every pane
	// of that window); Width/Height is the pane itself. The layout pass reads
	// these to choose a sidebar shape without any extra tmux round-trip.
	WindowWidth  int
	WindowHeight int
	WindowZoomed bool
	Width        int
	Height       int

	// Sidebar is true for a pane tmuxrd owns (@tmuxrd_sidebar=1), and Layout is
	// that window's recorded shape (@tmuxrd_layout: "side"/"strip", empty when
	// the option was never set).
	Sidebar bool
	Layout  string

	// Pinned is the pane's @tmuxrd_finished marker: the daemon sets it the
	// first time it sees the pane's agent working, so the sidebar can tell an
	// agent that has finished a run (✓) from one that never started (○). tmux
	// keeps pane options for the life of the pane, so the marker survives a
	// daemon restart and dies with the pane — a replacement agent starts fresh.
	Pinned bool

	// Active is window_active: this pane's window is its session's current
	// window, attached or not. Attached is session_attached > 0. Viewed is
	// both, and is what consumes the finished pin — a detached session's
	// current window is not viewed by anyone. The daemon clears Finished on a
	// viewed pane, which is what makes ✓ a notification pin rather than a
	// permanent badge.
	Active   bool
	Attached bool
	Viewed   bool

	// ContentFP is a cheap fingerprint of the visible screen, taken from the
	// same list-panes row: cursor, history, scroll position, alternate-screen
	// flag and pane title. next-3.9 and 3.5 have no #{pane_activity}, so this
	// is what a refresh can compare to skip capture-pane. A TUI can still
	// redraw in place without moving any of these; callers must not treat a
	// match as proof the screen is unchanged forever.
	ContentFP string

	// Title is #{pane_title}. Grok puts the focused session's title here, which
	// is how a process with several open tabs is reduced to the one on screen.
	Title string
}

const paneFmt = "#{session_name}\t#{window_index}\t#{window_name}\t#{pane_id}\t#{pane_pid}\t#{pane_current_command}\t#{pane_current_path}\t#{pane_width}\t#{pane_height}\t#{window_width}\t#{window_height}\t#{window_zoomed_flag}\t#{@tmuxrd_sidebar}\t#{@tmuxrd_layout}\t#{@tmuxrd_finished}\t#{window_active}\t#{session_attached}\t#{cursor_x}\t#{cursor_y}\t#{history_size}\t#{history_bytes}\t#{scroll_position}\t#{alternate_on}\t#{pane_title}"

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
		// 0..22 are fixed columns; pane_title is the rest so a title that
		// itself contains a tab does not shift the columns in front of it.
		if len(fields) < 23 {
			continue
		}
		title := ""
		if len(fields) > 23 {
			title = strings.Join(fields[23:], "\t")
		}
		active := strings.TrimSpace(fields[15]) == "1"
		attached := atoi(fields[16]) > 0
		panes = append(panes, Pane{
			Session: fields[0], Window: fields[1], WindowName: fields[2],
			ID: fields[3], PID: fields[4], Cmd: fields[5], Path: fields[6],
			Width:        atoi(fields[7]),
			Height:       atoi(fields[8]),
			WindowWidth:  atoi(fields[9]),
			WindowHeight: atoi(fields[10]),
			WindowZoomed: fields[11] == "1",
			Sidebar:      strings.TrimSpace(fields[12]) == "1",
			Layout:       strings.TrimSpace(fields[13]),
			Pinned:       strings.TrimSpace(fields[14]) == "1",
			Active:       active,
			Attached:     attached,
			Viewed:       active && attached,
			ContentFP: strings.Join([]string{
				fields[17], fields[18], fields[19], fields[20], fields[21], fields[22], title,
			}, "\x1e"),
			Title: title,
		})
	}
	return panes, nil
}

// atoi parses a tmux numeric format, defaulting to 0 on anything unexpected.
func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
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

// GoToAgent switches the view to the pane's window and session and leaves the
// keyboard on that window's sidebar pane, so the sidebar can keep navigating
// after the view has moved (switch-client handles the cross-session case;
// select-window moves the view when no client is attached). fromPane is the
// sidebar that received the gesture ("" when the caller has no pane context).
func (s Server) GoToAgent(fromPane, paneID string) {
	_ = s.GoToAgentClient(paneID, s.DrivingClient(fromPane))
}

// GoToAgentClient is GoToAgent with the driving client already resolved.
// Passing it in keeps a keypress from paying display-message + list-clients
// on every repeat. An empty client omits switch-client (a detached server).
func (s Server) GoToAgentClient(paneID, client string) error {
	target := paneID
	if sb := s.sidebarPaneInWindow(paneID); sb != "" {
		target = sb
	}
	return s.MoveClient(client, paneID, target)
}

// SelectPane focuses a pane: switch the client to its session, select its
// window, then make it the active pane.
//
// select-window is what actually moves the view. switch-client is a no-op when
// no client is attached (a server driven over ssh, a detached test server), and
// select-pane alone only sets the active pane of a window you are not looking
// at — so without select-window, focusing an agent in another window appears to
// do nothing.
func (s Server) SelectPane(fromPane, paneID string) {
	_ = s.MoveClient(s.DrivingClient(fromPane), paneID, paneID)
}

// FocusPane is SelectPane under the name the client uses.
func (s Server) FocusPane(fromPane, paneID string) { s.SelectPane(fromPane, paneID) }

// MoveClient moves client to view's session and window and leaves the keyboard
// on focus. It is ONE tmux invocation: run as separate invocations, the client
// repainted once when the view landed and again when the keyboard moved, which
// reads as a flicker through intermediate frames.
//
// The order is load-bearing. tmux stops processing a ';'-separated list at the
// first command that fails, so the two commands that always work — moving the
// session's window and its active pane — come first, and switch-client, which
// fails on a server with no client attached (a detached test server, an ssh
// driven server) and can race a client that just detached, comes last. With the
// switch first, a client-less server moved nothing at all: the view stayed put
// and only the error was left behind. An empty client skips switch-client.
// The error is returned so the caller can drop a cached client name when the
// switch fails; select-window and select-pane have already run by then.
//
// switch-client -t selects the pane it names, and it runs last, so the target
// is focus rather than view. Targeting the agent pane here puts the keyboard
// in the agent after select-pane had just parked it on the sidebar.
func (s Server) MoveClient(client, view, focus string) error {
	args := []string{"select-window", "-t", view, ";", "select-pane", "-t", focus}
	if client != "" {
		args = append(args, ";", "switch-client", "-c", client, "-t", focus)
	}
	_, err := s.Run(args...)
	return err
}

// DrivingClient names the client a gesture belongs to. A command run from a pane
// process carries no client context, so switch-client with no -c moves an
// arbitrary client — in practice the last one tmux iterates — and on a server
// with two attached terminals a keystroke in one moved the other. The gesture's
// client is the most recently active client attached to fromPane's session: the
// one whose keyboard just produced the event. The name is what -c wants (a
// client's name is its tty, or client-<pid> for a control client); a pid is not
// a client target. "" (nothing attached, a detached server, no pane context)
// leaves the choice to tmux.
func (s Server) DrivingClient(fromPane string) string {
	if fromPane == "" {
		return ""
	}
	sess, _ := s.Run("display-message", "-p", "-t", fromPane, "#{session_name}")
	sess = strings.TrimSpace(sess)
	if sess == "" {
		return ""
	}
	// A control client (`tmux -C`, the kind scripts and the test harness
	// attach) is never the client a human pressed a key in, so it only wins when
	// no real client is attached at all.
	out, _ := s.Run("list-clients", "-t", sess, "-F", "#{client_activity}\t#{client_control_mode}\t#{client_name}")
	best, bestAct, bestReal, bestRealAct := "", int64(-1), "", int64(-1)
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 3 {
			continue
		}
		act, err := strconv.ParseInt(strings.TrimSpace(f[0]), 10, 64)
		if err != nil {
			continue
		}
		name := strings.TrimSpace(f[2])
		if act > bestAct {
			bestAct, best = act, name
		}
		if strings.TrimSpace(f[1]) == "0" && act > bestRealAct {
			bestRealAct, bestReal = act, name
		}
	}
	if bestReal != "" {
		return bestReal
	}
	return best
}

// Option sets a global option.
func (s Server) Option(name, value string) { s.Run("set-option", "-gq", name, value) }

// SplitPane creates a pane in target, split along axis ("h" = side by side,
// "v" = stacked), placed before (left of / above) the target, detached so
// focus does not move, sized in cells (columns for "h", rows for "v"), running
// command. It returns the new pane id.
func (s Server) SplitPane(target, axis string, size int, command string) (string, error) {
	dir := "-h"
	if axis == "v" {
		dir = "-v"
	}
	out, err := s.Run("split-window", dir, "-b", "-d", "-t", target, "-l", strconv.Itoa(size), "-P", "-F", "#{pane_id}", command)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// KillPane closes a pane.
func (s Server) KillPane(paneID string) { s.Run("kill-pane", "-t", paneID) }

// ResizePane resizes a pane along one axis: "v" sets its height in rows (-y),
// "h" its width in columns (-x). tmux clamps to what the window can fit.
func (s Server) ResizePane(paneID, axis string, n int) {
	if axis == "v" {
		s.Run("resize-pane", "-t", paneID, "-y", strconv.Itoa(n))
		return
	}
	s.Run("resize-pane", "-t", paneID, "-x", strconv.Itoa(n))
}

// SetPaneOption sets an option on one pane (the sidebar tag).
func (s Server) SetPaneOption(paneID, name, value string) {
	s.Run("set-option", "-p", "-t", paneID, name, value)
}

// UnsetPaneOption removes a pane option, so it stops existing rather than
// lingering with an empty value.
func (s Server) UnsetPaneOption(paneID, name string) {
	s.Run("set-option", "-pu", "-t", paneID, name)
}

// SetWindowOption sets an option on one window (the recorded sidebar shape).
func (s Server) SetWindowOption(target, name, value string) {
	s.Run("set-option", "-w", "-t", target, name, value)
}

// ShowOption reads a global option's value (trimmed).
func (s Server) ShowOption(name string) string {
	out, _ := s.Run("show-option", "-gqv", name)
	return strings.TrimSpace(out)
}

// IsTmux reports whether the current process runs inside a tmux pane.
func (s Server) IsTmux() bool { return os.Getenv("TMUX") != "" }

// String renders the server's identity for logs/errors.
func (s Server) String() string {
	switch {
	case s.SocketPath != "":
		return fmt.Sprintf("tmux -S %s", s.SocketPath)
	case s.Socket != "":
		return fmt.Sprintf("tmux -L %s", s.Socket)
	}
	return "tmux (default socket)"
}
