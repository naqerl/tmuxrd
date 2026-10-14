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

// syncMarker marks the agent whose window is currently in view, and clears the
// marker entirely when that window has no agent (a shell window, htop).
//
// Keying off the *window* rather than the pane is what makes every sidebar agree
// without shared state: focusing an agent's sidebar still shows that agent
// marked, while a non-agent window shows no marker at all.
func (m *model) syncMarker() {
	_, sess, win := m.srv.CurrentTarget()
	m.cursor = -1
	if sess == "" {
		return
	}
	for i, r := range m.results {
		if r.Session == sess && r.Window == win {
			m.cursor = i
			return
		}
	}
}

// goTo selects row i and switches the view to that agent's window and session,
// leaving the keyboard on a sidebar so navigation can continue. Enter
// (FocusPane) is what moves the keyboard into the agent pane itself.
func (m *model) goTo(i int) {
	if i < 0 || i >= len(m.results) {
		return
	}
	m.cursor = i
	m.srv.GoToAgent(m.results[i].PaneID)
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
		m.syncMarker()
	case tickMsg:
		// Keep the daemon's accumulated work time: this local fallback cannot
		// know it, and losing it would make the timer flicker every 5s.
		prev := make(map[string]time.Duration, len(m.results))
		for _, r := range m.results {
			prev[r.PaneID] = r.Worked
		}
		m.results = detect.Detect(m.srv)
		for i := range m.results {
			if d, ok := prev[m.results[i].PaneID]; ok && d > m.results[i].Worked {
				m.results[i].Worked = d
			}
		}
		m.syncMarker()
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
			m.goTo(m.cursor + 1)
		case "k", "up", "ctrl+p":
			m.goTo(m.cursor - 1)
		case "enter":
			if m.cursor >= 0 && m.cursor < len(m.results) {
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

// workedLabel renders how long the agent has been working on this run, e.g.
// "0s", "45s", "1m", "1m30s". The timer is reset each time the agent starts
// working again, so it reads the current (or last) working period, not a total.
// It is empty for an agent that has never worked, so the column costs nothing
// until it is useful.
func workedLabel(r detect.Result) string {
	if r.Worked == 0 && r.State != detect.Working {
		return ""
	}
	return formatDuration(r.Worked)
}

// formatDuration renders a duration the way the sidebar shows it: 0s, 45s, 1m,
// 1m30s, 12m5s.
func formatDuration(d time.Duration) string {
	s := int(d.Seconds())
	if s < 0 {
		s = 0
	}
	if s < 60 {
		return fmt.Sprintf("%ds", s)
	}
	m, rem := s/60, s%60
	if rem == 0 {
		return fmt.Sprintf("%dm", m)
	}
	return fmt.Sprintf("%dm%ds", m, rem)
}

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
	s := titleStyle.Render("tmuxrd") + "\n" + lineStyle.Render(line) + "\n"
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
		s += lipgloss.NewStyle().Bold(true).Render(sess) + "\n"
		for _, gi := range groups[sess] {
			r := m.results[gi]
			pth := r.Path
			if pth != "" {
				parts := strings.Split(strings.TrimRight(pth, "/"), "/")
				pth = parts[len(parts)-1]
				if pth == "" {
					pth = "/"
				}
			}
			// Show a directory as "name/ ": the slash marks it as a directory,
			// and the trailing space keeps it off the pane divider.
			if pth != "" {
				if strings.HasSuffix(pth, "/") {
					pth += " "
				} else {
					pth += "/ "
				}
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
			left := fmt.Sprintf("%s %s %s:%-12s", mark, circle, r.Window, label)
			if t := workedLabel(r); t != "" {
				left += " " + t
			}
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

// version is stamped at build time: -ldflags "-X main.version=v1.2.3".
var version = "dev"

func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: tmuxrd client|daemon|notify|summary|version")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "version", "--version", "-v":
		fmt.Println(version)
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
