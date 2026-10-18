package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/naqerl/tmuxrd/internal/detect"
	"github.com/naqerl/tmuxrd/internal/layout"
	"github.com/naqerl/tmuxrd/internal/tmux"
)

type daemonMsg struct {
	Cmd  string `json:"cmd"`
	Pane string `json:"pane,omitempty"`
}

type stateMsg struct {
	Type    string          `json:"type"`
	Results []detect.Result `json:"results"`
}

// workTime times one pane's current working period: since is when the period
// started (zero when the pane is not working) and last is the duration the
// previous period reached, kept so the sidebar can show it while stopped.
// hold is when the current run of Unknown readings began (zero otherwise), the
// clock behind unknownGrace.
type workTime struct {
	last  time.Duration
	since time.Time
	hold  time.Time
}

var socketPath string

// runtimeBase is the directory the daemon socket and the adapt lock live in:
// the user's runtime dir when there is one, the temp dir otherwise.
func runtimeBase() string {
	if base := os.Getenv("XDG_RUNTIME_DIR"); base != "" {
		return base
	}
	return os.TempDir()
}

// initSocketPath derives the daemon's Unix socket. It is namespaced by the tmux
// socket name so that two different tmux servers each get their own daemon and
// never collide on a single shared path.
func initSocketPath(srv tmux.Server) {
	if socketPath != "" {
		return
	}
	name := srv.SocketFile()
	socketPath = filepath.Join(runtimeBase(), "tmuxrd-"+name+".sock")
}

// adaptLockPath is the per-server advisory lock file that serialises sidebar
// creation. It sits next to the daemon socket and is keyed by the same tmux
// socket identity, so exactly the `adapt --create` invocations aimed at one tmux
// server exclude each other — and no two servers share a lock.
func adaptLockPath(srv tmux.Server) string {
	return filepath.Join(runtimeBase(), "tmuxrd-"+srv.SocketFile()+".adapt.lock")
}

