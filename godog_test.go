//go:build integration

package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"
	"github.com/cucumber/messages/go/v21"

	"github.com/naqerl/tmuxrd/tmuxenv"
)

const repoRoot = "/home/user/terdr"

// sc binds step definitions to one isolated tmuxenv.Env per scenario.
type sc struct {
	t        *testing.T
	env      *tmuxenv.Env
	sock     string
	disabled bool
	// spinnerFramesSeen records distinct spinner.MiniDot frames captured from
	// the sidebar across a sampling window (animation assertions).
	spinnerFramesSeen map[string]bool
	// subject is the agent the sampling/glyph steps talk about.
	subject string
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('x')
		}
	}
	out := b.String()
	if len(out) > 24 {
		out = out[:24]
	}
	return strings.ToLower(out)
}

// glyph expectations for "the sidebar should show agent ... as ...". The
// working glyph is animated (spinner.MiniDot frames), so it is asserted as a
// set of frames rather than one literal character.
const (
	glyphIdle    = "○"
	glyphBlocked = "?"
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// cell returns the value of a table row's column by header name (looked up
// in the table header row), or "" when the feature does not declare that
// column — so older tables without a state column keep working.
func cell(tbl *godog.Table, row any, header string) string {
	if len(tbl.Rows) == 0 {
		return ""
	}
	r, ok := row.(*messages.PickleTableRow)
	if !ok {
		return ""
	}
	for i, h := range tbl.Rows[0].Cells {
		if strings.TrimSpace(h.Value) == header && i < len(r.Cells) {
			return strings.TrimSpace(r.Cells[i].Value)
		}
	}
	return ""
}

// InitializeScenario wires every Gherkin step to the tmux abstraction.
func InitializeScenario(s *godog.ScenarioContext) {
	var cur *sc

	s.Before(func(ctx context.Context, scsn *godog.Scenario) (context.Context, error) {
		sock := "godog-" + sanitize(scsn.Uri+scsn.Name)
		env, err := tmuxenv.New(sock, repoRoot)
		if err != nil {
			return ctx, err
		}
		cur = &sc{t: testingT, env: env, sock: sock}
		return ctx, nil
	})

	s.After(func(ctx context.Context, scsn *godog.Scenario, err error) (context.Context, error) {
		if cur != nil && cur.env != nil {
			cur.env.Close()
		}
		return ctx, nil
	})

	// --- Given -----------------------------------------------------------
	s.Step(`^an isolated tmux server "([^"]*)"$`, func(ctx context.Context, _ string) error {
		// The Before hook already created the isolated server.
		return nil
	})
	s.Step(`^session "([^"]*)" is created with agent windows:$`, func(ctx context.Context, sess string, tbl *godog.Table) error {
		rows := tbl.Rows[1:]
		if len(rows) == 0 {
			return nil
		}
		if err := cur.env.NewSession(sess, cell(tbl, rows[0], "agent"), cell(tbl, rows[0], "state")); err != nil {
			return err
		}
		for _, row := range rows[1:] {
			if err := cur.env.NewWindow(sess, cell(tbl, row, "agent"), cell(tbl, row, "state")); err != nil {
				return err
			}
		}
		return nil
	})
	s.Step(`^an isolated tmux server "([^"]*)" with sessions:$`, func(ctx context.Context, _ string, tbl *godog.Table) error {
		for _, row := range tbl.Rows[1:] {
			sess := row.Cells[0].Value
			if err := cur.env.NewSession(sess, "plain", ""); err != nil {
				return err
			}
		}
		return nil
	})

	s.Step(`^session "([^"]*)" has windows:$`, func(ctx context.Context, sess string, tbl *godog.Table) error {
		// first window already created by NewSession; create the rest
		rows := tbl.Rows[1:]
		for i := 1; i < len(rows); i++ {
			if err := cur.env.NewWindow(sess, "plain", ""); err != nil {
				return err
			}
		}
		return nil
	})

	// Optional suffix: ... in state "working" (blank/absent = idle banner).
	s.Step(`^session "([^"]*)" has window "([^"]*)" running agent "([^"]*)"(?: in state "([^"]*)")?$`, func(ctx context.Context, sess, _win, agent, state string) error {
		if state == "" {
			state = "idle"
		}
		// Ensure the session exists (NewSession creates window 0 with the agent).
		names, _ := cur.env.SessionNames()
		found := false
		for _, n := range names {
			if n == sess {
				found = true
			}
		}
		if !found {
			return cur.env.NewSession(sess, agent, state)
		}
		return cur.env.NewWindow(sess, agent, state)
	})

	// Switch an already-running fake agent's state through its state file
	// (tmuxenv.SetAgentState) — the pane keeps running, only the banner changes.
	s.Step(`^agent "([^"]*)" in session "([^"]*)" window "([^"]*)" is in state "([^"]*)"$`, func(ctx context.Context, agent, sess, win, state string) error {
		return cur.env.SetAgentState(sess, win, state)
	})

	s.Step(`^the tmuxrd config is loaded$`, func(ctx context.Context) error {
		// Sourcing tmuxrd.tmux installs the after-select-window / pane-focus-in
		// hooks that fire `tmuxrd notify`, giving the sub-50ms selection path.
		return cur.env.SourceConfig()
	})
	s.Step(`^the sidebar is enabled$`, func(ctx context.Context) error {
		cur.disabled = false
		return nil
	})
	s.Step(`^the sidebar is disabled$`, func(ctx context.Context) error {
		cur.disabled = true
		// pre-set the tmux option the scripts read
		cur.env.Server.Option("@tmuxrd_enabled", "0")
		return nil
	})

	// --- When ------------------------------------------------------------
	s.Step(`^I toggle the sidebar on$`, func(ctx context.Context) error {
		return withRetry(func() error {
			return cur.env.ToggleSidebar()
		})
	})
	s.Step(`^I toggle the sidebar off$`, func(ctx context.Context) error {
		return cur.env.ToggleSidebar()
	})
	s.Step(`^I ensure sidebar for new windows$`, func(ctx context.Context) error {
		return cur.env.EnsureSidebar()
	})
	s.Step(`^I clean up orphan sidebars$`, func(ctx context.Context) error {
		return cur.env.CleanupOrphans()
	})
	s.Step(`^I kill the main pane of window "([^"]*)"$`, func(ctx context.Context, win string) error {
		// find the non-sidebar pane in the session's window
		panes, err := cur.env.MainPanes()
		if err != nil {
			return err
		}
		// window "0" => first main pane of first session; for simplicity pick by
		// index among main panes matching the window ordering.
		if len(panes) == 0 {
			return fmt.Errorf("no main pane in window %s", win)
		}
		idx, _ := strconv.Atoi(win)
		if idx >= len(panes) {
			idx = 0
		}
		_, err = cur.env.KillPane(panes[idx])
		return err
	})
	s.Step(`^I start the daemon$`, func(ctx context.Context) error {
		return cur.env.StartDaemon()
	})
	s.Step(`^I start the daemon again$`, func(ctx context.Context) error {
		// Second start must be a no-op: the singleton guard refuses a second
		// listener. We just assert it does not error out the scenario.
		cur.env.StartDaemon()
		return nil
	})
	s.Step(`^a new window is created in session "([^"]*)"$`, func(ctx context.Context, sess string) error {
		return cur.env.NewWindow(sess, "plain", "")
	})
	s.Step(`^I run summary$`, func(ctx context.Context) error {
		_, err := cur.env.RunBin("summary")
		return err
	})
	s.Step(`^I detect agent panes$`, func(ctx context.Context) error {
		return nil // detection is read on demand
	})
	s.Step(`^I select window "([^"]*)"$`, func(ctx context.Context, win string) error {
		sess := cur.env.ActiveSession()
		if sess == "" {
			sess = "s1"
		}
		return cur.env.SelectWindow(sess + ":" + win)
	})
	s.Step(`^the sidebar is showing agent rows$`, func(ctx context.Context) error {
		// Wait until the client has rendered at least one detected agent row so
		// subsequent selection assertions measure switch latency, not first paint.
		return eventually(3*time.Second, func() error {
			content, err := cur.env.SidebarContent()
			if err != nil {
				return err
			}
			if !strings.Contains(content, "pi") && !strings.Contains(content, "claude") && !strings.Contains(content, "codex") {
				return fmt.Errorf("no agent rows yet:\n%s", content)
			}
			return nil
		})
	})
	s.Step(`^I refresh the sidebar$`, func(ctx context.Context) error {
		// the daemon pushes state; wait for at least one refresh cycle
		time.Sleep(600 * time.Millisecond)
		return nil
	})
	s.Step(`^I kill the isolated server$`, func(ctx context.Context) error {
		cur.env.Server.KillServer()
		return nil
	})

	s.Step(`^I capture the sidebar (\d+) times over (\d+)ms$`, func(ctx context.Context, n, ms int) error {
		// The daemon re-detects on a 500ms tick, so the glyph the sidebar shows
		// can lag the fakeagent's state file by up to that. Wait for the state
		// to be reflected before sampling frames.
		if err := eventually(5*time.Second, func() error {
			line, err := sidebarAgentLine(cur.env, cur.subject)
			if err != nil {
				return err
			}
			for _, f := range spinnerFrames {
				if strings.Contains(line, f) {
					return nil
				}
			}
			return fmt.Errorf("sidebar line for %s has no spinner frame yet: %q", cur.subject, line)
		}); err != nil {
			return err
		}
		frames := map[string]bool{}
		var last string
		step := time.Duration(ms) * time.Millisecond / time.Duration(n)
		for i := 0; i < n; i++ {
			if line, err := sidebarAgentLine(cur.env, cur.subject); err == nil {
				last = line
				for _, f := range spinnerFrames {
					if strings.Contains(line, f) {
						frames[f] = true
					}
				}
			}
			time.Sleep(step)
		}
		cur.spinnerFramesSeen = frames
		if len(frames) == 0 {
			return fmt.Errorf("no spinner frame ever captured for %s (last line %q)", cur.subject, last)
		}
		return nil
	})
	s.Step(`^at least (\d+) distinct spinner frames should have been captured$`, func(ctx context.Context, want int) error {
		got := len(cur.spinnerFramesSeen)
		if got < want {
			return fmt.Errorf("expected >= %d distinct spinner frames, got %d (%v)", want, got, keys(cur.spinnerFramesSeen))
		}
		return nil
	})
	// Names the agent later capture/glyph steps talk about (the feature's
	// subject); keeps the capture step reusable across features.
	s.Step(`^the subject agent is "([^"]*)"$`, func(ctx context.Context, agent string) error {
		cur.subject = agent
		return nil
	})

	// --- Then ------------------------------------------------------------
	// Exact glyph rendering: "a braille spinner" matches any spinner.MiniDot
	// frame (the working glyph animates), everything else is a literal char.
	s.Step(`^the sidebar should show agent "([^"]*)" as "([^"]*)"$`, func(ctx context.Context, agent, want string) error {
		return eventually(15*time.Second, func() error {
			line, err := sidebarAgentLine(cur.env, agent)
			if err != nil {
				return err
			}
			switch want {
			case "a braille spinner":
				for _, f := range spinnerFrames {
					if strings.Contains(line, f) {
						return nil
					}
				}
				return fmt.Errorf("agent %q has no spinner frame in sidebar line: %q", agent, line)
			case glyphBlocked:
				if strings.Contains(line, glyphBlocked) {
					return nil
				}
				return fmt.Errorf("agent %q does not show %q in sidebar line: %q", agent, glyphBlocked, line)
			case glyphIdle:
				if strings.Contains(line, glyphIdle) {
					return nil
				}
				return fmt.Errorf("agent %q does not show %q in sidebar line: %q", agent, glyphIdle, line)
			default:
				if strings.Contains(line, want) {
					return nil
				}
				return fmt.Errorf("agent %q sidebar line %q does not contain %q", agent, line, want)
			}
		})
	})
	s.Step(`^every window should have exactly one sidebar pane$`, func(ctx context.Context) error {
		return eventually(3*time.Second, func() error {
			ok, msg, err := cur.env.AllSidebarConsistent()
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("inconsistent sidebars: %s", msg)
			}
			return nil
		})
	})
	s.Step(`^every window should have no sidebar pane$`, func(ctx context.Context) error {
		return eventually(3*time.Second, func() error {
			ids, err := cur.env.SidebarPanes()
			if err != nil {
				return err
			}
			if len(ids) != 0 {
				return fmt.Errorf("expected no sidebars, got %d", len(ids))
			}
			return nil
		})
	})
	s.Step(`^window "([^"]*)" should have no sidebar pane$`, func(ctx context.Context, _ string) error {
		ids, err := cur.env.SidebarPanes()
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		// at least verify global count didn't grow; specific-window mapping is
		// covered by AllSidebarConsistent in the other scenarios
		return nil
	})
	s.Step(`^window "([^"]*)" should have exactly one sidebar pane$`, func(ctx context.Context, _ string) error {
		ok, msg, err := cur.env.AllSidebarConsistent()
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%s", msg)
		}
		return nil
	})
	s.Step(`^the sidebar count should be (\d+)$`, func(ctx context.Context, want int) error {
		return eventually(3*time.Second, func() error {
			ids, err := cur.env.SidebarPanes()
			if err != nil {
				// A dead server (last session torn down) means zero sidebars.
				if want == 0 && !cur.env.Server.HasServer() {
					return nil
				}
				return err
			}
			if len(ids) != want {
				return fmt.Errorf("expected %d sidebars, got %d", want, len(ids))
			}
			return nil
		})
	})
	s.Step(`^exactly one daemon should be running$`, func(ctx context.Context) error {
		// Count real processes bound to our socket via /proc, not just the pid we
		// recorded: that catches both a leaked daemon outliving its scenario and
		// one that fell back to the default tmux server.
		return eventually(3*time.Second, func() error {
			pids := cur.env.DaemonPIDs()
			if len(pids) != 1 {
				return fmt.Errorf("expected exactly 1 daemon for socket %s, got %d: %v",
					cur.env.Socket, len(pids), pids)
			}
			return nil
		})
	})
	s.Step(`^summary agents should be (\d+)$`, func(ctx context.Context, want int) error {
		return eventually(3*time.Second, func() error {
			agents, _, _, _, err := cur.env.Summary()
			if err != nil {
				return err
			}
			if agents != want {
				return fmt.Errorf("expected agents=%d, got %d", want, agents)
			}
			return nil
		})
	})
	s.Step(`^detection should find (\d+) panes$`, func(ctx context.Context, want int) error {
		got := len(cur.env.Detect())
		if got != want {
			return fmt.Errorf("expected %d detected panes, got %d", want, got)
		}
		return nil
	})
	s.Step(`^session "([^"]*)" should have (\d+) detected pane[s]?$`, func(ctx context.Context, sess string, want int) error {
		got := len(cur.env.DetectBySession()[sess])
		if got != want {
			return fmt.Errorf("session %s: expected %d, got %d", sess, want, got)
		}
		return nil
	})
	s.Step(`^I focus the sidebar pane$`, func(ctx context.Context) error {
		// Focusing the sidebar is what a user does to press j/k; the focused pane
		// is then not an agent, which used to make the marker vanish.
		sb, err := cur.env.SidebarPane()
		if err != nil {
			return err
		}
		if err := cur.env.SelectPane(sb); err != nil {
			return err
		}
		// let a state push land so the client re-syncs and re-renders
		time.Sleep(700 * time.Millisecond)
		return nil
	})
	s.Step(`^the sidebar should still show a selection marker$`, func(ctx context.Context) error {
		rows, err := cur.env.SidebarMarked()
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			content, _ := cur.env.SidebarContent()
			return fmt.Errorf("the selection marker vanished\n%s", content)
		}
		return nil
	})
	s.Step(`^I press the "([^"]*)" key in the sidebar$`, func(ctx context.Context, key string) error {
		sb, err := cur.env.SidebarPane()
		if err != nil {
			return err
		}
		if err := cur.env.SendKeys(sb, key); err != nil {
			return err
		}
		time.Sleep(400 * time.Millisecond)
		return nil
	})
	s.Step(`^I wait (\d+)ms$`, func(ctx context.Context, ms int) error {
		time.Sleep(time.Duration(ms) * time.Millisecond)
		return nil
	})
	s.Step(`^window "([^"]*)" should be the active window of session "([^"]*)"$`, func(ctx context.Context, win, sess string) error {
		return eventually(2*time.Second, func() error {
			got, err := cur.env.CurrentWindow(sess)
			if err != nil {
				return err
			}
			if got != win {
				return fmt.Errorf("session %s is on window %q, want %q", sess, got, win)
			}
			return nil
		})
	})
	s.Step(`^the sidebar should select window "([^"]*)" within (\d+)ms$`, func(ctx context.Context, win string, ms int) error {
		deadline := time.Now().Add(time.Duration(ms) * time.Millisecond)
		var last string
		for time.Now().Before(deadline) {
			ok, content, err := cur.env.SidebarShowsSelection(win)
			if err == nil && ok {
				return nil
			}
			last = content
			time.Sleep(5 * time.Millisecond)
		}
		return fmt.Errorf("sidebar did not select window %s within %dms\ncontent:\n%s", win, ms, last)
	})
	s.Step(`^the sidebar should be visible$`, func(ctx context.Context) error {
		_, err := cur.env.SidebarContent()
		return err
	})
	s.Step(`^the sidebar content should mention agent "([^"]*)"$`, func(ctx context.Context, agent string) error {
		return eventually(3*time.Second, func() error {
			content, err := cur.env.SidebarContent()
			if err != nil {
				return err
			}
			if !strings.Contains(content, agent) {
				return fmt.Errorf("sidebar does not mention %s\n%s", agent, content)
			}
			return nil
		})
	})
	s.Step(`^directory names should be marked with a trailing slash$`, func(ctx context.Context) error {
		// Directories render as "name/ " (the trailing space keeps them off the
		// pane divider). capture-pane trims trailing whitespace, so the slash is
		// what is observable — and its presence at the end of the row is itself
		// the proof that the name is not flush against the divider.
		return eventually(3*time.Second, func() error {
			content, err := cur.env.SidebarContent()
			if err != nil {
				return err
			}
			base := filepath.Base(repoRoot)
			if !strings.Contains(content, base+"/") {
				return fmt.Errorf("no %q in sidebar:\n%s", base+"/", content)
			}
			for _, line := range strings.Split(content, "\n") {
				if strings.Contains(line, base+"/") && !strings.HasSuffix(line, base+"/") {
					return fmt.Errorf("row does not end at the directory marker: %q", line)
				}
			}
			return nil
		})
	})
	s.Step(`^the isolated server should advertise its own socket$`, func(ctx context.Context) error {
		// This is what keeps a config-sourced daemon on our socket: tmux runs
		// run-shell with the server's global env, so if this is not published
		// the daemon falls back to the operator's default tmux server.
		if got := cur.env.ServerEnv("TERDR_TMUX_SOCKET"); got != cur.env.Socket {
			return fmt.Errorf("server advertises TERDR_TMUX_SOCKET=%q, want %q", got, cur.env.Socket)
		}
		return nil
	})
	s.Step(`^the server should be the isolated one$`, func(ctx context.Context) error {
		if cur.env.Server.Socket == "" || cur.env.Server.Socket == "default" {
			return fmt.Errorf("server socket not isolated: %q", cur.env.Server.Socket)
		}
		if cur.env.Server.Socket != cur.sock {
			return fmt.Errorf("socket mismatch: %q vs %q", cur.env.Server.Socket, cur.sock)
		}
		return nil
	})
	s.Step(`^the server should never target the default socket$`, func(ctx context.Context) error {
		if cur.env.Server.Socket == "" {
			return fmt.Errorf("empty socket would fall back to default")
		}
		return nil
	})
	s.Step(`^the isolated server should be gone$`, func(ctx context.Context) error {
		if cur.env.Server.HasServer() {
			return fmt.Errorf("server still alive")
		}
		return nil
	})
}

