//go:build integration

package main

import (
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/naqerl/terdr/internal/tmux"
)

func TestIntegrationToggleAndOrphan(t *testing.T) {
	os.Setenv("TERDR_TMUX_SOCKET", "terdr-go-int")
	tmux.Run("kill-server")
	defer tmux.Run("kill-server")

	// mock agent binary named pi
	dir := t.TempDir()
	mock := dir + "/pi"
	if err := os.WriteFile(mock, []byte("#!/bin/sh\nsleep 1000\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	tmux.Run("new-session", "-d", "-s", "t", "pi")
	tmux.Run("new-window")
	tmux.Run("source-file", "/home/user/terdr/terdr.tmux")
	time.Sleep(300 * time.Millisecond)

	tmux.Run("run-shell", "/home/user/terdr/scripts/toggle_sidebar.sh >/dev/null 2>&1")
	time.Sleep(800 * time.Millisecond)
	if got, _ := tmux.Run("list-panes", "-a", "-F", "#{@terdr_sidebar}"); count(got, "1") != 2 {
		t.Fatalf("expected 2 sidebars, got %d\n%s", count(got, "1"), got)
	}

	tmux.Run("run-shell", "/home/user/terdr/scripts/toggle_sidebar.sh >/dev/null 2>&1")
	time.Sleep(400 * time.Millisecond)
	if got, _ := tmux.Run("list-panes", "-a", "-F", "#{@terdr_sidebar}"); count(got, "1") != 0 {
		t.Fatalf("expected 0 sidebars, got %d", count(got, "1"))
	}

	// orphan cleanup
	tmux.Run("new-session", "-d", "-s", "u", "pi")
	tmux.Run("run-shell", "/home/user/terdr/scripts/toggle_sidebar.sh >/dev/null 2>&1")
	time.Sleep(600 * time.Millisecond)
	_ = exec.Command("true")
	tmux.Run("kill-pane", "-t", "u:0.0")
	time.Sleep(500 * time.Millisecond)
	if got, _ := tmux.Run("list-panes", "-a", "-F", "#{@terdr_sidebar}"); count(got, "1") > 2 {
		t.Fatalf("orphan sidebars left: %d", count(got, "1"))
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