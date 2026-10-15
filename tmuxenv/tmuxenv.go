// Package tmuxenv provides the Go abstraction used by the godog feature suite
// to drive a tmux server. Everything it does goes through tmux.Server, which
// always passes an explicit -L socket, so a scenario can never address the
// default (live) tmux server.
package tmuxenv

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/naqerl/tmuxrd/internal/detect"
	"github.com/naqerl/tmuxrd/internal/tmux"
)

// Env owns one isolated tmux server for the lifetime of a scenario.
type Env struct {
	Server       tmux.Server
	Socket       string
	BinDir       string // fake agent commands (pi, claude, ...) live here
	Bin          string // absolute path to the tmuxrd binary
	Scripts      string // absolute path to the scripts/ dir
	Config       string // absolute path to tmuxrd.tmux
	Root         string // repo root, published as @tmuxrd_root for the config
	Daemon       int    // pid of the first daemon we started, 0 if none
	daemons      []int  // every daemon pid we started, so Close can reap them all
	fakeName     string
	envPublished bool
	// attachClients are control-mode tmux clients started by AttachClient, with
	// the stdin pipe that keeps each one alive. Close reaps both.
	attachClients []*exec.Cmd
	attachStdin   []*os.File
}

// fakeagent states accepted by NewSession/NewWindow. An empty state means
// "render the idle banner".
const (
	StateIdle    = "idle"
	StateWorking = "working"
	StateBlocked = "blocked"
	StateUnknown = "unknown"
)

// New starts a fresh tmux server on an isolated socket and prepares a directory
// of fake agent executables. socket must be a unique, non-default name.
func New(socket string, repoRoot string) (*Env, error) {
	if socket == "" || socket == "default" {
		return nil, fmt.Errorf("refusing non-isolated socket %q", socket)
	}
	e := &Env{
		Server:  tmux.NewServer(socket),
		Socket:  socket,
		BinDir:  mustTempDir("tmuxrd-fakebin-"),
		Bin:     filepath.Join(repoRoot, "bin", "tmuxrd"),
		Scripts: filepath.Join(repoRoot, "scripts"),
		Config:  filepath.Join(repoRoot, "tmuxrd.tmux"),
		Root:    repoRoot,
	}
	fakeAgentSrc = filepath.Join(repoRoot, "cmd", "fakeagent", "fakeagent")
	if _, err := os.Stat(fakeAgentSrc); err != nil {
		return nil, fmt.Errorf("fakeagent binary missing (run go build ./cmd/fakeagent): %w", err)
	}
	e.Server.KillServer() // clear any leftover on this socket
	// A kill-server exits asynchronously; wait for the socket to actually go
	// away or the first new-session races the dying server and lands in it
	// (leaving a live server behind for the next scenario to collide with).
	for i := 0; i < 100 && e.Server.HasServer(); i++ {
		time.Sleep(20 * time.Millisecond)
	}
	e.envPublished = false
	for _, a := range []string{"pi", "claude", "codex", "opencode", "cursor", "kimi", "hermes", "gemini", "grok", "bash"} {
		if err := e.writeFake(a); err != nil {
			return nil, err
		}
	}
	// Sidebar panes run `tmuxrd client`, which resolves the daemon socket from
	// TERDR_TMUX_SOCKET + XDG_RUNTIME_DIR. Panes inherit these from the *server*,
	// not from this process, so they are published into the server's global env
	// as soon as the server exists (see ensureClientEnv).
	// Sidebar panes run `tmuxrd client`, which exits immediately (taking the
	// pane with it) unless a daemon is listening. Start one so scenarios that
	// create sidebar panes exercise a live client.
	if err := e.StartDaemon(); err != nil {
		e.Server.KillServer()
		os.RemoveAll(e.BinDir)
		return nil, fmt.Errorf("start daemon: %w", err)
	}
	return e, nil
}

// Close tears the server down and stops any daemon this Env started.
func (e *Env) Close() {
	for _, cmd := range e.attachClients {
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
	}
	for _, f := range e.attachStdin {
		f.Close()
	}
	e.StopDaemon()
	e.Server.KillServer()
	for i := 0; i < 100 && e.Server.HasServer(); i++ {
		time.Sleep(20 * time.Millisecond)
	}
	os.Remove(filepath.Join(os.TempDir(), "tmux-1000", e.Socket))
	os.RemoveAll(e.runtimeDir())
	os.RemoveAll(e.BinDir)
}

