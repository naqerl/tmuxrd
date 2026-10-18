// Package layout keeps a tmuxrd sidebar the right shape for the window it
// lives in.
//
// A tmux window's layout is shared by every attached client, so a phone and a
// desktop cannot each have their own. What we can do — and what window-size
// "latest" (the default) already gives us — is let the window take the size of
// whichever client last acted. This package reacts to that: it reads each
// window's width and rebuilds its sidebar as a 32-column left pane (desktop) or
// a 2-row top strip (a phone, where columns are scarce and rows are not).
//
// The pass is idempotent: a window whose sidebar already matches its mode is
// left completely alone, including any manual resize the user made.
package layout

import (
	"os"
	"strconv"
	"strings"

	"github.com/naqerl/tmuxrd/internal/tmux"
)

// The two sidebar shapes.
const (
	Side  = "side"  // a fixed-width column on the left
	Strip = "strip" // a full-width strip above the agent pane
)

// Defaults for the tunables. They can be overridden with tmux user options:
//
//	@tmuxrd_mobile_width  60  window columns below which the strip is used
//	@tmuxrd_strip_height   2  rows the strip takes
//	@tmuxrd_side_width    32  columns the desktop sidebar takes
const (
	DefaultMobileWidth = 60
	DefaultStripHeight = 2
	DefaultSideWidth   = 32
)

// Options are the layout tunables.
type Options struct {
	MobileWidth int
	StripHeight int
	SideWidth   int
}

func (o Options) withDefaults() Options {
	if o.MobileWidth <= 0 {
		o.MobileWidth = DefaultMobileWidth
	}
	if o.StripHeight <= 0 {
		o.StripHeight = DefaultStripHeight
	}
	if o.SideWidth <= 0 {
		o.SideWidth = DefaultSideWidth
	}
	return o
}

// OptionsFromServer reads the tunables from tmux user options, falling back to
// the defaults for anything unset.
func OptionsFromServer(srv tmux.Server) Options {
	return Options{
		MobileWidth: intOption(srv, "@tmuxrd_mobile_width", DefaultMobileWidth),
		StripHeight: intOption(srv, "@tmuxrd_strip_height", DefaultStripHeight),
		SideWidth:   intOption(srv, "@tmuxrd_side_width", DefaultSideWidth),
	}
}

func intOption(srv tmux.Server, name string, def int) int {
	if n, err := strconv.Atoi(srv.ShowOption(name)); err == nil && n > 0 {
		return n
	}
	return def
}

// Mode is the sidebar shape a window this many columns wide should use.
func Mode(windowWidth int, opts Options) string {
	if windowWidth < opts.withDefaults().MobileWidth {
		return Strip
	}
	return Side
}

// Ensure makes every enabled window hold exactly one tmuxrd sidebar of the
// shape its width calls for.
//
// create=false (the daemon path) only reshapes sidebars that already exist: the
// daemon must never create a pane before the config has installed its hooks, or
// scenarios (and users) that never loaded tmuxrd would grow sidebars anyway.
// create=true (the toggle/ensure script path) also creates a missing one.
//
// It is a thin wrapper over EnsurePanes for callers that hold no pane list; it
// lists the server once and hands the panes over.
func Ensure(srv tmux.Server, opts Options, create bool) error {
	panes, err := srv.ListPanes()
	if err != nil {
		return err
	}
	return EnsurePanes(srv, panes, opts, create)
}

