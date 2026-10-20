package sessions

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type ledgerFile struct {
	Version int      `json:"version"`
	Agents  []Record `json:"agents"`
}

// Load reads the ledger. A missing file is an empty ledger.
func Load(path string) ([]Record, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var f ledgerFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, err
	}
	return f.Agents, nil
}

// Save writes the ledger atomically. The daemon calls it only when the set
// of records changed, so a quiet server does not rewrite the file on every tick.
func Save(path string, rows []Record) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	body, err := json.MarshalIndent(ledgerFile{Version: 1, Agents: rows}, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// place is one agent pane observed on this refresh.
type place struct {
	PaneID     string
	Agent      string
	Session    string
	Window     string
	WindowName string
	Path       string
}

func (p place) key() string {
	return p.PaneID + "\x00" + p.Agent
}

// merge replaces this socket's rows with what the refresh could identify.
// A pane that is still there but whose session id could not be read keeps its
// previous row, so a grok registry that fails to parse, or a pi that has not
// written its file yet, does not erase a conversation we already know. A pane
// that is gone is dropped: the user closed it, and a later server death must
// not bring it back.
func merge(prev []Record, sock Socket, places []place, identified []Record, uncertain map[string]bool) []Record {
	prevHere := map[string]Record{}
	var keep []Record
	for _, r := range prev {
		if r.socket().key() != sock.key() {
			keep = append(keep, r)
			continue
		}
		prevHere[r.PaneID+"\x00"+r.Agent] = r
	}
	got := map[string]Record{}
	for _, r := range identified {
		got[r.PaneID+"\x00"+r.Agent] = r
	}
	for _, p := range places {
		k := p.key()
		if r, ok := got[k]; ok {
			keep = append(keep, r)
			continue
		}
		if uncertain[k] {
			if r, ok := prevHere[k]; ok {
				keep = append(keep, r)
			}
		}
	}
	sort.Slice(keep, func(i, j int) bool {
		a, b := keep[i], keep[j]
		if a.SocketKind != b.SocketKind {
			return a.SocketKind < b.SocketKind
		}
		if a.SocketValue != b.SocketValue {
			return a.SocketValue < b.SocketValue
		}
		if a.Session != b.Session {
			return a.Session < b.Session
		}
		if a.Window != b.Window {
			return a.Window < b.Window
		}
		if a.Agent != b.Agent {
			return a.Agent < b.Agent
		}
		return a.PaneID < b.PaneID
	})
	return keep
}

func sameRecords(a, b []Record) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !sameRecord(a[i], b[i]) {
			return false
		}
	}
	return true
}

func sameRecord(a, b Record) bool {
	a.Seen, b.Seen = time.Time{}, time.Time{}
	return a == b
}