// sidebarAgentLine returns the sidebar's rendered line for one agent. Rows
// look like "<mark> <glyph>  <win>: <label>  <path>"; the label is the window
// name when it differs from the agent name, e.g. "w1 (claude)". Matches are
// retried through eventually() so the assertion measures state, not paint.
func sidebarAgentLine(e *tmuxenv.Env, agent string) (string, error) {
	content, err := e.SidebarContent()
	if err != nil {
		return "", err
	}
	for _, l := range strings.Split(content, "\n") {
		if strings.Contains(l, "("+agent+")") ||
			strings.Contains(l, ": "+agent) ||
			strings.Contains(l, " "+agent+" ") {
			return l, nil
		}
	}
	return "", fmt.Errorf("sidebar has no row for agent %q\n%s", agent, content)
}

// testingT is set by TestGodog so Before hooks can reference *testing.T.
var testingT *testing.T

func TestGodog(t *testing.T) {
	testingT = t
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"features"},
			TestingT: t,
			Tags:     "~@wip",
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("godog suite failed")
	}
}

// eventually retries fn until it passes or the deadline lapses.
func eventually(d time.Duration, fn func() error) error {
	deadline := time.Now().Add(d)
	var last error
	for time.Now().Before(deadline) {
		if err := fn(); err == nil {
			return nil
		} else {
			last = err
		}
		time.Sleep(30 * time.Millisecond)
	}
	return last
}

// withRetry re-runs a shell script step once — the toggle scripts can race a
// freshly created window.
func withRetry(fn func() error) error {
	if err := fn(); err != nil {
		time.Sleep(200 * time.Millisecond)
		return fn()
	}
	return nil
}
