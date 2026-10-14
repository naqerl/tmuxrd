package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/naqerl/tmuxrd/internal/detect"
	"github.com/naqerl/tmuxrd/internal/tmux"
)

type tickMsg time.Time

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
	height  int
	// strip is true when the pane is a short full-width strip (a phone) rather
	// than the tall left sidebar (a desktop). force pins the choice (--strip /
	// --side, or $TMUXRD_LAYOUT) for tests and debugging.
	strip bool
	force string
	spin  spinner.Model
}

// newModel builds a client model with the working spinner ready to animate.
func newModel(srv tmux.Server) model {
	s := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	return model{srv: srv, spin: s, force: layoutOverride()}
}

// layoutOverride reads $TMUXRD_LAYOUT ("strip" or "side"), or "" to let the
// pane's own height decide.
func layoutOverride() string {
	switch strings.ToLower(os.Getenv("TMUXRD_LAYOUT")) {
	case "strip":
		return "strip"
	case "side":
		return "side"
	}
	return ""
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

// goTo selects row i and switches the view to that agent's window, leaving the
// keyboard on a sidebar so navigation can continue (j/k, Tab, the wheel).
func (m *model) goTo(i int) { m.moveTo(i, false) }

// jumpTo selects row i, switches the view, and puts the keyboard in the agent's
// pane. That is what a tap or a number key should do: on a touch client the
// point of selecting is to land in the agent ready to type.
func (m *model) jumpTo(i int) { m.moveTo(i, true) }

func (m *model) moveTo(i int, focusAgent bool) {
	if i < 0 || i >= len(m.results) {
		return
	}
	m.cursor = i
	if focusAgent {
		m.srv.FocusPane(m.results[i].PaneID)
		return
	}
	m.srv.GoToAgent(m.results[i].PaneID)
}

// handleMouse turns a tap or wheel event into a selection. This is what makes
// the sidebar usable on a touch client: `1`-`9` still work, but a tap on a row
// (desktop) or a chip (strip) is the natural gesture on a phone.
func (m *model) handleMouse(e tea.MouseEvent) {
	switch e.Button {
	case tea.MouseButtonWheelUp:
		m.goTo(m.cursor - 1)
		return
	case tea.MouseButtonWheelDown:
		m.goTo(m.cursor + 1)
		return
	}
	// A tap arrives as a left press; ignore release, motion and other buttons.
	if e.Button != tea.MouseButtonLeft || e.Action != tea.MouseActionPress {
		return
	}
	if m.strip {
		if i, ok := m.stripChipAt(e.X); ok {
			m.jumpTo(i)
		}
		return
	}
	if i, ok := m.sideRowAt(e.Y); ok {
		m.jumpTo(i)
	}
}

// sideRowAt maps a rendered sidebar row to the flat agent index drawn on it,
// using the same session grouping as sideView. Rows 0 and 1 are the title and
// the rule; each session then contributes a header row before its agents.
func (m model) sideRowAt(y int) (int, bool) {
	if y < 2 {
		return 0, false
	}
	row := 2
	sessions, groups := m.sessionGroups()
	for _, sess := range sessions {
		row++ // session header
		for _, gi := range groups[sess] {
			if y == row {
				return gi, true
			}
			row++
		}
	}
	return 0, false
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
		// Local fallback only: the daemon pushes state every 500ms, so this
		// 5s re-detect matters only when the connection is briefly down.
		return m, tea.Tick(stateInterval, func(t time.Time) tea.Msg { return tickMsg(t) })
	case spinner.TickMsg:
		// Advance only the spinner animation; never re-run detection here.
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		switch m.force {
		case "strip":
			m.strip = true
		case "side":
			m.strip = false
		default:
			// A sidebar pane created with `-l 2` is exactly two rows tall, which
			// is the one unambiguous signal that we are a mobile strip.
			m.strip = m.height > 0 && m.height <= 2
		}
	case tea.MouseMsg:
		m.handleMouse(tea.MouseEvent(msg))
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "j", "down", "ctrl+n", "tab", "l", "right":
			m.goTo(m.cursor + 1)
		case "k", "up", "ctrl+p", "shift+tab", "h", "left":
			m.goTo(m.cursor - 1)
		case "enter":
			if m.cursor >= 0 && m.cursor < len(m.results) {
				m.srv.FocusPane(m.results[m.cursor].PaneID)
			}
		default:
			// 1-9 jump straight to the Nth agent and switch the view to it, so a
			// phone (no arrow keys) can drive the sidebar with one tap each.
			if k := msg.String(); len(k) == 1 && k[0] >= '1' && k[0] <= '9' {
				if n := int(k[0] - '0'); n <= len(m.results) {
					m.jumpTo(n - 1)
				}
			}
		}
	}
	return m, nil
}

