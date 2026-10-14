// Command fakeagent impersonates a coding agent for the integration suite.
//
// It renders a small charm (bubbletea) TUI that looks like a real coding
// agent to internal/detect.classifyTail — a prompt, a running-thinking
// block with a spinner, a permission prompt, … — and sleeps forever under
// its own filename (never exec), so tmux's pane_current_command and
// /proc/<pid>/comm both report the agent name. The same binary with a
// different -agent persona impersonates codex, grok, … without their real
// processes running.
//
// # State API (state = what detect reports)
//
//	-state <name>       flag: idle | working | blocked | unknown
//	<name>              optional first positional arg (back-compat)
//	-state-file <path>  file polled every ~250ms; a changed content switches
//	                    the state of the *running* process (also read from
//	                    FAKEAGENT_STATE_FILE)
//
// Precedence: a non-empty state file wins (it is the live control channel
// tmuxenv.SetAgentState writes), otherwise -state, otherwise the positional
// arg, otherwise idle. An unknown name renders like "unknown".
//
// # Control socket API (dynamic, for tests and for driving a live pane)
//
//	-socket <path>   unix stream socket (also FAKEAGENT_SOCKET); each fake
//	                 listens on its own path, e.g. BinDir/<agent>.sock
//
// One newline-terminated message per connection, answered with one line:
//
//	set <state>        switch state (same names as -state)
//	set msg <text>     conversation message shown in the idle transcript
//	                    (status bar -> "N messages"), never changes state
//	set extra <state>  switch state AND append <state>'s transcript lines
//	                    as a completed exchange (the "block → resumed" flow)
//	get                reply: ok <state> <n> messages
//
// Replies are "ok …" or "err <reason>". Any connection whose message is
// unparseable also switches to the unknown state, so a broken driver is
// visible instead of silently ignored.
//
// # Personas
//
//	-agent <name>  defaults to the basename of argv[0]; sets the greeting
//	               and prompt identity shown by the TUI (pi, codex, grok, …)
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// fakeSleep is the terminal sleep when the TUI cannot start (headless pane):
// the process keeps its own name forever instead of exiting.
const fakeSleep = 100000 * time.Hour

// statePollInterval is how often the state file is re-read.
const statePollInterval = 250 * time.Millisecond

// Known states. renderState must handle every one of them.
const (
	stateIdle    = "idle"
	stateWorking = "working"
	stateBlocked = "blocked"
	stateUnknown = "unknown"
)

// piQuestionFooter is pi's real multi-select footer, captured verbatim. The
// fake renders it together with numbered "[ ]" options so the detection path is
// exercised end to end; keep it byte-identical to pi's output.
const piQuestionFooter = "Enter to select · ↑/↓ to navigate · Space to toggle · n to add notes · Esc to cancel · Ctrl+] to collapse"

// piQuestion renders pi's multiple-choice prompt: two or more numbered "[ ]"
// options and the footer help.
func piQuestion() string {
	return strings.Join([]string{
		" Which cleanup tiers should I run?",
		"",
		"❯ 1. [ ] Tier 1: pure waste (Recommended)",
		"  Stop/disable the crash-looping service, clear journals, delete stale backups.",
		"  2. [ ] Tier 2: rebuildable caches",
		"  Also clear npm, Go module and docker build caches.",
		"  3. [ ] Type something.",
		"",
		piQuestionFooter,
	}, "\n")
}

// notAQuestion is a numbered checklist WITHOUT the footer: the options alone
// must stay Unknown, not Blocked.
func notAQuestion() string {
	return strings.Join([]string{
		"nothing to report",
		"1. [ ] not a question",
		"2. [ ] still not one",
	}, "\n")
}

// The TUI renders richer frames; these banners are the headless fallback
// (no TTY) and the contract for what internal/detect.classifyTail expects.
func banner(state string) string {
	switch state {
	case stateWorking:
		// The braille spinner is what detection keys on, so the headless banner
		// carries one just like the TUI frame does.
		return "⠋ Thinking...\nworking\n↑1.2M ↓132k  27.0%\n"
	case stateBlocked:
		return piQuestion() + "\n"
	case stateUnknown:
		return notAQuestion() + "\n"
	case stateIdle, "":
		return "ready> "
	default:
		return notAQuestion() + "\n"
	}
}

// normalizeState maps CLI/file/socket content onto a known state name.
// Empty means "keep whatever was asked"; an unknown name becomes "unknown"
// so a typo in a test is visible instead of silently looking idle.
func normalizeState(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	switch s {
	case stateIdle, stateWorking, stateBlocked, stateUnknown:
		return s
	default:
		return stateUnknown
	}
}

// ---------------------------------------------------------------------------
// personas

