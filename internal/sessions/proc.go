package sessions

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// FindAgent returns the pid and starttime of the agent process behind a tmux
// pane. panePID is tmux's pane_pid, which is often the shell that launched the
// agent. A foreground tool (the agent's own bash) must not hide the agent, so
// the walk prefers a process whose comm is the agent name: the pane pid itself,
// then its descendants, then the terminal's foreground group.
func FindAgent(proc string, panePID int, agent string) (pid int, start int64, ok bool) {
	if proc == "" {
		proc = "/proc"
	}
	if panePID <= 0 || agent == "" {
		return 0, 0, false
	}
	if pid, start, ok = agentAt(proc, panePID, agent); ok {
		return pid, start, true
	}
	seen := map[int]bool{panePID: true}
	queue := children(proc, panePID)
	for len(queue) > 0 && len(seen) < 64 {
		cur := queue[0]
		queue = queue[1:]
		if cur <= 0 || seen[cur] {
			continue
		}
		seen[cur] = true
		if pid, start, ok = agentAt(proc, cur, agent); ok {
			return pid, start, true
		}
		queue = append(queue, children(proc, cur)...)
	}
	if fg := foregroundPID(proc, panePID); fg > 0 && !seen[fg] {
		if pid, start, ok = agentAt(proc, fg, agent); ok {
			return pid, start, true
		}
	}
	return 0, 0, false
}

func agentAt(proc string, pid int, agent string) (int, int64, bool) {
	st, ok := readStat(proc, pid)
	if !ok || st.state == "Z" || st.state == "X" {
		return 0, 0, false
	}
	comm := st.comm
	if comm == "" {
		comm = readComm(proc, pid)
	}
	if !strings.EqualFold(comm, agent) {
		return 0, 0, false
	}
	return pid, st.start, true
}

// Alive reports whether pid is the same process starttime was captured from.
// A start of 0 matches any live process with that pid (the record was written
// where /proc could not be read). A missing /proc falls back to signal 0.
func Alive(proc string, pid int, start int64) bool {
	if pid <= 0 {
		return false
	}
	if proc == "" {
		proc = "/proc"
	}
	st, ok := readStat(proc, pid)
	if !ok {
		// /proc is mounted (we can see "self") and this pid is not in it.
		if _, err := os.Stat(filepath.Join(proc, "self")); err == nil {
			return false
		}
		err := syscall.Kill(pid, 0)
		return err == nil && start == 0
	}
	if start == 0 {
		return true
	}
	return st.start == start
}

type procStat struct {
	comm  string
	state string
	tpgid int
	start int64
}

// readStat parses /proc/<pid>/stat. comm is taken from inside the parentheses
// so a name with spaces does not shift the later fields. starttime is field 22
// (index 19 of the fields after the comm), the same clock-tick counter grok
// stores as pid_start. tpgid is field 8 (index 5).
func readStat(proc string, pid int) (procStat, bool) {
	b, err := os.ReadFile(filepath.Join(proc, strconv.Itoa(pid), "stat"))
	if err != nil {
		return procStat{}, false
	}
	s := string(b)
	open := strings.IndexByte(s, '(')
	close := strings.LastIndex(s, ")")
	if open < 0 || close < open+1 || close+2 >= len(s) {
		return procStat{}, false
	}
	fields := strings.Fields(s[close+2:])
	if len(fields) < 20 {
		return procStat{}, false
	}
	start, err := strconv.ParseInt(fields[19], 10, 64)
	if err != nil {
		return procStat{}, false
	}
	tpgid, _ := strconv.Atoi(fields[5])
	return procStat{comm: s[open+1 : close], state: fields[0], tpgid: tpgid, start: start}, true
}

func readComm(proc string, pid int) string {
	b, err := os.ReadFile(filepath.Join(proc, strconv.Itoa(pid), "comm"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func children(proc string, pid int) []int {
	id := strconv.Itoa(pid)
	b, err := os.ReadFile(filepath.Join(proc, id, "task", id, "children"))
	if err != nil {
		return nil
	}
	var out []int
	for _, f := range strings.Fields(string(b)) {
		n, err := strconv.Atoi(f)
		if err == nil && n > 0 {
			out = append(out, n)
		}
	}
	return out
}

func foregroundPID(proc string, pid int) int {
	st, ok := readStat(proc, pid)
	if !ok || st.tpgid <= 0 {
		return 0
	}
	return st.tpgid
}

func environValue(proc string, pid int, key string) string {
	b, err := os.ReadFile(filepath.Join(proc, strconv.Itoa(pid), "environ"))
	if err != nil {
		return ""
	}
	prefix := []byte(key + "=")
	for _, part := range bytes.Split(b, []byte{0}) {
		if bytes.HasPrefix(part, prefix) {
			return string(part[len(prefix):])
		}
	}
	return ""
}