// EnsurePanes is Ensure over an already-fetched pane list. Callers that also
// need the pane list (the daemon refresh feeds both this pass and detection)
// fetch it once and pass it here, so a refresh lists the server once rather
// than once per phase.
//
// The pass also self-heals duplicate sidebars: if a window holds more than one
// pane tagged @tmuxrd_sidebar=1, the first in tmux order is kept and the rest
// are killed. That runs on every pass, including the daemon's create=false one,
// so a server left with duplicates by an older build (or any future race) heals
// within one tick and the sidebar count cannot grow again.
func EnsurePanes(srv tmux.Server, panes []tmux.Pane, opts Options, create bool) error {
	opts = opts.withDefaults()
	if srv.ShowOption("@tmuxrd_enabled") == "0" {
		return nil
	}

	type window struct {
		target  string
		width   int
		height  int
		zoomed  bool
		panes   int
		sidebar *tmux.Pane
	}
	windows := map[string]*window{}
	var order []string
	for i := range panes {
		p := &panes[i]
		if p.Session == "" {
			continue
		}
		key := p.Session + "\x00" + p.Window
		w := windows[key]
		if w == nil {
			w = &window{
				target: p.Session + ":" + p.Window,
				width:  p.WindowWidth,
				height: p.WindowHeight,
				zoomed: p.WindowZoomed,
			}
			windows[key] = w
			order = append(order, key)
		}
		w.panes++
		if p.Sidebar {
			if w.sidebar == nil {
				// First in tmux order wins: the panes arrive in pane-index order,
				// so this is the oldest sidebar (lowest pane id) — a deterministic
				// choice that leaves the survivor stable across passes.
				w.sidebar = p
			} else {
				// A duplicate sidebar: a creation race (or an older build) left
				// more than one. Reap it now, on every pass, not just when we would
				// create; that is what bounds an already-poisoned server.
				srv.KillPane(p.ID)
			}
		}
	}


	for _, key := range order {
		w := windows[key]
		mode := Mode(w.width, opts)

		// tmux ignores splits while a pane is zoomed, so acting now would just
		// fail. The next pass, once unzoomed, handles it.
		if w.zoomed {
			continue
		}
		// A strip needs its own rows, the pane border, and at least one row of
		// agent below it; below that tmux would squeeze the strip to 1 row.
		if mode == Strip && w.height < opts.StripHeight+2 {
			continue
		}

		if w.sidebar == nil {
			if !create {
				continue
			}
			// No last-instant re-check here: the create path serialises on an
			// advisory lock taken before the pane list is fetched (see cmdAdapt),
			// so a second caller re-reads a list that already contains the first
			// caller's sidebar. That is what makes concurrent hook invocations
			// idempotent without a check-then-act window.
			createSidebar(srv, w.target, mode, opts)
			continue
		}

		// A window whose only pane is the sidebar is an orphan: its agent pane is
		// gone and cleanup_orphans.sh is about to reap it. Reshaping it here (the
		// expanded orphan infers as a strip) would keep a sidebar alive in a
		// window that should lose it, so leave it alone.
		if w.sidebar != nil && w.panes <= 1 {
			continue
		}

		// The recorded shape is authoritative; for a sidebar created before
		// this option existed, infer it from the geometry.
		current := w.sidebar.Layout
		if current != Side && current != Strip {
			current = inferMode(*w.sidebar, opts)
		}
		if current == mode {
			// Right shape, but a window resize squeezes the smallest pane first:
			// tmux will shrink a 2-row strip to 1 row (hiding the detail line)
			// and will also grow it when the window gets taller. The strip's
			// two-line UI only works at exactly @tmuxrd_strip_height, so restore
			// it. A side pane's width is deliberately left alone, so a manual
			// resize survives.
			if mode == Strip && w.sidebar.Height != opts.StripHeight {
				srv.ResizePane(w.sidebar.ID, "v", opts.StripHeight)
			}
			continue
		}
		srv.KillPane(w.sidebar.ID)
		createSidebar(srv, w.target, mode, opts)
	}
	return nil
}

// createSidebar splits a sidebar into target in the given shape and tags both
// the pane (@tmuxrd_sidebar) and the window (@tmuxrd_layout).
func createSidebar(srv tmux.Server, target, mode string, opts Options) {
	axis, size := "h", opts.SideWidth
	if mode == Strip {
		axis, size = "v", opts.StripHeight
	}
	// Capture the pane that is active in target BEFORE the split, so the
	// sidebar can restore focus to it on Esc/C-g. Inside the new sidebar
	// pane, tmux resolves the "current" pane to the sidebar itself.
	origin, _ := srv.Run("display-message", "-t", target, "-p", "#{pane_id}")
	origin = strings.TrimSpace(origin)
	cmd := clientCommand()
	if origin != "" {
		cmd = "env TMUXRD_ORIGIN_PANE=" + origin + " " + cmd
	}
	pid, err := srv.SplitPane(target, axis, size, cmd)
	if err != nil || pid == "" {
		return
	}
	srv.SetPaneOption(pid, "@tmuxrd_sidebar", "1")
	srv.SetWindowOption(target, "@tmuxrd_layout", mode)
}

// inferMode guesses the shape of a sidebar that carries no @tmuxrd_layout: a
// strip spans the full window width and is only a few rows tall; anything else
// is a side pane.
func inferMode(p tmux.Pane, opts Options) string {
	if p.Width >= p.WindowWidth {
		return Strip
	}
	if p.Height <= opts.StripHeight {
		return Strip
	}
	return Side
}

// clientCommand is the command a new sidebar pane runs: this same binary, in
// client mode.
func clientCommand() string {
	exe, err := os.Executable()
	if err != nil || exe == "" {
		exe = "tmuxrd"
	}
	return exe + " client"
}