type persona struct {
	greeting string // shown in the idle transcript on first paint
	prompt   string // prompt prefix in the idle state
	tool     string // the command the working state is "running"
}

var personas = map[string]persona{
	"pi":      {greeting: "pi agent session started.", prompt: "pi", tool: "tmuxrd detect"},
	"codex":   {greeting: "codex agent session started.", prompt: "codex", tool: "codex exec"},
	"grok":    {greeting: "grok agent session started.", prompt: "grok", tool: "grok --run"},
	"claude":  {greeting: "claude agent session started.", prompt: "claude", tool: "claude -p"},
	"default": {greeting: "agent session started.", prompt: "agent", tool: "run"},
}

func resolvePersona(agent string) persona {
	if p, ok := personas[agent]; ok {
		return p
	}
	return personas["default"]
}

// ---------------------------------------------------------------------------
// TUI

var (
	stylePrompt   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("14"))
	styleHeader   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("13"))
	styleDim      = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	styleQuestion = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("11"))
	styleState    = map[string]lipgloss.Style{
		stateIdle:    lipgloss.NewStyle().Foreground(lipgloss.Color("2")),
		stateWorking: lipgloss.NewStyle().Foreground(lipgloss.Color("3")),
		stateBlocked: lipgloss.NewStyle().Foreground(lipgloss.Color("1")),
		stateUnknown: lipgloss.NewStyle().Foreground(lipgloss.Color("8")),
	}
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// spinMsg advances the spinner (spinner.MiniDot.FPS).
type spinMsg time.Time

// controlMsg carries a parsed control-socket command into the TUI loop.
type controlMsg struct {
	cmd  string // "set" | "set msg" | "set extra"
	text string // state name for set/set extra, message text for "set msg"
}

type model struct {
	agent   string
	p       persona
	state   string
	spin    int
	msgs    []string // conversation transcript (idle view)
	stateAt time.Time
	width   int
	height  int
}

func newModel(agent string) model {
	p := resolvePersona(agent)
	return model{agent: agent, p: p, state: stateIdle, stateAt: time.Now(),
		msgs: []string{p.greeting}}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(tickSpinner(), tea.WindowSize())
}

func tickSpinner() tea.Cmd {
	return tea.Tick(time.Second/12, func(t time.Time) tea.Msg { return spinMsg(t) })
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case spinMsg:
		if m.state == stateWorking {
			m.spin = (m.spin + 1) % len(spinnerFrames)
			return m, tickSpinner()
		}
		return m, nil
	case controlMsg:
		switch msg.cmd {
		case "set":
			m.state = normalizeState(msg.text)
			m.stateAt = time.Now()
		case "set msg":
			m.msgs = append(m.msgs, msg.text)
		case "set extra":
			st := normalizeState(msg.text)
			m.msgs = append(m.msgs, m.renderExchange(st))
			m.state = st
			m.stateAt = time.Now()
		}
		return m, nil
	}
	return m, nil
}

// renderExchange renders one completed state block as transcript lines, so
// switching away from it (blocked -> working) reads like a real exchange.
func (m model) renderExchange(st string) string {
	switch st {
	case stateWorking:
		return m.p.prompt + "> run " + m.p.tool
	case stateBlocked:
		return strings.Join([]string{
			"Working...",
			"Choose an option:",
			"[y/n]",
		}, "\n")
	default:
		return m.p.prompt + "> " + st
	}
}

// View renders the state frame. Every state keeps detect-visible markers:
// idle ends on a ">" prompt, working carries "Thinking..."/"working", blocked
// carries "[y/n]", unknown has no markers at all.
func (m model) View() string {
	switch m.state {
	case stateWorking:
		elapsed := time.Since(m.stateAt).Round(time.Second)
		return strings.Join([]string{
			m.p.prompt + "> run " + m.p.tool,
			"",
			"  " + spinnerFrames[m.spin] + " Thinking... (" + elapsed.String() + ")",
			"  working",
			"",
			// A realistic status line a real agent shows while working. It
			// carries a token counter ("1.2M") on purpose: blocked detection
			// must never match bare numbers, so this keeps the working case a
			// regression guard for that (see the blockedPats comment).
			styleDim.Render("↑1.2M ↓132k  27.0%"),
			styleDim.Render("esc:cancel  ctrl+c:cancel"),
		}, "\n")
	case stateBlocked:
		return piQuestion()
	case stateUnknown:
		return notAQuestion()
	default: // idle
		var b strings.Builder
		for _, l := range m.msgs {
			b.WriteString(l)
			b.WriteString("\n")
		}
		b.WriteString("\n")
		b.WriteString(stylePrompt.Render(m.p.prompt+">") + " ")
		b.WriteString(styleDim.Render("ctrl+.:shortcuts"))
		return b.String()
	}
}