// fakeAgentSrc is the prebuilt binary that impersonates an agent. Each agent
// name gets its own *copy* (not symlink) so /proc/<pid>/comm and tmux's
// pane_current_command both report the agent name.
var fakeAgentSrc string

func (e *Env) writeFake(name string) error {
	if fakeAgentSrc == "" {
		return fmt.Errorf("fakeAgentSrc not initialised")
	}
	dst := filepath.Join(e.BinDir, name)
	// A hardlink is enough: /proc/<pid>/comm and tmux's pane_current_command are
	// taken from the executed basename, so a link named "pi" reports "pi".
	// Copying the (multi-MB) binary ten times per scenario filled /tmp.
	if err := os.Link(fakeAgentSrc, dst); err == nil {
		return nil
	}
	// Different filesystem: fall back to a copy.
	data, err := os.ReadFile(fakeAgentSrc)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o755)
}

// PATH returns the fake-bin-augmented PATH for child processes.
func (e *Env) PATH() string { return e.BinDir + ":" + os.Getenv("PATH") }

// env builds the environment for every tmux command and, through the server's
// global environment, for the panes themselves.
//
// The sidebar's selection is the accent background, so the suite asserts ANSI
// colours — a desktop row has no `>` pointer to look for anymore. lipgloss
// honours NO_COLOR / CLICOLOR=0 (and a CI runner may set one) by rendering plain
// text, which would fail every selection assertion for a reason that has nothing
// to do with the client. So the harness drops those opt-outs and forces colour
// on: the suite pins the palette, and it can only do that in a colour-capable,
// colour-enabled environment — which is what a real tmux user has.
func (e *Env) env(extra ...string) []string {
	out := make([]string, 0, len(os.Environ())+len(extra)+2)
	for _, kv := range os.Environ() {
		switch strings.SplitN(kv, "=", 2)[0] {
		case "NO_COLOR", "CLICOLOR", "CLICOLOR_FORCE", "FORCE_COLOR":
			continue
		}
		out = append(out, kv)
	}
	out = append(out, "PATH="+e.PATH(), "CLICOLOR_FORCE=1")
	return append(out, extra...)
}

// run executes a tmux command with PATH set to see the fake agents.
func (e *Env) run(args ...string) (string, error) {
	cmd := exec.Command("tmux", append([]string{"-L", e.Socket}, args...)...)
	cmd.Env = e.env()
	out, err := cmd.CombinedOutput()
	return strings.TrimRight(string(out), "\n"), err
}

// cmd resolves an agent name to an executable path and builds the pane
// command. The sentinel "plain" yields a non-agent command (a bare sleep) so
// detection must ignore it. Every fake agent also gets its own state file
// (<BinDir>/<agent>.state, pre-seeded with state) and is launched with
// -state-file so SetAgentState can flip a running pane's state in place.
//
// screen is a repo-relative (or absolute) file the fake renders verbatim
// instead of a state banner — used to replay a real agent pane capture. A
// screen-mode pane gets no state file: its view is pinned.
func (e *Env) cmd(agent, state, screen string) (string, error) {
	if state == "" {
		state = StateIdle
	}
	if agent == "plain" {
		return "/bin/sleep 3600", nil
	}
	bin := filepath.Join(e.BinDir, agent)
	if screen != "" {
		if !filepath.IsAbs(screen) {
			screen = filepath.Join(e.Root, screen)
		}
		return fmt.Sprintf("%s -screen-file %s", bin, screen), nil
	}
	path := e.StateFile(agent)
	if err := os.WriteFile(path, []byte(state), 0o644); err != nil {
		return "", fmt.Errorf("write state file %s: %w", path, err)
	}
	return fmt.Sprintf("%s -state-file %s", bin, path), nil
}

// StateFile is the one-per-agent state file SetAgentState rewrites and the
// matching fakeagent re-reads while running. One running pane -> one file.
func (e *Env) StateFile(agent string) string {
	return filepath.Join(e.BinDir, agent+".state")
}