// lockAdapt takes the exclusive adapt lock. The returned file must stay open
// for the lock to be held; the caller closes it (or exits, which closes it).
// Blocking LOCK_EX is deliberate: concurrent after-new-* hooks queue behind the
// first, and by the time each gets the lock the sidebars it would have created
// already exist in the pane list it is about to read.
func lockAdapt(srv tmux.Server) (*os.File, error) {
	f, err := os.OpenFile(adaptLockPath(srv), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// paneFinishedOption is the per-pane marker the daemon sets the first time it
// sees the pane's agent working. It lives in tmux (pane options survive a
// daemon restart and die with the pane), which is what lets the sidebar show ✓
// for an agent that has finished a run and reserve ○ for one that never
// started.
const paneFinishedOption = "@tmuxrd_finished"

// unknownGrace is how long a pane may read unread before its working period is
// really closed. "The screen said nothing" is not "the agent stopped": pi draws
// its spinner in the editor's top border and the slash-command menu below the
// editor's bottom border, so the spinner can leave the tail window the detector
// reads while the turn is still live. Holding the period across those ticks
// keeps the timer honest (it counts on and never restarts), and the grace bounds
// the damage when a pane has genuinely gone quiet — a stopped agent whose screen
// the detector cannot read still shows its ✓ a few seconds later.
const unknownGrace = 10 * time.Second

func cmdDaemon() {
	srv := tmux.NewServerFromEnv()
	initSocketPath(srv)
	// Singleton guard: an exclusive lock file, not a dial of the socket. Two
	// starts can both fail to dial before either has bound, and then both would
	// remove the socket and listen — leaving two daemons scanning and pushing.
	// flock makes "only one daemon per socket" atomic.
	lockPath := socketPath + ".lock"
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		fmt.Println("daemon lock:", err)
		os.Exit(1)
	}
	defer lockFile.Close()
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		fmt.Println("tmuxrd daemon already running")
		return
	}
	os.Remove(socketPath)

	l, err := net.Listen("unix", socketPath)
	if err != nil {
		fmt.Println("daemon listen:", err)
		os.Exit(1)
	}
	defer l.Close()
	defer os.Remove(socketPath)

	var (
		mu        sync.Mutex
		refreshMu sync.Mutex
		clients   = map[net.Conn]struct{}{}
		state     []detect.Result
		workTimes = map[string]*workTime{}
	)
	// Layout tunables are read once: they are tmux user options that change
	// rarely, and the reshape pass runs on every refresh.
	layoutOpts := layout.OptionsFromServer(srv)
	// applyWorkTime times the current working period for each pane. Starting to
	// work (re)sets it, so an agent that goes idle and then works again starts
	// from zero rather than resuming; stopping freezes the value it reached. A
	// reading of Unknown holds the period open instead of closing it (see
	// unknownGrace).
	applyWorkTime := func(res []detect.Result, now time.Time) {
		seen := make(map[string]bool, len(res))
		for i := range res {
			r := &res[i]
			seen[r.PaneID] = true
			wt := workTimes[r.PaneID]
			if wt == nil {
				wt = &workTime{}
				workTimes[r.PaneID] = wt
			}
			// The pin is consumed by looking: if the agent's window is the one a
			// client is displaying, an unseen result no longer exists. Cleared
			// before the working branch so an agent that works and stops in the
			// window you are watching never flashes a ✓ for a tick. Detect has
			// already promoted an idle pinned pane to Finished, so the state is
			// put back to Idle here rather than recomputed.
			if r.Pinned && r.Viewed {
				srv.UnsetPaneOption(r.PaneID, paneFinishedOption)
				r.Pinned = false
				if r.State == detect.Finished {
					r.State = detect.Idle
				}
			}
			if r.State == detect.Working {
				// The agent has done work: record it on the pane itself so the ✓
				// survives a daemon restart (and an upgrade that restarts only
				// the daemon). Detect reads it back from tmux on the next tick,
				// so this write happens once per pane. A viewed pane is skipped:
				// the user is seeing this run, so it is not a notification.
				if !r.Pinned && !r.Viewed {
					srv.SetPaneOption(r.PaneID, paneFinishedOption, "1")
					r.Pinned = true
				}
				if wt.since.IsZero() {
					// A new working period: start the timer over.
					wt.since = now
					wt.last = 0
				}
				wt.hold = time.Time{}
				r.Worked = now.Sub(wt.since)
				continue
			}
			if r.Unread && !wt.since.IsZero() {
				// A gap in detection, not a stop. Hold the period open and keep
				// timing: an overlay drawn over the spinner must not reset a run
				// that is still going. After unknownGrace the pane is treated as
				// stopped, so an agent that has really gone quiet is not timed
				// forever. A pane that never worked has nothing to hold.
				if wt.hold.IsZero() {
					wt.hold = now
				}
				if now.Sub(wt.hold) < unknownGrace {
					// Still inside the grace: report the run as live, so the sidebar
					// keeps the spinner and the timer counts on instead of dropping
					// to 0s the moment the overlay closes.
					r.State = detect.Working
					r.Worked = now.Sub(wt.since)
					continue
				}
			}
			wt.hold = time.Time{}
			if !wt.since.IsZero() {
				wt.last = now.Sub(wt.since)
				wt.since = time.Time{}
			}
			r.Worked = wt.last
		}
		for id := range workTimes {
			if !seen[id] {
				delete(workTimes, id)
			}
		}
	}
	refresh := func() {
		// One refresh at a time: a notify (a goroutine) and the ticker can both
		// fire, and the layout pass creates panes — two concurrent passes could
		// each decide a window is missing its sidebar and split twice.
		refreshMu.Lock()
		defer refreshMu.Unlock()
		// One list-panes per refresh: the layout pass and detection read the
		// same snapshot, so a refresh costs one listing plus one capture per
		// agent pane (not one listing per phase and a capture per pane).
		panes, err := srv.ListPanes()
		if err != nil {
			panes = nil
		}
		// Keep every sidebar the shape its window's width calls for. Reshape
		// only: creating panes is the config hooks' job, so a server that never
		// loaded tmuxrd never grows sidebars. The same pass reaps duplicate
		// sidebars, so an already-poisoned server heals here.
		layout.EnsurePanes(srv, panes, layoutOpts, false)
		res := detect.DetectPanes(srv, panes)
		now := time.Now()
		mu.Lock()
		applyWorkTime(res, now)
		state = res
		snap := append([]detect.Result(nil), res...)
		conns := make([]net.Conn, 0, len(clients))
		for c := range clients {
			conns = append(conns, c)
		}
		mu.Unlock()
		b, _ := json.Marshal(stateMsg{Type: "state", Results: snap})
		b = append(b, '\n')
		for _, c := range conns {
			_, err := c.Write(b)
			if err != nil {
				mu.Lock()
				delete(clients, c)
				mu.Unlock()
				c.Close()
			}
		}
	}

	// Coalesce notify bursts into (at most) one queued refresh. A burst of tmux
	// hooks — one `j` can fire pane-focus-in and after-select-pane together, and
	// a resize fires window-layout-changed for each step — used to start one full
	// refresh each, each listing and capturing every agent. A capacity-1 channel
	// with a single worker collapses them: the first notify starts a refresh
	// immediately (no added latency a user could see), and any that arrive while
	// it runs leave a single token behind, so at most one more refresh follows.
	// refreshMu still serialises the worker against the ticker.
	notify := make(chan struct{}, 1)
	go func() {
		for range notify {
			refresh()
		}
	}()

	// accept loop
	// stop is signalled by the "quit" message, which `tmuxrd stop` sends: a
	// precise way to shut down the daemon for one server (and how the installer
	// restarts it) without pattern-killing every tmuxrd daemon on the machine.
	stop := make(chan struct{}, 1)
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			clients[conn] = struct{}{}
			// send current state
			snap := append([]detect.Result(nil), state...)
			mu.Unlock()
			b, _ := json.Marshal(stateMsg{Type: "state", Results: snap})
			b = append(b, '\n')
			conn.Write(b)

			go func(c net.Conn) {
				sc := bufio.NewScanner(c)
				for sc.Scan() {
					var m daemonMsg
					if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
						continue
					}
					switch m.Cmd {
					case "notify":
						select {
						case notify <- struct{}{}:
						default:
						}
					case "focus":
						srv.FocusPane("", m.Pane)
					case "quit":
						select {
						case stop <- struct{}{}:
						default:
						}
						return
					}
				}
				mu.Lock()
				delete(clients, c)
				mu.Unlock()
				c.Close()
			}(conn)
		}
	}()

	refresh()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	ticks, gone := 0, 0
	for {
		select {
		case <-ticker.C:
			// If the server we serve has gone away, exit rather than poll a dead
			// socket forever (that is how a daemon outlives its server and has to
			// be hunted down later). Checked every ~5s, and twice before acting.
			ticks++
			if ticks%10 == 0 {
				if srv.HasServer() {
					gone = 0
				} else {
					gone++
					if gone >= 2 {
						return
					}
				}
			}
			refresh()
		case <-sig:
			return
		case <-stop:
			return
		}
	}
}

