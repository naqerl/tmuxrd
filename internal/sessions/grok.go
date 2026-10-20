package sessions

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type grokRow struct {
	SessionID string `json:"session_id"`
	PID       int    `json:"pid"`
	PIDStart  int64  `json:"pid_start"`
	Cwd       string `json:"cwd"`
	OpenedAt  string `json:"opened_at"`
}

// readGrok lists the tabs one grok process still has open. A missing file is
// not an error: grok has not written the registry. A parse error is returned
// so the caller can keep the previous ledger row instead of forgetting it.
func readGrok(home string) ([]grokRow, error) {
	b, err := os.ReadFile(filepath.Join(home, "active_sessions.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var rows []grokRow
	if err := json.Unmarshal(b, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

// pickGrok chooses the tab on screen. Several rows share a pid, one per open
// tab. The pane title contains the focused tab's generated title (grok
// truncates it, so a prefix matches). When nothing matches, the newest
// opened_at is the tab grok opened last.
func pickGrok(rows []grokRow, pid int, start int64, paneTitle, home string) (grokRow, bool) {
	var mine []grokRow
	for _, r := range rows {
		if r.PID != pid || r.SessionID == "" {
			continue
		}
		if start != 0 && r.PIDStart != 0 && r.PIDStart != start {
			continue
		}
		mine = append(mine, r)
	}
	if len(mine) == 0 {
		return grokRow{}, false
	}
	bestLen := 0
	var hits []grokRow
	for _, r := range mine {
		n := titlePrefixLen(paneTitle, generatedTitle(home, r))
		if n > bestLen {
			bestLen = n
			hits = []grokRow{r}
		} else if n > 0 && n == bestLen {
			hits = append(hits, r)
		}
	}
	if len(hits) == 0 {
		hits = mine
	}
	best := hits[0]
	for _, r := range hits[1:] {
		if r.OpenedAt > best.OpenedAt {
			best = r
		}
	}
	return best, true
}

// titlePrefixLen is how much of the session title is visible in the pane
// title. Grok clips the OSC title from the right and inserts an ellipsis, so
// the full string often is not there. Titles shorter than 8 runes are ignored:
// they match too much else.
func titlePrefixLen(paneTitle, generated string) int {
	paneTitle = strings.TrimSpace(paneTitle)
	generated = strings.TrimSpace(generated)
	if paneTitle == "" || generated == "" {
		return 0
	}
	pr := []rune(paneTitle)
	gr := []rune(generated)
	n := len(gr)
	for n >= 8 {
		if strings.Contains(string(pr), string(gr[:n])) {
			return n
		}
		n--
	}
	return 0
}

func generatedTitle(home string, r grokRow) string {
	path := summaryPath(home, r.Cwd, r.SessionID)
	if path == "" {
		return ""
	}
	if _, err := os.Stat(path); err != nil {
		matches, _ := filepath.Glob(filepath.Join(home, "sessions", "*", r.SessionID, "summary.json"))
		if len(matches) == 0 {
			return ""
		}
		path = matches[0]
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var meta struct {
		Title string `json:"generated_title"`
	}
	if json.Unmarshal(b, &meta) != nil {
		return ""
	}
	return meta.Title
}

func summaryPath(home, cwd, id string) string {
	if home == "" || cwd == "" || !validID(id) {
		return ""
	}
	return filepath.Join(home, "sessions", url.QueryEscape(cwd), id, "summary.json")
}

func validID(id string) bool {
	return id != "" && !strings.ContainsAny(id, `/\`)
}
