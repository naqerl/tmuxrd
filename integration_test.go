//go:build integration

package main

import (
	"os"
	"testing"
	"time"

	"github.com/naqerl/terdr/internal/tmux"
)

func setupServer(t *testing.T) {
	t.Helper()
	os.Setenv("TERDR_TMUX_SOCKET", "terdr-go-int")
	tmux.Run("kill-server")
	t.Cleanup(func() { tmux.Run("kill-server") })

	dir := t.TempDir()
	if err := os.WriteFile(dir+"/pi", []byte("#!/bin/sh\nsleep 1000\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	tmux.Run("new-session", "-d", "-s", "t", "pi")
	tmux.Run("source-file", "/home/user/terdr/terdr.tmux")
	time.Sleep(300 * time.Millisecond)
}

func TestIntegrationToggle(t *testing.T) {
	setupServer(t)

	tmux.Run("run-shell", "/home/user/terdr/scripts/toggle_sidebar.sh >/dev/null 2>&1")
	time.Sleep(800 * time.Millisecond)
	got, _ := tmux.Run("list-panes", "-a", "-F", "#{@terdr_sidebar}")
	if count(got, "1") != 1 {
		t.Fatalf("expected 1 sidebar after toggle on, got %d", count(got, "1"))
	}

	tmux.Run("run-shell", "/home/user/terdr/scripts/toggle_sidebar.sh >/dev/null 2>&1")
	time.Sleep(400 * time.Millisecond)
	got, _ = tmux.Run("list-panes", "-a", "-F", "#{@terdr_sidebar}")
	if count(got, "1") != 0 {
		t.Fatalf("expected 0 sidebars after toggle off, got %d", count(got, "1"))
	}
}

func TestIntegrationOrphanCleanup(t *testing.T) {
	setupServer(t)

	tmux.Run("run-shell", "/home/user/terdr/scripts/toggle_sidebar.sh >/dev/null 2>&1")
	time.Sleep(800 * time.Millisecond)

	main, _ := tmux.Run("list-panes", "-t", "t", "-F", "#{pane_id} #{@terdr_sidebar}")
	t.Logf("panes before kill: %q", main)
	mainPane := ""
	for _, line := range splitLines(main) {
		fields := splitSpaces(line)
		if len(fields) >= 1 {
			mark := ""
			if len(fields) > 1 {
				mark = fields[1]
			}
			if mark != "1" {
				mainPane = fields[0]
			}
		}
	}
	if mainPane == "" {
		t.Fatal("main pane not found")
	}

	tmux.Run("kill-pane", "-t", mainPane)
	time.Sleep(700 * time.Millisecond)
	tmux.Run("run-shell", "/home/user/terdr/scripts/cleanup_orphans.sh >/dev/null 2>&1")
	time.Sleep(300 * time.Millisecond)
	got2, err2 := tmux.Run("list-panes", "-a", "-F", "#{@terdr_sidebar}")
	t.Logf("after kill: panes=%q err=%v", got2, err2)

	if _, err := tmux.Run("list-panes", "-a", "-F", "#{@terdr_sidebar}"); err == nil {
		t.Fatal("expected sidebar orphan cleanup to close the window/server")
	}
}

func count(s, needle string) int {
	n := 0
	for _, line := range splitLines(s) {
		if line == needle {
			n++
		}
	}
	return n
}

func splitLines(s string) []string {
	var out []string
	cur := ""
	for _, c := range s {
		if c == '\n' {
			out = append(out, cur)
			cur = ""
		} else {
			cur += string(c)
		}
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

func splitSpaces(s string) []string {
	var out []string
	cur := ""
	for _, c := range s {
		if c == ' ' {
			if cur != "" {
				out = append(out, cur)
				cur = ""
			}
		} else {
			cur += string(c)
		}
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