func cmdNotify() {
	srv := tmux.NewServerFromEnv()
	initSocketPath(srv)
	conn, err := net.DialTimeout("unix", socketPath, 200*time.Millisecond)
	if err != nil {
		return
	}
	defer conn.Close()
	b, _ := json.Marshal(daemonMsg{Cmd: "notify"})
	b = append(b, '\n')
	conn.Write(b)
}

// cmdStop asks the daemon for this server to shut down. The message goes to
// the socket resolved from TERDR_TMUX_SOCKET or $TMUX, so it can only reach
// this server's daemon — unlike a pattern kill, which would take every tmuxrd
// daemon on the machine with it. The installer uses this to restart the daemon
// after replacing the binary.
func cmdStop() {
	srv := tmux.NewServerFromEnv()
	initSocketPath(srv)
	conn, err := net.DialTimeout("unix", socketPath, 200*time.Millisecond)
	if err != nil {
		return
	}
	defer conn.Close()
	b, _ := json.Marshal(daemonMsg{Cmd: "quit"})
	b = append(b, '\n')
	conn.Write(b)
}

// cmdAdapt reshapes every tmuxrd sidebar to match the width of the window it is
// in: a 32-column left pane on a desktop, a 2-row top strip on a phone.
//
// Without --create it only reshapes sidebars that already exist (the same work
// the daemon does every tick). With --create it also gives a window whose
// sidebar is missing one in the right shape — the toggle and ensure scripts use
// that so a new window opens already adapted.
//
// --create is single-owner: concurrent hook invocations (run-shell -b
// backgrounds each one) would otherwise each read "no sidebar" and each split
// one in. An exclusive advisory lock is held across the pane list and the create
// pass, so the first caller creates and every later caller re-reads a list that
// already contains those sidebars.
func cmdAdapt() {
	srv := tmux.NewServerFromEnv()
	create := false
	for _, a := range os.Args[2:] {
		if a == "--create" {
			create = true
		}
	}
	// Read the tunables before locking: they are three tmux calls, and holding
	// the lock for them would only delay the other hook invocations.
	opts := layout.OptionsFromServer(srv)

	if !create {
		if err := layout.Ensure(srv, opts, false); err != nil {
			fmt.Fprintln(os.Stderr, "adapt:", err)
			os.Exit(1)
		}
		return
	}

	if f, err := lockAdapt(srv); err == nil {
		defer func() {
			syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
			f.Close()
		}()
	} else {
		// An unwritable runtime dir should not stop the sidebar from adapting;
		// the layout pass's self-heal still bounds any duplicates this lets
		// through.
		fmt.Fprintln(os.Stderr, "adapt: lock:", err)
	}
	// List after taking the lock and build from that list, or the snapshot would
	// predate the winner's sidebars and this caller would create its own.
	panes, err := srv.ListPanes()
	if err != nil {
		fmt.Fprintln(os.Stderr, "adapt:", err)
		os.Exit(1)
	}
	if err := layout.EnsurePanes(srv, panes, opts, true); err != nil {
		fmt.Fprintln(os.Stderr, "adapt:", err)
		os.Exit(1)
	}
}