// SetAgentState switches an already-running agent's state without touching
// the pane: it overwrites the agent's state file, whose path was baked into
// the pane command as -state-file. The fakeagent polls the file (~250ms) and
// reprints its banner when the content changes.
func (e *Env) SetAgentState(sess, win, state string) error {
	if state == "" {
		state = StateIdle
	}
	// resolve the agent name from the pane's current command (read every pane
	// in the window: tmux expands -t to the *active* pane, which may be the
	// sidebar client rather than the agent pane)
	out, err := e.run("list-panes", "-t", sess+":"+win, "-F",
		"#{pane_current_command}\t#{@tmuxrd_sidebar}\t#{pane_active}")
	if err != nil {
		return fmt.Errorf("list-panes %s:%s: %v: %s", sess, win, err, out)
	}
	agent := ""
	for _, l := range strings.Split(out, "\n") {
		f := strings.Split(l, "\t")
		if len(f) < 2 || strings.TrimSpace(f[0]) == "" {
			continue
		}
		if strings.TrimSpace(f[1]) == "1" {
			continue // sidebar pane
		}
		agent = strings.TrimSpace(f[0])
		if len(f) < 3 || strings.TrimSpace(f[2]) == "1" {
			break // prefer the active (non-sidebar) pane
		}
	}
	if agent == "" {
		return fmt.Errorf("no pane in %s:%s", sess, win)
	}
	if _, err := os.Stat(filepath.Join(e.BinDir, agent)); err != nil {
		return fmt.Errorf("%s:%s is not a fake agent pane (command %q)", sess, win, agent)
	}
	if err := os.WriteFile(e.StateFile(agent), []byte(state), 0o644); err != nil {
		return err
	}
	// The daemon re-detects on a 500ms tick; push a notify so the sidebar
	// redraws with the new state immediately instead of up to a tick later.
	e.RunBin("notify")
	return nil
}