var (
	titleStyle = lipgloss.NewStyle().Bold(true)
	lineStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	stateStyle = map[detect.State]lipgloss.Style{
		detect.Idle:    lipgloss.NewStyle().Foreground(lipgloss.Color("2")),
		detect.Working: lipgloss.NewStyle().Foreground(lipgloss.Color("3")),
		detect.Blocked: lipgloss.NewStyle().Foreground(lipgloss.Color("1")),
		detect.Unknown: lipgloss.NewStyle().Foreground(lipgloss.Color("8")),
	}
)

// The mobile strip is its own visual surface: a full-width bar with a
// background of its own, and one padded "tab" per agent. A tab's accent
// background is the mobile selection marker — the desktop's ">" is too small
// to pick out on a phone.
var (
	stripBarStyle = lipgloss.NewStyle().Background(lipgloss.Color("236")).Foreground(lipgloss.Color("250"))
	tabStyle      = lipgloss.NewStyle().Background(lipgloss.Color("239")).Foreground(lipgloss.Color("253"))
	tabSelStyle   = lipgloss.NewStyle().Background(lipgloss.Color("62")).Foreground(lipgloss.Color("231")).Bold(true)
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

// stateGlyph is the raw glyph for a state, with no colour of its own, so a tab
// can colour it against the tab's background.
func stateGlyph(s detect.State, spinView string) string {
	switch s {
	case detect.Working:
		return strings.TrimRight(spinView, " ")
	case detect.Blocked:
		return "?"
	default:
		return "○"
	}
}

// stateColor is the glyph colour inside a tab, matching the desktop sidebar's
// colours for the same states.
func stateColor(s detect.State) lipgloss.TerminalColor {
	switch s {
	case detect.Working:
		return lipgloss.Color("3")
	case detect.Blocked:
		return lipgloss.Color("1")
	case detect.Idle:
		return lipgloss.Color("2")
	default:
		return lipgloss.Color("8")
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

// View renders the sidebar in whichever shape this pane is: the tall left
// column, or the short full-width mobile strip.
func (m model) View() string {
	if m.strip {
		return m.stripView()
	}
	return m.sideView()
}

// sessionGroups returns the sessions in display order and, per session, the
// flat result indices belonging to it. sideView draws from it and sideRowAt
// hit-tests against it, so what is drawn and what a click hits cannot drift.
func (m model) sessionGroups() ([]string, map[string][]int) {
	sessions := []string{}
	groups := map[string][]int{}
	for i, r := range m.results {
		if _, ok := groups[r.Session]; !ok {
			sessions = append(sessions, r.Session)
		}
		groups[r.Session] = append(groups[r.Session], i)
	}
	return sessions, groups
}

// sideView is the desktop sidebar: a title, a rule, then one row per agent.
func (m model) sideView() string {
	line := "─"
	if m.width > 0 {
		line = strings.Repeat("─", m.width)
	}
	s := titleStyle.Render("tmuxrd") + "\n" + lineStyle.Render(line) + "\n"
	sessions, groups := m.sessionGroups()
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

// stripBarPad pads a strip line to the pane width with the bar's background,
// so every row reads as one continuous bar rather than floating text.
func stripBarPad(s string, width int) string {
	if width <= 0 {
		return s
	}
	if ansi.StringWidth(s) > width {
		s = ansi.Truncate(s, width, "…")
	}
	if w := ansi.StringWidth(s); w < width {
		s += stripBarStyle.Render(strings.Repeat(" ", width-w))
	}
	return s
}

// renderTab draws one agent as a tab: padded on both sides, with the selected
// one on the accent background. The glyph keeps its state colour, which is why
// the tab is assembled from separately rendered segments — nesting a styled
// glyph inside a styled tab would let the glyph's reset cancel the tab's
// background.
func (m model) renderTab(i int) string {
	r := m.results[i]
	tab := tabStyle
	bg := lipgloss.TerminalColor(lipgloss.Color("239"))
	if i == m.cursor {
		tab = tabSelStyle
		bg = lipgloss.Color("62")
	}
	glyph := lipgloss.NewStyle().Background(bg).Foreground(stateColor(r.State))
	return tab.Render(" ") + glyph.Render(stateGlyph(r.State, m.spin.View())) + tab.Render(" "+r.Agent+" ")
}

// tabWidth is the visible width renderTab occupies.
func (m model) tabWidth(i int) int {
	r := m.results[i]
	return 3 + ansi.StringWidth(stateGlyph(r.State, m.spin.View())) + ansi.StringWidth(r.Agent)
}

// stripView renders the mobile strip: a full-width row of agent tabs, and a
// second row describing the selected agent. It emits exactly m.height lines
// (never more — a 2-row pane that scrolls destroys its own content), each a
// full-width bar.
func (m model) stripView() string {
	chips, _ := m.stripChipLayout()
	lines := []string{chips}
	if m.height >= 2 {
		lines = append(lines, stripBarPad(stripBarStyle.Render(m.stripDetail()), m.width))
	}
	h := m.height
	if h <= 0 {
		h = len(lines)
	}
	for len(lines) < h {
		lines = append(lines, stripBarPad("", m.width))
	}
	if len(lines) > h {
		lines = lines[:h]
	}
	return strings.Join(lines, "\n")
}

// chipSpan is the visible column range [start,end) of one agent tab in the
// strip's first row, so a tap can be mapped back to an agent.
type chipSpan struct {
	index      int
	start, end int
}

// stripChips is the first strip row.
func (m model) stripChips() string {
	s, _ := m.stripChipLayout()
	return s
}

// stripChipLayout builds the tab row and, alongside it, the column span of
// every visible tab. Both come from one layout pass, so what is drawn and what a
// tap hits can never disagree. Tabs sit flush against each other (each carries
// its own padding), which is what makes the row read as a tab bar.
func (m model) stripChipLayout() (string, []chipSpan) {
	if len(m.results) == 0 {
		return stripBarPad(stripBarStyle.Render(" no agents"), m.width), nil
	}

	// Fit a contiguous run of tabs around the selection. Reserve two columns for
	// the ‹ › markers that signal clipped tabs.
	budget := m.width
	if budget > 4 {
		budget -= 2
	}
	sel := m.cursor
	if sel < 0 {
		sel = 0
	}
	if sel >= len(m.results) {
		sel = len(m.results) - 1
	}
	used := m.tabWidth(sel)
	lo, hi := sel, sel
	for budget > 0 && lo > 0 && used+m.tabWidth(lo-1) <= budget {
		used += m.tabWidth(lo - 1)
		lo--
	}
	for budget > 0 && hi < len(m.results)-1 && used+m.tabWidth(hi+1) <= budget {
		used += m.tabWidth(hi + 1)
		hi++
	}

	var b strings.Builder
	spans := make([]chipSpan, 0, hi-lo+1)
	x := 0
	if lo > 0 {
		b.WriteString(stripBarStyle.Render("‹ "))
		x += 2
	}
	for i := lo; i <= hi; i++ {
		start := x
		b.WriteString(m.renderTab(i))
		x += m.tabWidth(i)
		spans = append(spans, chipSpan{index: i, start: start, end: x})
	}
	if hi < len(m.results)-1 {
		b.WriteString(stripBarStyle.Render(" ›"))
		x += 2
	}
	out := b.String()
	if m.width > 0 && x < m.width {
		out += stripBarStyle.Render(strings.Repeat(" ", m.width-x))
	}
	return out, spans
}

// stripChipAt returns the agent whose tab covers column x of the first strip
// row.
func (m model) stripChipAt(x int) (int, bool) {
	_, spans := m.stripChipLayout()
	for _, s := range spans {
		if x >= s.start && x < s.end {
			return s.index, true
		}
	}
	return 0, false
}

// stripDetail is the second strip row: the selected agent's session, path,
// state and timer — or a one-line census when no agent is selected.
func (m model) stripDetail() string {
	if m.cursor < 0 || m.cursor >= len(m.results) {
		counts := map[detect.State]int{}
		for _, r := range m.results {
			counts[r.State]++
		}
		parts := []string{fmt.Sprintf("%d agents", len(m.results))}
		if n := counts[detect.Working]; n > 0 {
			parts = append(parts, fmt.Sprintf("⠴%d", n))
		}
		if n := counts[detect.Blocked]; n > 0 {
			parts = append(parts, fmt.Sprintf("?%d", n))
		}
		if n := counts[detect.Idle]; n > 0 {
			parts = append(parts, fmt.Sprintf("○%d", n))
		}
		return strings.Join(parts, "  ")
	}
	r := m.results[m.cursor]
	name := r.Agent
	if r.WindowName != "" && r.WindowName != r.Agent {
		name = r.WindowName + " (" + r.Agent + ")"
	}
	parts := []string{name, r.Session + ":" + r.Window}
	if p := baseName(r.Path); p != "" {
		parts = append(parts, p)
	}
	parts = append(parts, string(r.State))
	if t := workedLabel(r); t != "" {
		parts = append(parts, t)
	}
	return strings.Join(parts, " · ")
}

// baseName renders a path the compact way the strip wants: the last element
// with a trailing slash, so directories stay recognisable.
func baseName(p string) string {
	p = strings.TrimRight(p, "/")
	if p == "" {
		return ""
	}
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:] + "/"
	}
	return p + "/"
}

// version is stamped at build time: -ldflags "-X main.version=v1.2.3".
var version = "dev"

func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: tmuxrd client|daemon|notify|summary|adapt|stop|version")
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
	case "adapt":
		cmdAdapt()
	case "stop", "quit":
		cmdStop()
	case "notify":
		cmdNotify()
	case "client", "run":
		runClient()
	default:
		fmt.Println("unknown command")
		os.Exit(2)
	}
}