// runClient runs one sidebar pane's UI. The daemon connection is owned by a
// goroutine that reconnects on its own (see streamStates), so a daemon restart
// is invisible to the user.
func runClient() {
	initSocketPath(tmux.NewServerFromEnv())
	m := newModel(tmux.NewServerFromEnv())
	// Cell-motion mouse mode: clicks and wheel events are what let a touch
	// client drive the sidebar (tap a row or chip). tmux must also have
	// `mouse on` for it to forward the events. bubbletea disables it on exit.
	program := tea.NewProgram(m, tea.WithMouseCellMotion())
	done := make(chan struct{})
	go streamStates(program, done)
	program.Run()
	close(done)
}

// streamStates keeps a connection to the daemon alive and feeds every state
// snapshot into the program.
//
// It reconnects rather than giving up. That matters more than it looks: if the
// daemon is restarted (an upgrade, a crash, a restart during development) the
// old code's socket reader just ended, and the sidebar went on rendering its
// last snapshot forever. The working spinner still animated because it is
// local, so the symptom was a *frozen timer* and a selection marker that only
// moved on the 5s local tick — which reads as "the cursor is slow". A sidebar
// that loses the daemon must reattach and catch up.
func streamStates(program *tea.Program, done <-chan struct{}) {
	for {
		if !sleepOrDone(0, done) {
			return
		}
		conn, err := net.Dial("unix", socketPath)
		if err != nil {
			if !sleepOrDone(500*time.Millisecond, done) {
				return
			}
			continue
		}
		sc := bufio.NewScanner(conn)
		for sc.Scan() {
			var sm stateMsg
			if err := json.Unmarshal(sc.Bytes(), &sm); err != nil {
				continue
			}
			program.Send(stateUIMsg{Results: sm.Results})
		}
		conn.Close()
		// The daemon closed the connection (a restart, or it exited because its
		// server went away): wait a beat, then reattach.
		if !sleepOrDone(500*time.Millisecond, done) {
			return
		}
	}
}

// sleepOrDone sleeps for d, returning false if done closed first. A d of zero
// just checks done.
func sleepOrDone(d time.Duration, done <-chan struct{}) bool {
	if d <= 0 {
		select {
		case <-done:
			return false
		default:
			return true
		}
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-done:
		return false
	}
}

type stateUIMsg struct {
	Results []detect.Result
}