// AttachClient attaches a control-mode tmux client to the session. The daemon
// treats a pane as viewed only when its window is the current window of a
// session that has at least one attached client (tmux.Pane.Viewed); a
// control-mode client is the one kind of client that needs no TTY, so this is
// how a scenario makes a window count as seen.
//
// Control clients do not participate in window sizing (tmux keeps the window at
// its real client's size, verified with -x 200 -y 50), so layout scenarios are
// unaffected. The client stays attached until Close, which kills it.
func (e *Env) AttachClient(sess string) error {
	cmd := exec.Command("tmux", "-L", e.Socket, "-C", "attach", "-t", sess)
	cmd.Env = e.env()
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	// A pipe on stdin is what keeps a control client alive: EOF ends it. The
	// child gets its own copy of the read end; the write end stays open here.
	pr, pw, err := os.Pipe()
	if err != nil {
		return err
	}
	cmd.Stdin = pr
	if err := cmd.Start(); err != nil {
		pr.Close()
		pw.Close()
		return fmt.Errorf("attach client to %s: %w", sess, err)
	}
	pr.Close()
	e.attachClients = append(e.attachClients, cmd)
	e.attachStdin = append(e.attachStdin, pw)

	// Wait until tmux counts it, so the next step can rely on "viewed".
	deadline := time.Now().Add(3 * time.Second)
	for {
		out, err := e.run("display-message", "-p", "-t", sess, "#{session_attached}")
		if err == nil {
			if n, _ := strconv.Atoi(strings.TrimSpace(out)); n > 0 {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("session %s never reported an attached client (last: %q)", sess, out)
		}
		time.Sleep(30 * time.Millisecond)
	}
}

// NewSession creates a detached session running a fake agent in the given
// state ("" = idle banner).
func (e *Env) NewSession(name, agent, state string) error {
	return e.NewSessionWithScreen(name, agent, state, "")
}

// NewSessionWithScreen is NewSession with a verbatim screen file: the pane's
// fake agent paints that capture instead of a synthetic state banner, so a
// scenario can assert detection against exactly what a real TUI rendered.
func (e *Env) NewSessionWithScreen(name, agent, state, screen string) error {
	cmd, err := e.cmd(agent, state, screen)
	if err != nil {
		return err
	}
	// Give the session a realistic size. A detached server defaults to 80x24, and
	// at 80 columns pi's question footer is truncated — so the very thing the
	// blocked detection keys on would never be on screen (a real 117-col pane
	// shows it in full).
	// -f /dev/null keeps the scenario hermetic: without it tmux reads the
	// operator's ~/.tmux.conf, which now auto-loads tmuxrd — that would create
	// sidebars behind the scenario's back and start an untracked daemon.
	out, err := e.run("-f", "/dev/null", "new-session", "-d", "-s", name, "-x", "220", "-y", "50", cmd)
	if err != nil {
		return fmt.Errorf("new-session %s (%s): %v: %s", name, cmd, err, out)
	}
	// Publish after the command: the first session is what brings the server
	// into existence, and a config-sourced daemon only targets our socket once
	// this lands in the server's global environment.
	return e.ensureClientEnv()
}

// NewWindow adds a window to session running a fake agent in the given state
// ("" = idle banner).
// NOTE: -t sess: (with the trailing colon) — without it tmux resolves -t to a
// window index and fails with "index 0 in use".
func (e *Env) NewWindow(sess, agent, state string) error {
	return e.NewWindowWithScreen(sess, agent, state, "")
}

// NewWindowWithScreen is NewWindow with a verbatim screen file (see
// NewSessionWithScreen).
func (e *Env) NewWindowWithScreen(sess, agent, state, screen string) error {
	cmd, err := e.cmd(agent, state, screen)
	if err != nil {
		return err
	}
	out, err := e.run("new-window", "-t", sess+":", cmd)
	if err != nil {
		return fmt.Errorf("new-window %s: (%s): %v: %s", sess, cmd, err, out)
	}
	return e.ensureClientEnv()
}

// SourceConfig loads tmuxrd.tmux (daemon start + hooks + status line).
//
// tmux runs the config's run-shell jobs with the *server's* global
// environment, not ours, so the daemon the config starts only targets our
// socket if the env is published first. Without this it would fall back to the
// process' default tmux server — i.e. the operator's live one.
func (e *Env) SourceConfig() error {
	if err := e.publishClientEnv(); err != nil {
		return err
	}
	if got := e.serverEnv(tmux.EnvVar); got != e.Socket {
		return fmt.Errorf("refusing to source config: server %s has %s=%q, want %q",
			e.Socket, tmux.EnvVar, got, e.Socket)
	}
	cmd := exec.Command("tmux", "-L", e.Socket, "source-file", e.Config)
	cmd.Env = e.env(
		"TERDR_TMUX_SOCKET="+e.Socket,
		"XDG_RUNTIME_DIR="+e.runtimeDir(),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("source-file: %v: %s", err, out)
	}
	return nil
}

func (e *Env) RuntimeDir() string {
	return e.runtimeDir()
}

// ensureClientEnv publishes the daemon socket location into the server's global
// env so every pane can find it.
func (e *Env) ensureClientEnv() error {
	if e.envPublished {
		return nil
	}
	if !e.Server.HasServer() {
		return nil // no server yet; retried on the next call
	}
	rt := e.runtimeDir()
	for _, kv := range [][2]string{
		{tmux.EnvVar, e.Socket},
		{"XDG_RUNTIME_DIR", rt},
		// Panes are created later by the scripts and inherit this global
		// environment; forcing colour here keeps the palette assertions valid
		// even if the server was seeded some other way.
		{"CLICOLOR_FORCE", "1"},
	} {
		if _, err := e.Server.Run("set-environment", "-g", kv[0], kv[1]); err != nil {
			return fmt.Errorf("set-environment %s: %w", kv[0], err)
		}
	}
	// A colour opt-out in the inherited environment would defeat that: drop it
	// from the server's global environment so it never reaches a client pane.
	// (Non-fatal: unsetting a variable the server never had is not an error we
	// care about.)
	e.Server.Run("set-environment", "-gu", "NO_COLOR")
	// The config resolves every path from @tmuxrd_root, so it must point at this
	// checkout before the config is sourced.
	if _, err := e.Server.Run("set-option", "-gq", "@tmuxrd_root", e.Root); err != nil {
		return fmt.Errorf("set-option @tmuxrd_root: %w", err)
	}
	e.envPublished = true
	return nil
}

func (e *Env) runtimeDir() string {
	d := filepath.Join(os.TempDir(), "tmuxrd-rt-"+e.Socket)
	os.MkdirAll(d, 0o700)
	return d
}

// publishClientEnv force-publishes the client env and requires a server.
func (e *Env) publishClientEnv() error {
	if !e.Server.HasServer() {
		return fmt.Errorf("cannot publish client env: no server on %s", e.Socket)
	}
	e.envPublished = false
	return e.ensureClientEnv()
}

// serverEnv reads a global environment variable from our tmux server.
func (e *Env) serverEnv(name string) string {
	out, _ := e.Server.Run("show-environment", "-g", name)
	if i := strings.IndexByte(out, '='); i >= 0 {
		return out[i+1:]
	}
	return ""
}

// ServerEnv exposes the server's global environment for assertions.
func (e *Env) ServerEnv(name string) string { return e.serverEnv(name) }

// DaemonPIDs returns the pids of live `tmuxrd daemon` processes bound to this
// Env's socket, by scanning /proc for TERDR_TMUX_SOCKET=<socket>. It is the
// only way to notice a daemon that outlived its scenario, or one that fell back
// to the default tmux server.
func (e *Env) DaemonPIDs() []int {
	var pids []int
	want := tmux.EnvVar + "=" + e.Socket
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	for _, ent := range entries {
		if _, err := strconv.Atoi(ent.Name()); err != nil {
			continue
		}
		comm, err := os.ReadFile("/proc/" + ent.Name() + "/comm")
		if err != nil || strings.TrimSpace(string(comm)) != "tmuxrd" {
			continue
		}
		envb, err := os.ReadFile("/proc/" + ent.Name() + "/environ")
		if err != nil {
			continue
		}
		if strings.Contains(string(envb), want+"\x00") {
			pid, _ := strconv.Atoi(ent.Name())
			pids = append(pids, pid)
		}
	}
	return pids
}

// runScript runs one of the shell helpers against this server with the right
// TERDR_TMUX_SOCKET so it operates on our socket only.
func (e *Env) runScript(name string) error {
	cmd := exec.Command("bash", filepath.Join(e.Scripts, name))
	cmd.Env = e.env(
		"TERDR_TMUX_SOCKET="+e.Socket,
		"XDG_RUNTIME_DIR="+e.runtimeDir(),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %v: %s", name, err, out)
	}
	return nil
}

// ToggleSidebar flips sidebar visibility exactly like the prefix-b binding.
func (e *Env) ToggleSidebar() error {
	if err := e.ensureClientEnv(); err != nil {
		return err
	}
	return e.runScript("toggle_sidebar.sh")
}

// EnsureSidebar is the after-new-window/after-new-session hook handler.
func (e *Env) EnsureSidebar() error {
	if err := e.ensureClientEnv(); err != nil {
		return err
	}
	return e.runScript("ensure_sidebar.sh")
}

// CleanupOrphans is the pane-exited/after-kill-pane hook handler.
func (e *Env) CleanupOrphans() error { return e.runScript("cleanup_orphans.sh") }

// RunBin invokes the tmuxrd binary (summary/client) against this server.
func (e *Env) RunBin(args ...string) (string, error) {
	cmd := exec.Command(e.Bin, args...)
	cmd.Env = e.env(
		"TERDR_TMUX_SOCKET="+e.Socket,
		"XDG_RUNTIME_DIR="+e.runtimeDir(),
	)
	out, err := cmd.CombinedOutput()
	return strings.TrimRight(string(out), "\n"), err
}

// StartDaemon ensures one tmuxrd daemon is running for this server. It is
// idempotent: a daemon already running for our socket is enough, and starting
// another would be rejected by the daemon's singleton guard anyway.
func (e *Env) StartDaemon() error {
	if e.Daemon != 0 && e.DaemonAlive(e.Daemon) {
		return nil
	}
	cmd := exec.Command(e.Bin, "daemon")
	cmd.Env = e.env(
		"TERDR_TMUX_SOCKET="+e.Socket,
		"XDG_RUNTIME_DIR="+e.runtimeDir(),
	)
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return err
	}
	if e.Daemon == 0 {
		e.Daemon = cmd.Process.Pid
	}
	e.daemons = append(e.daemons, cmd.Process.Pid)
	go cmd.Wait() // reap
	// wait for the socket to appear
	sock := filepath.Join(e.runtimeDir(), "tmuxrd-"+e.Socket+".sock")
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(sock); err == nil {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return fmt.Errorf("daemon socket %s never appeared", sock)
}

// DaemonAlive reports whether pid is still running.
func (e *Env) DaemonAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

// StopDaemon kills every daemon this Env started. Killing all of them (rather
// than just the last pid) is what stops an earlier daemon from being orphaned
// when a scenario starts the daemon more than once.
func (e *Env) StopDaemon() {
	for _, pid := range e.daemons {
		if p, err := os.FindProcess(pid); err == nil {
			p.Kill()
		}
	}
	e.daemons = nil
	e.Daemon = 0
}

// SidebarPanes returns pane ids tagged @tmuxrd_sidebar=1.
func (e *Env) SidebarPanes() ([]string, error) {
	out, err := e.run("list-panes", "-a", "-F", "#{pane_id}\t#{@tmuxrd_sidebar}")
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, l := range strings.Split(out, "\n") {
		f := strings.Split(l, "\t")
		if len(f) == 2 && strings.TrimSpace(f[1]) == "1" {
			ids = append(ids, f[0])
		}
	}
	return ids, nil
}

// MainPanes returns pane ids NOT tagged as a sidebar.
func (e *Env) MainPanes() ([]string, error) {
	all, err := e.run("list-panes", "-a", "-F", "#{pane_id}\t#{@tmuxrd_sidebar}")
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, l := range strings.Split(all, "\n") {
		f := strings.Split(l, "\t")
		if len(f) == 2 && strings.TrimSpace(f[1]) != "1" {
			ids = append(ids, f[0])
		}
	}
	return ids, nil
}

// Windows returns window indices of a session.
func (e *Env) Windows(sess string) ([]string, error) {
	out, err := e.run("list-windows", "-t", sess, "-F", "#{window_index}")
	if err != nil {
		return nil, err
	}
	return nonEmptyLines(out), nil
}

// WindowCount returns the total number of windows across all sessions.
func (e *Env) WindowCount() (int, error) {
	out, err := e.run("list-windows", "-a", "-F", "#{window_index}")
	if err != nil {
		return 0, err
	}
	return len(nonEmptyLines(out)), nil
}

// SessionNames returns all session names.
func (e *Env) SessionNames() ([]string, error) {
	out, err := e.run("list-sessions", "-F", "#{session_name}")
	if err != nil {
		return nil, err
	}
	return nonEmptyLines(out), nil
}

// ActiveSession returns the session that is current for the calling client.
// For a headless (detached) test server this is the most recently used one.
func (e *Env) ActiveSession() string {
	out, _ := e.run("display-message", "-p", "#{session_name}")
	return strings.TrimSpace(out)
}

// KillPane kills a pane by id (returns error output for assertions).
func (e *Env) KillPane(id string) (string, error) {
	return e.run("kill-pane", "-t", id)
}

// SelectWindow switches the current client to a window.
func (e *Env) SelectWindow(target string) error {
	_, err := e.run("select-window", "-t", target)
	return err
}

// timerRe matches the timer the sidebar renders on a row: 0s, 45s, 1m, 1m30s.
var timerRe = regexp.MustCompile(`(?:^|\s)(\d+m\d+s|\d+m|\d+s)(?:\s|$)`)

// TimerFor returns the timer token shown on the agent's row, or an error when
// the row exists but carries no timer.
func (e *Env) TimerFor(agent string) (string, error) {
	content, err := e.SidebarContent()
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(content, "\n") {
		if !strings.Contains(line, ":"+agent) && !strings.Contains(line, "("+agent+")") {
			continue
		}
		if m := timerRe.FindStringSubmatch(line); m != nil {
			return m[1], nil
		}
		return "", fmt.Errorf("no timer on the row for %s: %q", agent, line)
	}
	return "", fmt.Errorf("no row for agent %q\n%s", agent, content)
}

// ParseTimer converts a rendered timer token ("1m30s") back to a duration.
func ParseTimer(s string) (time.Duration, error) {
	m := regexp.MustCompile(`^(?:(\d+)m)?(?:(\d+)s)?$`).FindStringSubmatch(s)
	if m == nil {
		return 0, fmt.Errorf("bad timer %q", s)
	}
	mins, secs := 0, 0
	if m[1] != "" {
		mins, _ = strconv.Atoi(m[1])
	}
	if m[2] != "" {
		secs, _ = strconv.Atoi(m[2])
	}
	return time.Duration(mins)*time.Minute + time.Duration(secs)*time.Second, nil
}

// SidebarPane returns the first sidebar pane id.
func (e *Env) SidebarPane() (string, error) {
	ids, err := e.SidebarPanes()
	if err != nil {
		return "", err
	}
	if len(ids) == 0 {
		return "", fmt.Errorf("no sidebar pane")
	}
	return ids[0], nil
}

// SelectPane makes a pane the active pane of its window (no client needed).
func (e *Env) SelectPane(paneID string) error {
	_, err := e.run("select-pane", "-t", paneID)
	return err
}

// ResizeWindow sets a window's size. With tmux's default window-size "latest",
// this is exactly what a client of that size attaching would produce — so it is
// how a scenario simulates picking up a phone (narrow) or a desktop (wide).
func (e *Env) ResizeWindow(sess, win string, w, h int) error {
	out, err := e.run("resize-window", "-t", sess+":"+win, "-x", strconv.Itoa(w), "-y", strconv.Itoa(h))
	if err != nil {
		return fmt.Errorf("resize-window %s:%s to %dx%d: %v: %s", sess, win, w, h, err, out)
	}
	return nil
}

// SidebarInWindow returns the pane id of the tmuxrd sidebar in one window, or
// "" when that window has none.
func (e *Env) SidebarInWindow(sess, win string) (string, error) {
	out, err := e.run("list-panes", "-t", sess+":"+win, "-F", "#{pane_id}\t#{@tmuxrd_sidebar}")
	if err != nil {
		return "", err
	}
	for _, l := range strings.Split(out, "\n") {
		f := strings.Split(l, "\t")
		if len(f) == 2 && strings.TrimSpace(f[1]) == "1" {
			return strings.TrimSpace(f[0]), nil
		}
	}
	return "", nil
}

// WindowLayout is the window's recorded sidebar shape (@tmuxrd_layout).
func (e *Env) WindowLayout(sess, win string) string {
	out, _ := e.run("display-message", "-p", "-t", sess+":"+win, "#{@tmuxrd_layout}")
	return strings.TrimSpace(out)
}

// PaneGeometry returns a pane's left, top, width and height.
func (e *Env) PaneGeometry(paneID string) (left, top, width, height int, err error) {
	out, err := e.run("display-message", "-p", "-t", paneID,
		"#{pane_left}\t#{pane_top}\t#{pane_width}\t#{pane_height}")
	if err != nil {
		return 0, 0, 0, 0, fmt.Errorf("pane geometry %s: %v: %s", paneID, err, out)
	}
	f := strings.Split(strings.TrimSpace(out), "\t")
	if len(f) != 4 {
		return 0, 0, 0, 0, fmt.Errorf("pane geometry %s: bad output %q", paneID, out)
	}
	left, _ = strconv.Atoi(f[0])
	top, _ = strconv.Atoi(f[1])
	width, _ = strconv.Atoi(f[2])
	height, _ = strconv.Atoi(f[3])
	return
}

// WindowGeometry returns a window's width and height.
func (e *Env) WindowGeometry(sess, win string) (width, height int, err error) {
	out, err := e.run("display-message", "-p", "-t", sess+":"+win, "#{window_width}\t#{window_height}")
	if err != nil {
		return 0, 0, fmt.Errorf("window geometry %s:%s: %v", sess, win, err)
	}
	f := strings.Split(strings.TrimSpace(out), "\t")
	if len(f) != 2 {
		return 0, 0, fmt.Errorf("window geometry %s:%s: bad output %q", sess, win, out)
	}
	width, _ = strconv.Atoi(f[0])
	height, _ = strconv.Atoi(f[1])
	return
}

// Adapt runs the layout pass (`tmuxrd adapt`); create also gives a window whose
// sidebar is missing one in the right shape.
func (e *Env) Adapt(create bool) (string, error) {
	args := []string{"adapt"}
	if create {
		args = append(args, "--create")
	}
	return e.RunBin(args...)
}

// SidebarContentIn captures the sidebar of one window.
func (e *Env) SidebarContentIn(sess, win string) (string, error) {
	id, err := e.SidebarInWindow(sess, win)
	if err != nil {
		return "", err
	}
	if id == "" {
		return "", fmt.Errorf("no sidebar in %s:%s", sess, win)
	}
	return e.CapturePane(id), nil
}

// SendKeys types keys into a pane, e.g. to drive the sidebar TUI.
func (e *Env) SendKeys(paneID, keys string) error {
	_, err := e.run("send-keys", "-t", paneID, keys)
	return err
}

// ClickPane sends an SGR mouse left-press at the 0-based cell (x, y) into a
// pane, which is how a tap arrives from a touch client. The SGR wire format is
// 1-based, so 1 is added here; 0-based matches what bubbletea reports in its
// MouseMsg. Writing the raw sequence with send-keys -H is deterministic and
// independent of tmux's own mouse handling.
func (e *Env) ClickPane(paneID string, x, y int) error {
	seq := fmt.Sprintf("\x1b[<0;%d;%dM", x+1, y+1)
	args := []string{"send-keys", "-t", paneID, "-H"}
	for _, b := range []byte(seq) {
		args = append(args, fmt.Sprintf("%02x", b))
	}
	out, err := e.run(args...)
	if err != nil {
		return fmt.Errorf("click %d,%d in %s: %v: %s", x, y, paneID, err, out)
	}
	return nil
}

// accentMarked reports whether a captured ANSI line carries the selection
// accent. The desktop row and the mobile chip are both marked by the accent
// background (ANSI colour 4), so this is how the selection is observed now that
// the desktop has no `>` pointer.
func accentMarked(line string) bool {
	// tmux emits the accent background as its own SGR parameter (44), not a
	// combined one, but accept a combined sequence too so the check does not
	// depend on tmux's exact SGR grouping.
	return strings.Contains(line, "[44m") || strings.Contains(line, ";44m")
}

// SidebarMarked returns the sidebar lines the client currently draws as the
// selection, so a test can tell whether the selection survived (rather than
// vanished).
func (e *Env) SidebarMarked() ([]string, error) {
	id, err := e.SidebarPane()
	if err != nil {
		return nil, err
	}
	out, err := e.Server.Run("capture-pane", "-p", "-e", "-t", id)
	if err != nil {
		return nil, err
	}
	var rows []string
	for _, line := range strings.Split(out, "\n") {
		if accentMarked(line) {
			rows = append(rows, line)
		}
	}
	return rows, nil
}

// CurrentWindow returns the active window index of the given session.
func (e *Env) CurrentWindow(sess string) (string, error) {
	out, err := e.run("list-windows", "-t", sess, "-F",
		"#{window_index} #{window_active}")
	if err != nil {
		return "", err
	}
	for _, l := range nonEmptyLines(out) {
		f := strings.SplitN(l, " ", 2)
		if len(f) == 2 && f[1] == "1" {
			return f[0], nil
		}
	}
	return "", nil
}

// CapturePane returns a pane's visible content.
func (e *Env) CapturePane(id string) string { return e.Server.Capture(id) }

// Detect runs the real detector against this server.
func (e *Env) Detect() []detect.Result { return detect.Detect(e.Server) }

// DetectBySession groups detector results by session name for assertions.
func (e *Env) DetectBySession() map[string][]detect.Result {
	out := map[string][]detect.Result{}
	for _, r := range e.Detect() {
		out[r.Session] = append(out[r.Session], r)
	}
	return out
}

// Summary runs `tmuxrd summary` and parses the counts.
func (e *Env) Summary() (agents, idle, working, blocked int, err error) {
	out, err := e.RunBin("summary")
	if err != nil {
		return
	}
	// agents=%d idle=%d working=%d blocked=%d
	fmt.Sscanf(out, "agents=%d idle=%d working=%d blocked=%d",
		&agents, &idle, &working, &blocked)
	return
}

// SidebarContent reads the rendered sidebar by capturing the sidebar pane.
func (e *Env) SidebarContent() (string, error) {
	ids, err := e.SidebarPanes()
	if err != nil {
		return "", err
	}
	if len(ids) == 0 {
		return "", fmt.Errorf("no sidebar pane")
	}
	return e.CapturePane(ids[0]), nil
}

// SidebarShowsSelection reports whether the sidebar currently draws the row for
// the given window index as selected (fast-switch assertion). The selection is
// the accent background, so this reads the ANSI form and identifies the row by
// its "<index>:" text.
func (e *Env) SidebarShowsSelection(window string) (bool, string, error) {
	id, err := e.SidebarPane()
	if err != nil {
		return false, "", err
	}
	out, err := e.Server.Run("capture-pane", "-p", "-e", "-t", id)
	if err != nil {
		return false, "", err
	}
	for _, l := range strings.Split(out, "\n") {
		if accentMarked(l) && strings.Contains(l, window+":") {
			return true, out, nil
		}
	}
	return false, out, nil
}

// AllSidebarConsistent reports whether every window that should have a sidebar
// has exactly one, and no window has more than one (pane-consistency check).
func (e *Env) AllSidebarConsistent() (bool, string, error) {
	out, err := e.run("list-panes", "-a", "-F",
		"#{session_name}:#{window_index}\t#{@tmuxrd_sidebar}")
	if err != nil {
		return false, "", err
	}
	per := map[string]int{}
	for _, l := range strings.Split(out, "\n") {
		if l == "" {
			continue
		}
		f := strings.Split(l, "\t")
		if len(f) != 2 {
			continue
		}
		if strings.TrimSpace(f[1]) == "1" {
			per[f[0]]++
		}
	}
	for k, v := range per {
		if v != 1 {
			return false, fmt.Sprintf("window %s has %d sidebars", k, v), nil
		}
	}
	return true, "", nil
}

func mustTempDir(prefix string) string {
	d, err := os.MkdirTemp("", prefix)
	if err != nil {
		panic(err)
	}
	return d
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}
