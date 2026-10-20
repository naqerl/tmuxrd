package sessions

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Cache holds the last ledger and the last parsed grok registry. The daemon
// keeps one and calls Sync from its single refresh goroutine.
type Cache struct {
	roots Roots
	prev  []Record

	grokMod  time.Time
	grokRows []grokRow
	grokErr  error
	grokRead bool
}

// NewCache loads whatever ledger is already on disk so the first refresh can
// keep rows it cannot yet identify.
func NewCache(roots Roots) *Cache {
	c := &Cache{roots: roots}
	if rows, err := Load(roots.LedgerPath()); err == nil {
		c.prev = rows
	}
	return c
}

// Sync joins the current agent panes to their conversation ids and rewrites
// the ledger when the set changes. roots.Proc, StateDir and GrokHome come
// from the cache.
func (c *Cache) Sync(sock Socket, panes []Pane, agents []Agent, now time.Time) {
	if c == nil {
		return
	}
	places, identified, uncertain := c.identify(sock, panes, agents, now)
	next := stampNew(c.prev, merge(c.prev, sock, places, identified, uncertain), now)
	if sameRecords(c.prev, next) {
		c.sweepPi()
		return
	}
	if err := Save(c.roots.LedgerPath(), next); err != nil {
		return
	}
	c.prev = next
	c.sweepPi()
}

func stampNew(prev, next []Record, now time.Time) []Record {
	old := map[string]time.Time{}
	for _, r := range prev {
		old[r.PaneID+"\x00"+r.Agent+"\x00"+r.SessionID] = r.Seen
	}
	out := append([]Record(nil), next...)
	for i := range out {
		k := out[i].PaneID + "\x00" + out[i].Agent + "\x00" + out[i].SessionID
		if t, ok := old[k]; ok && !t.IsZero() {
			out[i].Seen = t
			continue
		}
		out[i].Seen = now
	}
	return out
}

func (c *Cache) identify(sock Socket, panes []Pane, agents []Agent, now time.Time) ([]place, []Record, map[string]bool) {
	byID := map[string]Pane{}
	for _, p := range panes {
		byID[p.ID] = p
	}
	var places []place
	var identified []Record
	uncertain := map[string]bool{}
	for _, a := range agents {
		if a.Agent != "pi" && a.Agent != "grok" {
			continue
		}
		p, ok := byID[a.PaneID]
		if !ok || p.Sidebar {
			continue
		}
		pl := place{
			PaneID: a.PaneID, Agent: a.Agent,
			Session: a.Session, Window: a.Window, WindowName: a.WindowName, Path: a.Path,
		}
		places = append(places, pl)
		panePID, _ := strconv.Atoi(p.PID)
		pid, start, found := FindAgent(c.roots.proc(), panePID, a.Agent)
		if !found {
			uncertain[pl.key()] = true
			continue
		}
		switch a.Agent {
		case "grok":
			rec, ok, unsure := c.identifyGrok(sock, pl, p.Title, pid, start, now)
			if ok {
				identified = append(identified, rec)
			} else if unsure {
				uncertain[pl.key()] = true
			}
		case "pi":
			rec, ok, unsure := c.identifyPi(sock, pl, pid, start, now)
			if ok {
				identified = append(identified, rec)
			} else if unsure {
				uncertain[pl.key()] = true
			}
		}
	}
	return places, identified, uncertain
}

func (c *Cache) identifyGrok(sock Socket, pl place, title string, pid int, start int64, now time.Time) (Record, bool, bool) {
	rows, unsure := c.grok()
	if unsure {
		return Record{}, false, true
	}
	row, ok := pickGrok(rows, pid, start, title, c.roots.GrokHome)
	if !ok {
		return Record{}, false, false
	}
	cwd := row.Cwd
	if cwd == "" {
		cwd = pl.Path
	}
	return Record{
		Agent: "grok", SessionID: row.SessionID, Cwd: cwd,
		PID: pid, PIDStart: startOr(start, row.PIDStart),
		SocketKind: sock.Kind, SocketValue: sock.Value,
		Session: pl.Session, Window: pl.Window, WindowName: pl.WindowName,
		PaneID: pl.PaneID, Seen: now,
	}, true, false
}

func (c *Cache) identifyPi(sock Socket, pl place, pid int, start int64, now time.Time) (Record, bool, bool) {
	if rep, ok := readPiReport(c.roots.StateDir, pid, start); ok {
		cwd := rep.Cwd
		if cwd == "" {
			cwd = pl.Path
		}
		return Record{
			Agent: "pi", SessionID: rep.SessionID, SessionFile: rep.SessionFile, Cwd: cwd,
			PID: pid, PIDStart: startOr(start, rep.PIDStart),
			SocketKind: sock.Kind, SocketValue: sock.Value,
			Session: pl.Session, Window: pl.Window, WindowName: pl.WindowName,
			PaneID: pl.PaneID, Seen: now,
		}, true, false
	}
	if meta, ok := readSubagent(c.roots.proc(), pid); ok {
		cwd := meta.Cwd
		if cwd == "" {
			cwd = pl.Path
		}
		return Record{
			Agent: "pi", SessionID: meta.ChildSessionID, SessionFile: meta.SessionFile, Cwd: cwd,
			PID: pid, PIDStart: start,
			SocketKind: sock.Kind, SocketValue: sock.Value,
			Session: pl.Session, Window: pl.Window, WindowName: pl.WindowName,
			PaneID: pl.PaneID, Seen: now,
		}, true, false
	}
	// No file yet. The extension writes one on session_start; until then keep
	// whatever this pane already contributed.
	return Record{}, false, true
}

func startOr(live, recorded int64) int64 {
	if live != 0 {
		return live
	}
	return recorded
}

// grok returns the registry. unsure is true when the file could not be read,
// so a blip does not look like "this process has no tabs".
func (c *Cache) grok() ([]grokRow, bool) {
	path := filepath.Join(c.roots.GrokHome, "active_sessions.json")
	st, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			c.grokRows, c.grokErr, c.grokRead = nil, nil, true
			c.grokMod = time.Time{}
			return nil, true
		}
		return nil, true
	}
	if c.grokRead && c.grokMod.Equal(st.ModTime()) && c.grokErr == nil {
		return c.grokRows, false
	}
	rows, err := readGrok(c.roots.GrokHome)
	c.grokMod = st.ModTime()
	c.grokRead = true
	if err != nil {
		c.grokErr = err
		return nil, true
	}
	c.grokErr = nil
	c.grokRows = rows
	return rows, false
}

// sweepPi removes reports whose process is gone. A pi that is killed with the
// tmux server never runs session_shutdown, so the file outlives the process
// until the next daemon start — and a reused pid must not inherit it.
func (c *Cache) sweepPi() {
	dir := filepath.Join(c.roots.StateDir, "pi")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSuffix(name, ".json"))
		if err != nil {
			continue
		}
		path := filepath.Join(dir, name)
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var rep piReport
		if json.Unmarshal(b, &rep) != nil {
			os.Remove(path)
			continue
		}
		if !Alive(c.roots.proc(), pid, rep.PIDStart) {
			os.Remove(path)
		}
	}
}
