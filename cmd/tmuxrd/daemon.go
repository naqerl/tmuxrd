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
type workTime struct {
	last  time.Duration
	since time.Time
}

var socketPath string

// initSocketPath derives the daemon's Unix socket. It is namespaced by the tmux
// socket name so that two different tmux servers each get their own daemon and
// never collide on a single shared path.
func initSocketPath(srv tmux.Server) {
	if socketPath != "" {
		return
	}
	base := os.Getenv("XDG_RUNTIME_DIR")
	if base == "" {
		base = os.TempDir()
	}
	name := srv.SocketFile()
	socketPath = filepath.Join(base, "tmuxrd-"+name+".sock")
}

func cmdDaemon() {
	srv := tmux.NewServerFromEnv()
	initSocketPath(srv)
	// singleton guard
	if conn, err := net.DialTimeout("unix", socketPath, 200*time.Millisecond); err == nil {
		conn.Close()
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
		clients   = map[net.Conn]struct{}{}
		state     []detect.Result
		workTimes = map[string]*workTime{}
	)
	// applyWorkTime times the current working period for each pane. Starting to
	// work (re)sets it, so an agent that goes idle and then works again starts
	// from zero rather than resuming; stopping freezes the value it reached.
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
			if r.State == detect.Working {
				if wt.since.IsZero() {
					// A new working period: start the timer over.
					wt.since = now
					wt.last = 0
				}
				r.Worked = now.Sub(wt.since)
				continue
			}
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
		res := detect.Detect(srv)
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

	// accept loop
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
						go refresh()
					case "focus":
						srv.FocusPane(m.Pane)
					case "quit":
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
	for {
		select {
		case <-ticker.C:
			refresh()
		case <-sig:
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

func runClient() {
	initSocketPath(tmux.NewServerFromEnv())
	// connect
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		// fall back: try launching daemon
		fmt.Fprintln(os.Stderr, "daemon not available")
		os.Exit(1)
	}
	m := newModel(tmux.NewServerFromEnv())
	// reader goroutine sends states into a channel stored in model? We'll use tea.Msg via Cmd.
	program := tea.NewProgram(m)
	go func() {
		sc := bufio.NewScanner(conn)
		for sc.Scan() {
			var sm stateMsg
			if err := json.Unmarshal(sc.Bytes(), &sm); err != nil {
				continue
			}
			program.Send(stateUIMsg{Results: sm.Results})
		}
	}()
	// keep conn open by blocking in cmd
	program.Run()
	conn.Close()
}

type stateUIMsg struct {
	Results []detect.Result
}
