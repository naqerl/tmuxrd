package sessions

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
)

type piReport struct {
	SessionID   string `json:"session_id"`
	SessionFile string `json:"session_file"`
	Cwd         string `json:"cwd"`
	PID         int    `json:"pid"`
	PIDStart    int64  `json:"pid_start"`
}

// readPiReport reads the file the pi extension writes for this process.
// ok is false when the file is absent. A pid_start that does not match the
// live process is a recycled pid: the file is ignored and removed.
func readPiReport(stateDir string, pid int, start int64) (piReport, bool) {
	path := filepath.Join(stateDir, "pi", strconv.Itoa(pid)+".json")
	b, err := os.ReadFile(path)
	if err != nil {
		return piReport{}, false
	}
	var rep piReport
	if json.Unmarshal(b, &rep) != nil || rep.SessionID == "" {
		return piReport{}, false
	}
	if start != 0 && rep.PIDStart != 0 && rep.PIDStart != start {
		os.Remove(path)
		return piReport{}, false
	}
	if rep.PID == 0 {
		rep.PID = pid
	}
	return rep, true
}

type subMeta struct {
	ChildSessionID string `json:"childSessionId"`
	SessionFile    string `json:"sessionFile"`
	Cwd            string `json:"cwd"`
}

// readSubagent reads the metadata pi writes for a pane it launched as a
// subagent. PI_SUBAGENT_RUN_DIR is set on that process and nowhere else.
func readSubagent(proc string, pid int) (subMeta, bool) {
	dir := environValue(proc, pid, "PI_SUBAGENT_RUN_DIR")
	if dir == "" {
		return subMeta{}, false
	}
	b, err := os.ReadFile(filepath.Join(dir, "metadata.json"))
	if err != nil {
		return subMeta{}, false
	}
	var meta subMeta
	if json.Unmarshal(b, &meta) != nil || meta.ChildSessionID == "" {
		return subMeta{}, false
	}
	return meta, true
}
