package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
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
	// What is "layout" when the sender only wants the reshape pass
	// (window-layout-changed). Anything else is a full refresh.
	What string `json:"what,omitempty"`
	// Target is "session:window" for a window notify. The hook expands it, so
	// the daemon can move the accent without listing panes.
	Target string `json:"target,omitempty"`
}

// SidebarInfo is one tmuxrd pane, as the daemon last saw it. The client uses
// it to highlight its own session's active window, to focus that window's
// sidebar on j/k, and to decide whether it is the sidebar on screen.
type SidebarInfo struct {
	Session  string `json:"session"`
	Window   string `json:"window"`
	Pane     string `json:"pane"`
	Active   bool   `json:"active"`
	Attached bool   `json:"attached"`
}

type stateMsg struct {
	Type     string          `json:"type"`
	Results  []detect.Result `json:"results"`
	Sidebars []SidebarInfo   `json:"sidebars"`
	// Gen orders sidebar snapshots. A refresh that listed earlier carries an
	// older gen, and the client keeps the newer active window.
	Gen int `json:"gen"`
}

func barsEqual(a, b []SidebarInfo) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sidebarInfos(panes []tmux.Pane) []SidebarInfo {
	out := []SidebarInfo{}
	for _, p := range panes {
		if !p.Sidebar {
			continue
		}
		out = append(out, SidebarInfo{
			Session: p.Session, Window: p.Window, Pane: p.ID,
			Active: p.Active, Attached: p.Attached,
		})
	}
	return out
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

// windowAgentOption records which agent a window belongs to. tmux names a
// window from its active pane, so focusing the sidebar (command "tmuxrd")
// renames an unpinned window to "tmuxrd" and the sidebar then reads
// "tmuxrd (pi)". The format keeps automatic-rename on: a set option wins,
// and a window with no agent still follows pane_current_command.
const windowAgentOption = "@tmuxrd_agent"

const windowRenameFormat = "#{?@tmuxrd_agent,#{@tmuxrd_agent},#{pane_current_command}}"

// unknownGrace is how long a pane may read unread before its working period is
// really closed. "The screen said nothing" is not "the agent stopped": pi draws
// its spinner in the editor's top border and the slash-command menu below the
// editor's bottom border, so the spinner can leave the tail window the detector
// reads while the turn is still live. Holding the period across those ticks
// keeps the timer honest (it counts on and never restarts), and the grace bounds
// the damage when a pane has genuinely gone quiet — a stopped agent whose screen
// the detector cannot read still shows its ✓ a few seconds later.
const unknownGrace = 10 * time.Second

// windowNamePlan is one agent window whose name pin is missing or whose name
// has already become "tmuxrd". Rename is false when the visible name is
// already the agent and only the option needs writing.
type windowNamePlan struct {
	Target string
	Agent  string
	Rename bool
}

// planAgentWindowNames decides which windows to pin. One agent pane owns the
// window; a later pane in the same window does not override it. A window that
// is already pinned and not named tmuxrd is left alone, including one the
// operator renamed by hand.
func planAgentWindowNames(panes []tmux.Pane, results []detect.Result) []windowNamePlan {
	byID := make(map[string]tmux.Pane, len(panes))
	for _, p := range panes {
		byID[p.ID] = p
	}
	seen := map[string]bool{}
	var out []windowNamePlan
	for _, r := range results {
		if r.Agent == "" || r.Session == "" || r.Window == "" {
			continue
		}
		target := r.Session + ":" + r.Window
		if seen[target] {
			continue
		}
		p, ok := byID[r.PaneID]
		if !ok {
			continue
		}
		seen[target] = true
		if p.WindowAgent == r.Agent && p.WindowName != "tmuxrd" {
			continue
		}
		out = append(out, windowNamePlan{
			Target: target,
			Agent:  r.Agent,
			Rename: p.WindowName != r.Agent,
		})
	}
	return out
}

// stampWindowNames copies a planned rename onto the snapshot so the sidebar
// does not draw "tmuxrd (pi)" for the rest of this tick.
func stampWindowNames(res []detect.Result, plans []windowNamePlan) {
	if len(plans) == 0 {
		return
	}
	name := make(map[string]string, len(plans))
	for _, p := range plans {
		if p.Rename {
			name[p.Target] = p.Agent
		}
	}
	if len(name) == 0 {
		return
	}
	for i := range res {
		if n, ok := name[res[i].Session+":"+res[i].Window]; ok {
			res[i].WindowName = n
		}
	}
}

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
		bars      []SidebarInfo
		workTimes = map[string]*workTime{}
		// barGen bumps every time a notify publishes the active window on its
		// own, ahead of the refresh worker. A refresh that listed earlier must
		// not write those sidebars back.
		barGen int
	)
	screens := detect.NewScreenCache()
	initSessionCache()
	// Tunables and @tmuxrd_enabled change rarely. Reading them on every 500ms
	// tick was several tmux processes per refresh; re-read every few seconds.
	// TrustEnabled tells the layout pass not to read the option again.
	var (
		layoutOpts layout.Options
		enabled    = true
		optsAt     time.Time
	)
	reloadOpts := func() {
		if !optsAt.IsZero() && time.Since(optsAt) < 5*time.Second {
			return
		}
		layoutOpts = layout.OptionsFromServer(srv)
		layoutOpts.TrustEnabled = true
		enabled = srv.ShowOption("@tmuxrd_enabled") != "0"
		optsAt = time.Now()
	}
	// applyWorkTime times the current working period for each pane. Starting to
	// work (re)sets it, so an agent that goes idle and then works again starts
	// from zero rather than resuming; stopping freezes the value it reached. A
	// reading of Unknown holds the period open instead of closing it (see
	// unknownGrace).
	type pinWrite struct {
		id    string
		unset bool
	}
	// applyWorkTime does not touch tmux. The caller writes the returned pin
	// options after dropping mu, so a list-panes is never waiting on a lock
	// that is itself waiting on tmux.
	applyWorkTime := func(res []detect.Result, now time.Time) []pinWrite {
		var writes []pinWrite
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
				writes = append(writes, pinWrite{id: r.PaneID, unset: true})
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
					writes = append(writes, pinWrite{id: r.PaneID})
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
		return writes
	}
	applyPins := func(writes []pinWrite) {
		for _, w := range writes {
			if w.unset {
				srv.UnsetPaneOption(w.id, paneFinishedOption)
				continue
			}
			srv.SetPaneOption(w.id, paneFinishedOption, "1")
		}
	}
	// applyWindowNames pins each agent window once. The create hook only sees
	// the command at the moment the window appears, which is a shell when the
	// agent is started afterwards, so the pin is missing and the next sidebar
	// focus renames the window. Steady state writes nothing.
	applyWindowNames := func(plans []windowNamePlan) {
		if len(plans) == 0 {
			return
		}
		args := make([]string, 0, len(plans)*14)
		for i, p := range plans {
			if i > 0 {
				args = append(args, ";")
			}
			args = append(args,
				"set-option", "-w", "-t", p.Target, windowAgentOption, p.Agent, ";",
				"set-option", "-w", "-t", p.Target, "automatic-rename-format", windowRenameFormat,
			)
			if p.Rename {
				args = append(args, ";", "rename-window", "-t", p.Target, p.Agent)
			}
		}
		srv.Run(args...)
	}
	// sendMu keeps snapshot writes ordered. publishBars and refresh write the
	// same connections from different goroutines; a torn write is a dropped
	// line, and the dropped line is the one that moved the accent.
	var sendMu sync.Mutex
	sendState := func(conns []net.Conn, res []detect.Result, sb []SidebarInfo, gen int) {
		if sb == nil {
			sb = []SidebarInfo{}
		}
		b, _ := json.Marshal(stateMsg{Type: "state", Results: res, Sidebars: sb, Gen: gen})
		b = append(b, '\n')
		sendMu.Lock()
		defer sendMu.Unlock()
		for _, c := range conns {
			if _, err := c.Write(b); err != nil {
				mu.Lock()
				delete(clients, c)
				mu.Unlock()
				// Leave the conn open. Notify clients write one command and
				// exit without reading; closing here drops that unread
				// command, so a window switch never moves the accent.
			}
		}
	}
	// connsLocked copies the client set. The caller holds mu.
	connsLocked := func() []net.Conn {
		out := make([]net.Conn, 0, len(clients))
		for c := range clients {
			out = append(out, c)
		}
		return out
	}
	// A full notify that arrives while a refresh is listing or capturing has
	// already changed the current window. pendingFull reports that, so the
	// refresh can abandon the stale snapshot and let the worker re-list.
	// Declared here, before refresh, because the closure reads it; enqueue
	// below is what sets the flag.
	var (
		pendMu     sync.Mutex
		pendFull   bool
		pendLayout bool
	)
	pendingFull := func() bool {
		pendMu.Lock()
		defer pendMu.Unlock()
		return pendFull
	}
	currentGen := func() int {
		mu.Lock()
		defer mu.Unlock()
		return barGen
	}
	// publishActive marks one session's window current from the hook's own
	// target and pushes that before any list-panes. after-select-window
	// already names the window it selected; listing here would sit behind a
	// capture the refresh still has in flight, and the 250ms budget would
	// expire on the previous window.
	publishActive := func(target string) bool {
		sess, win, ok := strings.Cut(target, ":")
		if !ok || sess == "" || win == "" {
			return false
		}
		mu.Lock()
		if len(bars) == 0 {
			mu.Unlock()
			return false
		}
		next := append([]SidebarInfo(nil), bars...)
		found := false
		for i := range next {
			if next[i].Session != sess {
				continue
			}
			next[i].Active = next[i].Window == win
			if next[i].Active {
				found = true
			}
		}
		if !found || barsEqual(bars, next) {
			mu.Unlock()
			return found
		}
		barGen++
		gen := barGen
		bars = next
		prev := append([]detect.Result(nil), state...)
		conns := connsLocked()
		mu.Unlock()
		sendState(conns, prev, next, gen)
		return true
	}
	// publishBars lists panes and pushes sidebar focus immediately. A window
	// switch has to move the accent within 250ms, and the refresh worker may
	// be inside capture-pane. The hook path calls this before enqueueing.
	publishBars := func() {
		mu.Lock()
		barGen++
		gen := barGen
		mu.Unlock()
		panes, err := srv.ListPanes()
		if err != nil {
			return
		}
		infos := sidebarInfos(panes)
		mu.Lock()
		if barGen != gen || barsEqual(bars, infos) {
			mu.Unlock()
			return
		}
		bars = append([]SidebarInfo(nil), infos...)
		prev := append([]detect.Result(nil), state...)
		conns := connsLocked()
		mu.Unlock()
		sendState(conns, prev, infos, gen)
	}
	// refresh reports whether a newer full notify superseded it. The caller
	// re-runs so the snapshot it publishes was listed after that notify.
	refresh := func() bool {
		// One refresh at a time: a notify and the ticker can both fire, and the
		// layout pass kills panes. Two concurrent passes could each split.
		refreshMu.Lock()
		defer refreshMu.Unlock()
		listedGen := currentGen()
		reloadOpts()
		// One list-panes per refresh: the layout pass and detection read the
		// same snapshot. capture-pane runs only for agent panes whose
		// fingerprint moved (or whose cached classification has gone stale).
		panes, err := srv.ListPanes()
		listed := err == nil
		if err != nil {
			panes = nil
		}
		if pendingFull() {
			return true
		}
		// Reshape only: creating panes is the config hooks' job, so a server
		// that never loaded tmuxrd never grows sidebars. The same pass reaps
		// duplicate sidebars and puts a drifted width or strip height back.
		replaced := false
		if enabled {
			replaced, _ = layout.EnsurePanes(srv, panes, layoutOpts, false)
		}
		if pendingFull() {
			return true
		}
		// A shape change replaces the sidebar pane. The list above still names
		// the pane that was killed; take another so clients learn the new id.
		// A resize does not, and costs no second listing.
		if replaced {
			if again, err := srv.ListPanes(); err == nil {
				panes = again
			}
			if pendingFull() {
				return true
			}
		}
		// The marker follows these sidebars. Publish them before capture-pane,
		// unless a notify already owns a newer list. Pairing our older list
		// with that newer gen lets it arrive last and stick for the rest of
		// the 250ms hook budget: the client keeps the highest gen it has seen.
		infos := sidebarInfos(panes)
		mu.Lock()
		if barGen != listedGen {
			mu.Unlock()
			return true
		}
		changed := !barsEqual(bars, infos)
		if changed {
			bars = append([]SidebarInfo(nil), infos...)
		}
		gen := barGen
		prev := append([]detect.Result(nil), state...)
		sbEarly := append([]SidebarInfo(nil), bars...)
		conns := connsLocked()
		mu.Unlock()
		if changed {
			sendState(conns, prev, sbEarly, gen)
		}
		if pendingFull() {
			return true
		}
		now := time.Now()
		res := detect.DetectPanesCached(srv, panes, screens, now, func() bool {
			return pendingFull() || currentGen() != listedGen
		})
		mu.Lock()
		if pendingFull() || barGen != listedGen {
			mu.Unlock()
			return true
		}
		writes := applyWorkTime(res, now)
		names := planAgentWindowNames(panes, res)
		stampWindowNames(res, names)
		state = res
		bars = append([]SidebarInfo(nil), infos...)
		snap := append([]detect.Result(nil), res...)
		sb := append([]SidebarInfo(nil), bars...)
		gen = barGen
		conns = connsLocked()
		mu.Unlock()
		applyPins(writes)
		applyWindowNames(names)
		sendState(conns, snap, sb, gen)
		// A failed list must not replace the ledger with an empty snapshot.
		// The write is a local file, and only when the set of agents changed.
		if listed {
			observeSessions(srv, panes, snap)
		}
		return false
	}
	// layoutRefresh is the window-layout-changed path: reshape, and do not
	// capture every agent. A burst of those hooks used to run a full detect.
	layoutRefresh := func() {
		refreshMu.Lock()
		defer refreshMu.Unlock()
		listedGen := currentGen()
		reloadOpts()
		if !enabled {
			return
		}
		panes, err := srv.ListPanes()
		if err != nil {
			return
		}
		replaced, err := layout.EnsurePanes(srv, panes, layoutOpts, false)
		if err != nil || !replaced {
			return
		}
		again, err := srv.ListPanes()
		if err != nil {
			return
		}
		infos := sidebarInfos(again)
		mu.Lock()
		// A newer notify owns the marker. Publishing this list would either
		// carry a stale gen or stamp the newer gen on the older windows.
		if barGen != listedGen {
			mu.Unlock()
			return
		}
		bars = append([]SidebarInfo(nil), infos...)
		gen := barGen
		snap := append([]detect.Result(nil), state...)
		sb := append([]SidebarInfo(nil), bars...)
		conns := connsLocked()
		mu.Unlock()
		sendState(conns, snap, sb, gen)
	}

	// Coalesce notify bursts into one queued refresh. A full refresh upgrades
	// a queued layout-only one: the full pass reshapes too. The worker is the
	// only caller of refresh besides the synchronous first pass below, so a
	// hook storm cannot stack captures. pendFull and pendLayout live next to
	// refresh, which reads them to abandon a stale pass.
	kick := make(chan struct{}, 1)
	enqueue := func(full bool) {
		pendMu.Lock()
		if full {
			pendFull = true
		} else {
			pendLayout = true
		}
		pendMu.Unlock()
		select {
		case kick <- struct{}{}:
		default:
		}
	}
	go func() {
		for range kick {
			for {
				pendMu.Lock()
				full, lay := pendFull, pendLayout
				pendFull, pendLayout = false, false
				pendMu.Unlock()
				if full {
					// A notify that landed mid-pass superseded the snapshot.
					// Loop now, rather than waiting for the kick, so the
					// re-list is not stuck behind the channel.
					if refresh() {
						continue
					}
					break
				}
				if lay {
					layoutRefresh()
				}
				break
			}
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
			snap := append([]detect.Result(nil), state...)
			sb := append([]SidebarInfo(nil), bars...)
			gen := barGen
			mu.Unlock()
			// Read the command before the hello write. A notify process
			// writes one line and exits; the hello often fails, and it must
			// not run (or close the conn) before that line is handled.
			go func(c net.Conn) {
				defer func() {
					mu.Lock()
					delete(clients, c)
					mu.Unlock()
					c.Close()
				}()
				sc := bufio.NewScanner(c)
				for sc.Scan() {
					var m daemonMsg
					if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
						continue
					}
					switch m.Cmd {
					case "notify":
						// A window hook names the window it selected. Mark that
						// before any list, and do not follow it with a list
						// that may have been queued on the other side of the
						// select.
						if m.What == "window" && publishActive(m.Target) {
							enqueue(true)
							break
						}
						// Other full notifies (focus, split, kill) still move
						// the accent. Publish that before the worker's capture.
						if m.What != "layout" {
							publishBars()
						}
						enqueue(m.What != "layout")
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
			}(conn)
			sendState([]net.Conn{conn}, snap, sb, gen)
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
			enqueue(true)
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
	what, target := "", ""
	if len(os.Args) > 2 {
		what = os.Args[2]
	}
	if len(os.Args) > 3 {
		target = os.Args[3]
	}
	b, _ := json.Marshal(daemonMsg{Cmd: "notify", What: what, Target: target})
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
	if _, err := layout.EnsurePanes(srv, panes, opts, true); err != nil {
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
			program.Send(stateUIMsg{Results: sm.Results, Sidebars: sm.Sidebars, Gen: sm.Gen})
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
	Results  []detect.Result
	Sidebars []SidebarInfo
	Gen      int
}
