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
	"github.com/naqerl/terdr/internal/detect"
	"github.com/naqerl/terdr/internal/tmux"
)

type daemonMsg struct {
	Cmd  string `json:"cmd"`
	Pane string `json:"pane,omitempty"`
}

type stateMsg struct {
	Type    string          `json:"type"`
	Results []detect.Result `json:"results"`
}

var socketPath string

func initSocketPath() {
	if socketPath != "" {
		return
	}
	base := os.Getenv("XDG_RUNTIME_DIR")
	if base == "" {
		base = os.TempDir()
	}
	socketPath = filepath.Join(base, "terdr.sock")
}

func cmdDaemon() {
	initSocketPath()
	// singleton guard
	if conn, err := net.DialTimeout("unix", socketPath, 200*time.Millisecond); err == nil {
		conn.Close()
		fmt.Println("terdr daemon already running")
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
		mu       sync.Mutex
		clients  = map[net.Conn]struct{}{}
		state    []detect.Result
	)
	refresh := func() {
		res := detect.Detect()
		mu.Lock()
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
						tmux.FocusPane(m.Pane)
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
	ticker := time.NewTicker(2 * time.Second)
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
	initSocketPath()
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
	initSocketPath()
	// connect
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		// fall back: try launching daemon
		fmt.Fprintln(os.Stderr, "daemon not available")
		os.Exit(1)
	}
	m := model{}
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