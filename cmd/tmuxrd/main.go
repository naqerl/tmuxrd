package main

import (
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/naqerl/tmuxrd/internal/detect"
	"github.com/naqerl/tmuxrd/internal/tmux"
)

type tickMsg time.Time
type refreshMsg struct{}

// stateInterval is how often the sidebar re-detects agent state; the working
// spinner animates independently at spinner.MiniDot.FPS between refreshes.
const (
	stateInterval = 5 * time.Second
)

type model struct {
	srv     tmux.Server
	results []detect.Result
	cursor  int
	width   int
	spin    spinner.Model
}

// newModel builds a client model with the working spinner ready to animate.
func newModel(srv tmux.Server) model {
	s := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	return model{srv: srv, spin: s}
}

func waitForSignal() tea.Msg {
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGUSR1)
	<-c
	signal.Stop(c)
	return refreshMsg{}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		tea.Tick(stateInterval, func(t time.Time) tea.Msg { return tickMsg(t) }),
		m.spin.Tick,
	)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case stateUIMsg:
		m.results = msg.Results
		if cp := m.srv.CurrentPane(); cp != "" {
			found := false
			for i, r := range m.results {
				if r.PaneID == cp {
					m.cursor = i
					found = true
					break
				}
			}
			if !found {
				m.cursor = -1
			}
		}
	case tickMsg:
		m.results = detect.Detect(m.srv)
		if cp := m.srv.CurrentPane(); cp != "" {
			found := false
			for i, r := range m.results {
				if r.PaneID == cp {
					m.cursor = i
					found = true
					break
				}
			}
			if !found {
				m.cursor = -1
			}
		}
		if m.cursor >= len(m.results) && len(m.results) > 0 {
			m.cursor = len(m.results) - 1
		}
		return m, tea.Batch(tea.Tick(stateInterval, func(t time.Time) tea.Msg { return tickMsg(t) }), func() tea.Msg { return waitForSignal() })
	case spinner.TickMsg:
		// Advance only the spinner animation; never re-run detection here.
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	case tea.WindowSizeMsg:
		m.width = msg.Width
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "j", "down", "ctrl+n":
			if m.cursor < len(m.results)-1 {
				m.cursor++
			}
		case "k", "up", "ctrl+p":
			if m.cursor > 0 {
				m.cursor--
			}
		case "enter":
			if len(m.results) > 0 && m.cursor < len(m.results) {
				m.srv.FocusPane(m.results[m.cursor].PaneID)
			}
		}
	}
	return m, nil
}

var (
	titleStyle = lipgloss.NewStyle().Bold(true)
	lineStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	selStyle   = lipgloss.NewStyle().Reverse(true)
	stateStyle = map[detect.State]lipgloss.Style{
		detect.Idle:    lipgloss.NewStyle().Foreground(lipgloss.Color("2")),
		detect.Working: lipgloss.NewStyle().Foreground(lipgloss.Color("3")),
		detect.Blocked: lipgloss.NewStyle().Foreground(lipgloss.Color("1")),
		detect.Unknown: lipgloss.NewStyle().Foreground(lipgloss.Color("8")),
	}
)

func statusCircle(s detect.State, spinView string) string {
	st := stateStyle[s]
	switch s {
	case detect.Working:
		// Animated braille spinner (same family pi uses) for active work.
		return st.Render(strings.TrimRight(spinView, " "))
	case detect.Blocked:
		// The agent is waiting on the user.
		return st.Render("?")
	case detect.Idle, detect.Unknown:
		// Stale / idle / unknown: a quiet empty circle.
		return st.Render("○")
	default:
		return st.Render("○")
	}
}

func abbreviatePath(pth string) string {
	if home, _ := os.UserHomeDir(); home != "" {
		pth = strings.Replace(pth, home, "~", 1)
	}
	parts := strings.Split(pth, "/")
	if len(parts) <= 2 {
		return pth
	}
	for i := 1; i < len(parts)-1; i++ {
		if parts[i] != "" && parts[i] != "~" {
			parts[i] = string([]rune(parts[i])[:1])
		}
	}
	return strings.Join(parts, "/")
}

func (m model) View() string {
	line := "─"
	if m.width > 0 {
		line = strings.Repeat("─", m.width)
	}
	s := titleStyle.Render(" tmuxrd") + "\n" + lineStyle.Render(line) + "\n"
	// group agent results by session preserving order
	sessions := []string{}
	groups := map[string][]int{}
	for i, r := range m.results {
		if _, ok := groups[r.Session]; !ok {
			sessions = append(sessions, r.Session)
		}
		groups[r.Session] = append(groups[r.Session], i)
	}
	idx := 0 // visual row index for cursor mapping among agents (flat)
	// We map cursor to flat result index m.cursor, so build a flat pointer list
	flat := make([]int, len(m.results))
	for i := range m.results {
		flat[i] = i
	}
	_ = idx
	_ = flat
	for _, sess := range sessions {
		s += lipgloss.NewStyle().Bold(true).Render(" "+sess) + "\n"
		for _, gi := range groups[sess] {
			r := m.results[gi]
			pth := r.Path
			if pth != "" {
				parts := strings.Split(strings.TrimRight(pth, "/"), "/")
				pth = parts[len(parts)-1]
				if pth == "" { pth = "/" }
			}
			circle := statusCircle(r.State, m.spin.View())
			mark := " "
			if m.cursor >= 0 && gi == m.cursor {
				mark = ">"
			}
			label := r.Agent
			if r.WindowName != "" && r.WindowName != r.Agent {
				label = r.WindowName + " (" + r.Agent + ")"
			}
			left := fmt.Sprintf("%s  %s  %s: %-12s", mark, circle, r.Window, label)
			line := left + " " + pth
			if m.width > 0 {
				// Measure *visible* width (ignore ANSI colour codes) so the styled
				// glyph doesn't skew the right-hand path alignment.
				lw := ansi.StringWidth(left)
				pw := ansi.StringWidth(pth)
				if lw+1+pw <= m.width {
					line = left + strings.Repeat(" ", m.width-lw-pw) + pth
				}
			}
			s += line + "\n"
		}
	}
	return s
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: tmuxrd run|summary")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "summary":
		srv := tmux.NewServerFromEnv()
		results := detect.Detect(srv)
		c := map[detect.State]int{}
		for _, r := range results {
			c[r.State]++
		}
		fmt.Printf("agents=%d idle=%d working=%d blocked=%d\n", len(results), c[detect.Idle], c[detect.Working], c[detect.Blocked])
	case "daemon":
		cmdDaemon()
	case "notify":
		cmdNotify()
	case "client", "run":
		runClient()
	default:
		fmt.Println("unknown command")
		os.Exit(2)
	}
}
