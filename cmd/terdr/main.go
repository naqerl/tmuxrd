package main

import (
	"fmt"
	"strings"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/naqerl/terdr/internal/detect"
	"github.com/naqerl/terdr/internal/tmux"
)

type tickMsg time.Time

type model struct {
	results []detect.Result
	cursor  int
	width   int
}

func (m model) Init() tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tickMsg:
		m.results = detect.Detect()
		if m.cursor >= len(m.results) && len(m.results) > 0 {
			m.cursor = len(m.results) - 1
		}
		return m, tea.Tick(2*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
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
				tmux.FocusPane(m.results[m.cursor].PaneID)
			}
		}
	}
	return m, nil
}

var (
	titleStyle = lipgloss.NewStyle().Bold(true)
	lineStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	selStyle   = lipgloss.NewStyle().Background(lipgloss.Color("62")).Foreground(lipgloss.Color("15"))
	stateStyle = map[detect.State]lipgloss.Style{
		detect.Idle:    lipgloss.NewStyle().Foreground(lipgloss.Color("2")),
		detect.Working: lipgloss.NewStyle().Foreground(lipgloss.Color("3")),
		detect.Blocked: lipgloss.NewStyle().Foreground(lipgloss.Color("1")),
		detect.Unknown: lipgloss.NewStyle().Foreground(lipgloss.Color("8")),
	}
)

func glyph(s detect.State) string {
	switch s {
	case detect.Idle:
		return "✅"
	case detect.Working:
		return "⚙"
	case detect.Blocked:
		return "⛔"
	default:
		return "·"
	}
}

func (m model) View() string {
	line := "─"
	if m.width > 0 { line = strings.Repeat("─", m.width) }
	s := titleStyle.Render(" terdr") + "\n" + lineStyle.Render(line) + "\n"
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
			line := fmt.Sprintf("   %s %-7s %s  %s", glyph(r.State), r.Agent, r.State, r.Path)
			suff := ""
			if gi == m.cursor {
				s += selStyle.Render(line+suff) + "\n"
			} else {
				s += line + "\n"
			}
		}
	}
	return s
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: terdr run|summary")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "summary":
		results := detect.Detect()
		c := map[detect.State]int{}
		for _, r := range results {
			c[r.State]++
		}
		fmt.Printf("agents=%d idle=%d working=%d blocked=%d\n", len(results), c[detect.Idle], c[detect.Working], c[detect.Blocked])
	case "run":
		m := model{results: detect.Detect()}
		p := tea.NewProgram(m)
		if _, err := p.Run(); err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
	default:
		fmt.Println("unknown command")
		os.Exit(2)
	}
}