// ---------------------------------------------------------------------------
// control socket

// serve listens on sock and applies each accepted message to the running
// program. A malformed message switches to the unknown state so a broken
// driver shows up in the pane instead of failing silently.
func serve(sock string, prog *tea.Program) error {
	os.Remove(sock)
	l, err := net.Listen("unix", sock)
	if err != nil {
		return err
	}
	for {
		conn, err := l.Accept()
		if err != nil {
			return err
		}
		go func(c net.Conn) {
			defer c.Close()
			line, err := bufio.NewReader(c).ReadString('\n')
			if err != nil && !errors.Is(err, io.EOF) {
				fmt.Fprintln(c, "err read")
				return
			}
			cmd, text, err := parseControl(strings.TrimSpace(line))
			if err != nil {
				prog.Send(controlMsg{cmd: "set", text: stateUnknown})
				fmt.Fprintln(c, "err", err.Error())
				return
			}
			prog.Send(controlMsg{cmd: cmd, text: text})
			switch cmd {
			case "set", "set extra":
				fmt.Fprintf(c, "ok %s\n", normalizeState(text))
			default:
				fmt.Fprintln(c, "ok")
			}
		}(conn)
	}
}

// parseControl decodes one control-socket line.
func parseControl(line string) (cmd, text string, err error) {
	switch {
	case line == "get":
		return "get", "", nil
	case strings.HasPrefix(line, "set msg "):
		if t := strings.TrimSpace(strings.TrimPrefix(line, "set msg ")); t != "" {
			return "set msg", t, nil
		}
		return "", "", errors.New("empty message")
	case strings.HasPrefix(line, "set extra "):
		if t := normalizeState(strings.TrimPrefix(line, "set extra ")); t == "" {
			return "", "", errors.New("empty state")
		} else {
			return "set extra", t, nil
		}
	case strings.HasPrefix(line, "set "):
		if t := normalizeState(strings.TrimPrefix(line, "set ")); t == "" {
			return "", "", errors.New("empty state")
		} else {
			return "set", t, nil
		}
	default:
		return "", "", fmt.Errorf("unknown command %q", line)
	}
}

// ---------------------------------------------------------------------------
// main

func main() {
	stateFlag := flag.String("state", "", "state to render: idle | working | blocked | unknown")
	stateFile := flag.String("state-file", "", "file polled for state changes (also FAKEAGENT_STATE_FILE)")
	sock := flag.String("socket", "", "control socket path (also FAKEAGENT_SOCKET)")
	agent := flag.String("agent", "", "persona name (defaults to argv[0] basename)")
	flag.Parse()

	who := *agent
	if who == "" {
		who = filepath.Base(os.Args[0])
	}

	path := *stateFile
	if path == "" {
		path = os.Getenv("FAKEAGENT_STATE_FILE")
	}
	spath := *sock
	if spath == "" {
		spath = os.Getenv("FAKEAGENT_SOCKET")
	}

	// initial state: -state > positional > idle; a non-empty state file
	// overrides both (it is the live control channel).
	state := normalizeState(*stateFlag)
	if state == "" && flag.NArg() > 0 {
		state = normalizeState(flag.Arg(0))
	}
	if path != "" {
		if b, err := os.ReadFile(path); err == nil {
			if s := normalizeState(string(b)); s != "" {
				state = s
			}
		}
	}
	if state == "" {
		state = stateIdle
	}

	m := newModel(who)
	m.state = state
	prog := tea.NewProgram(m, tea.WithAltScreen())
	if spath != "" {
		go func() {
			if err := serve(spath, prog); err != nil {
				fmt.Fprintln(os.Stderr, "fakeagent control socket:", err)
			}
		}()
	}

	// state-file poller: a changed file switches the live TUI's state.
	if path != "" {
		shown := state
		go func() {
			for range time.Tick(statePollInterval) {
				b, err := os.ReadFile(path)
				if err != nil {
					continue
				}
				next := normalizeState(string(b))
				if next == "" || next == shown {
					continue
				}
				shown = next
				prog.Send(controlMsg{cmd: "set", text: next})
			}
		}()
	}

	// The pane is a real TTY when tmux runs us, so bubbletea renders the TUI.
	// If Run fails (headless capture, no TTY at all) fall back to printing
	// plain banners forever so detection still works and the process name
	// never changes.
	if _, err := prog.Run(); err != nil {
		fmt.Print(banner(state))
		for range time.Tick(statePollInterval) {
			if path == "" {
				time.Sleep(fakeSleep)
				return
			}
			b, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			if s := normalizeState(string(b)); s != "" && s != state {
				state = s
				fmt.Print(banner(state))
			}
		}
	}
}
