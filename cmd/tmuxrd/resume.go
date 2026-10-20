package main

import (
	"fmt"
	"os"
	"time"

	"github.com/naqerl/tmuxrd/internal/detect"
	"github.com/naqerl/tmuxrd/internal/sessions"
	"github.com/naqerl/tmuxrd/internal/tmux"
)

func serverSocket(srv tmux.Server) sessions.Socket {
	switch {
	case srv.SocketPath != "":
		return sessions.Socket{Kind: "path", Value: srv.SocketPath}
	case srv.Socket != "":
		return sessions.Socket{Kind: "name", Value: srv.Socket}
	default:
		return sessions.Socket{Kind: "default"}
	}
}

func serverFor(kind, value string) tmux.Server {
	switch kind {
	case "path":
		return tmux.Server{SocketPath: value}
	case "name":
		return tmux.Server{Socket: value}
	default:
		return tmux.Server{}
	}
}

// sessionCache is the daemon's ledger. Sync runs on the refresh goroutine.
var sessionCache *sessions.Cache

func initSessionCache() {
	sessionCache = sessions.NewCache(sessions.DefaultRoots())
}

func observeSessions(srv tmux.Server, panes []tmux.Pane, results []detect.Result) {
	if sessionCache == nil {
		return
	}
	ps := make([]sessions.Pane, 0, len(panes))
	for _, p := range panes {
		ps = append(ps, sessions.Pane{
			ID: p.ID, PID: p.PID, Cmd: p.Cmd, Path: p.Path,
			Session: p.Session, Window: p.Window, WindowName: p.WindowName,
			Title: p.Title, Sidebar: p.Sidebar,
		})
	}
	as := make([]sessions.Agent, 0, len(results))
	for _, r := range results {
		as = append(as, sessions.Agent{
			PaneID: r.PaneID, Agent: r.Agent, Session: r.Session,
			Window: r.Window, WindowName: r.WindowName, Path: r.Path,
		})
	}
	sessionCache.Sync(serverSocket(srv), ps, as, time.Now())
}

func cmdResume() {
	roots := sessions.DefaultRoots()
	rows, err := sessions.Load(roots.LedgerPath())
	if err != nil {
		fmt.Fprintln(os.Stderr, "resume:", err)
		os.Exit(1)
	}
	if len(rows) == 0 {
		fmt.Println("no agents recorded")
		return
	}
	open := map[string]bool{}
	for _, live := range sessions.OpenSessions(roots) {
		open[live.Agent+"\x00"+live.SessionID] = true
	}
	actions := sessions.Plan(rows, func(pid int, start int64) bool {
		return sessions.Alive(roots.Proc, pid, start)
	}, func(agent, id string) bool {
		return open[agent+"\x00"+id]
	})
	failed := false
	started := map[string]bool{}
	for _, a := range actions {
		r := a.Record
		if a.Command == "" {
			fmt.Printf("skip %s %s (%s)\n", r.Agent, r.SessionID, a.Skip)
			continue
		}
		if r.Session == "" {
			fmt.Fprintf(os.Stderr, "resume %s %s: no tmux session recorded\n", r.Agent, r.SessionID)
			failed = true
			continue
		}
		srv := serverFor(r.SocketKind, r.SocketValue)
		name := r.WindowName
		if name == "" {
			name = r.Agent
		}
		key := r.SocketKind + "\x00" + r.SocketValue + "\x00" + r.Session
		var out string
		var runErr error
		if started[key] || hasSession(srv, r.Session) {
			out, runErr = runIn(srv, r.Session, name, r.Cwd, a.Command)
			started[key] = runErr == nil || started[key]
		} else {
			out, runErr = runNew(srv, r.Session, name, r.Cwd, a.Command)
			if runErr == nil {
				started[key] = true
			}
		}
		if runErr != nil {
			failed = true
			fmt.Fprintf(os.Stderr, "resume %s %s: %v %s\n", r.Agent, r.SessionID, runErr, out)
			continue
		}
		fmt.Printf("resumed %s %s in %s:%s\n", r.Agent, r.SessionID, r.Session, r.Window)
	}
	if failed {
		os.Exit(1)
	}
}

func runNew(srv tmux.Server, session, name, cwd, command string) (string, error) {
	args := []string{"new-session", "-d", "-s", session, "-n", name}
	if cwd != "" {
		args = append(args, "-c", cwd)
	}
	args = append(args, command)
	return srv.RunCombined(args...)
}

func runIn(srv tmux.Server, session, name, cwd, command string) (string, error) {
	args := []string{"new-window", "-d", "-t", session, "-n", name}
	if cwd != "" {
		args = append(args, "-c", cwd)
	}
	args = append(args, command)
	return srv.RunCombined(args...)
}

func hasSession(srv tmux.Server, name string) bool {
	_, err := srv.Run("has-session", "-t", name)
	return err == nil
}
