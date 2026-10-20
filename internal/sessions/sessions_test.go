package sessions

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func writeProc(t *testing.T, root string, pid int, comm, state string, tpgid int, start int64, kids []int) {
	t.Helper()
	id := strconv.Itoa(pid)
	dir := filepath.Join(root, id, "task", id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	fields := make([]string, 20)
	for i := range fields {
		fields[i] = "0"
	}
	fields[0] = state
	fields[5] = strconv.Itoa(tpgid)
	fields[19] = strconv.FormatInt(start, 10)
	stat := strconv.Itoa(pid) + " (" + comm + ") " + strings.Join(fields, " ") + "\n"
	if err := os.WriteFile(filepath.Join(root, id, "stat"), []byte(stat), 0o644); err != nil {
		t.Fatal(err)
	}
	var childText string
	for _, k := range kids {
		childText += strconv.Itoa(k) + " "
	}
	if err := os.WriteFile(filepath.Join(dir, "children"), []byte(childText), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFindAgent(t *testing.T) {
	root := t.TempDir()
	writeProc(t, root, 10, "bash", "S", 40, 1, []int{40})
	writeProc(t, root, 40, "grok", "S", 40, 100, nil)
	pid, start, ok := FindAgent(root, 10, "grok")
	if !ok || pid != 40 || start != 100 {
		t.Fatalf("child agent: pid=%d start=%d ok=%v", pid, start, ok)
	}

	writeProc(t, root, 7, "pi", "S", 7, 50, nil)
	pid, start, ok = FindAgent(root, 7, "pi")
	if !ok || pid != 7 || start != 50 {
		t.Fatalf("pane is the agent: pid=%d start=%d ok=%v", pid, start, ok)
	}

	writeProc(t, root, 8, "bash", "S", 9, 1, nil)
	writeProc(t, root, 9, "pi", "S", 9, 70, nil)
	pid, start, ok = FindAgent(root, 8, "pi")
	if !ok || pid != 9 || start != 70 {
		t.Fatalf("foreground fallback: pid=%d start=%d ok=%v", pid, start, ok)
	}

	if !Alive(root, 40, 100) {
		t.Fatal("matching starttime should be alive")
	}
	if Alive(root, 40, 101) {
		t.Fatal("reused pid must not match")
	}
}

func TestSelfAlive(t *testing.T) {
	if _, err := os.Stat("/proc/self/stat"); err != nil {
		t.Skip("no /proc")
	}
	st, ok := readStat("/proc", os.Getpid())
	if !ok || st.start == 0 {
		t.Fatal(st, ok)
	}
	if !Alive("/proc", os.Getpid(), st.start) {
		t.Fatal("this process should match its starttime")
	}
	if Alive("/proc", os.Getpid(), st.start+1) {
		t.Fatal("a different starttime is a different process")
	}
}

func TestPickGrok(t *testing.T) {
	home := t.TempDir()
	cwd := "/work/app"
	writeSummary(t, home, cwd, "old", "Older title that is not on screen")
	writeSummary(t, home, cwd, "new", "Track the agents across sessions")
	rows := []grokRow{
		{SessionID: "old", PID: 9, PIDStart: 100, Cwd: cwd, OpenedAt: "2026-10-20T01:00:00Z"},
		{SessionID: "new", PID: 9, PIDStart: 100, Cwd: cwd, OpenedAt: "2026-10-20T02:00:00Z"},
		{SessionID: "other", PID: 3, PIDStart: 100, Cwd: cwd, OpenedAt: "2026-10-20T03:00:00Z"},
		{SessionID: "reused", PID: 9, PIDStart: 1, Cwd: cwd, OpenedAt: "2026-10-20T04:00:00Z"},
	}
	got, ok := pickGrok(rows, 9, 100, "⠼ Track the agents across sess… - grok", home)
	if !ok || got.SessionID != "new" {
		t.Fatalf("title match: %+v ok=%v", got, ok)
	}
	got, ok = pickGrok(rows, 9, 100, "grok", home)
	if !ok || got.SessionID != "new" {
		t.Fatalf("newest fallback: %+v ok=%v", got, ok)
	}
	if _, ok := pickGrok(rows, 9, 999, "grok", home); ok {
		t.Fatal("pid_start mismatch must not match")
	}
}

func writeSummary(t *testing.T, home, cwd, id, title string) {
	t.Helper()
	dir := filepath.Dir(summaryPath(home, cwd, id))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"generated_title":"` + title + `"}`
	if err := os.WriteFile(filepath.Join(dir, "summary.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPiReportAndSubagent(t *testing.T) {
	root := t.TempDir()
	state := t.TempDir()
	writeProc(t, root, 7, "pi", "S", 7, 50, nil)
	rep := `{"session_id":"pi-1","session_file":"/sess/pi-1.jsonl","cwd":"/work","pid":7,"pid_start":50}`
	if err := os.MkdirAll(filepath.Join(state, "pi"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(state, "pi", "7.json")
	if err := os.WriteFile(path, []byte(rep), 0o644); err != nil {
		t.Fatal(err)
	}
	got, ok := readPiReport(state, 7, 50)
	if !ok || got.SessionID != "pi-1" || got.SessionFile != "/sess/pi-1.jsonl" {
		t.Fatalf("report: %+v ok=%v", got, ok)
	}
	if _, ok := readPiReport(state, 7, 99); ok {
		t.Fatal("stale pid_start must be ignored")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("stale report should be removed")
	}

	sub := t.TempDir()
	meta := `{"childSessionId":"sub-1","sessionFile":"` + filepath.Join(sub, "session.jsonl") + `","cwd":"/work"}`
	if err := os.WriteFile(filepath.Join(sub, "metadata.json"), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
	env := "PI_SUBAGENT_RUN_DIR=" + sub + "\x00OTHER=no\x00"
	if err := os.WriteFile(filepath.Join(root, "7", "environ"), []byte(env), 0o644); err != nil {
		t.Fatal(err)
	}
	sm, ok := readSubagent(root, 7)
	if !ok || sm.ChildSessionID != "sub-1" {
		t.Fatalf("subagent: %+v ok=%v", sm, ok)
	}
}

func TestMergeKeepsUncertainAndOtherSockets(t *testing.T) {
	prev := []Record{
		{Agent: "grok", SessionID: "g1", PaneID: "%1", Session: "s", Window: "0", SocketKind: "name", SocketValue: "a"},
		{Agent: "pi", SessionID: "p1", PaneID: "%2", Session: "s", Window: "1", SocketKind: "name", SocketValue: "a"},
		{Agent: "pi", SessionID: "other", PaneID: "%9", Session: "s", Window: "0", SocketKind: "name", SocketValue: "b"},
	}
	sock := Socket{Kind: "name", Value: "a"}
	places := []place{{PaneID: "%1", Agent: "grok", Session: "s", Window: "0"}}
	got := merge(prev, sock, places, nil, map[string]bool{"%1\x00grok": true})
	if len(got) != 2 {
		t.Fatalf("want grok kept and the other socket kept, got %+v", got)
	}
	ids := map[string]bool{}
	for _, r := range got {
		ids[r.SessionID] = true
	}
	if !ids["g1"] || !ids["other"] || ids["p1"] {
		t.Fatalf("rows: %+v", got)
	}
	// A parsed registry with no row for this pid drops the pane.
	got = merge(prev, sock, places, nil, nil)
	for _, r := range got {
		if r.SessionID == "g1" {
			t.Fatal("certain miss must drop the grok row")
		}
	}
}

func TestSyncLedger(t *testing.T) {
	proc := t.TempDir()
	state := t.TempDir()
	home := t.TempDir()
	writeProc(t, proc, 10, "bash", "S", 40, 1, []int{40})
	writeProc(t, proc, 40, "grok", "S", 40, 100, nil)
	writeProc(t, proc, 7, "pi", "S", 7, 50, nil)
	writeSummary(t, home, "/work", "sess-g", "Focused title here")
	reg := `[{"session_id":"sess-g","pid":40,"pid_start":100,"cwd":"/work","opened_at":"2026-10-20T00:00:00Z"}]`
	if err := os.WriteFile(filepath.Join(home, "active_sessions.json"), []byte(reg), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(state, "pi"), 0o755); err != nil {
		t.Fatal(err)
	}
	piRep := `{"session_id":"sess-p","session_file":"/sess/p.jsonl","cwd":"/work","pid":7,"pid_start":50}`
	if err := os.WriteFile(filepath.Join(state, "pi", "7.json"), []byte(piRep), 0o644); err != nil {
		t.Fatal(err)
	}
	c := NewCache(Roots{StateDir: state, GrokHome: home, Proc: proc})
	sock := Socket{Kind: "path", Value: "/tmp/tmux-test"}
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	panes := []Pane{
		{ID: "%1", PID: "10", Cmd: "bash", Path: "/work", Session: "dev", Window: "0", WindowName: "grok", Title: "Focused title here - grok"},
		{ID: "%2", PID: "7", Cmd: "pi", Path: "/work", Session: "dev", Window: "1", WindowName: "pi"},
	}
	agents := []Agent{
		{PaneID: "%1", Agent: "grok", Session: "dev", Window: "0", WindowName: "grok", Path: "/work"},
		{PaneID: "%2", Agent: "pi", Session: "dev", Window: "1", WindowName: "pi", Path: "/work"},
	}
	c.Sync(sock, panes, agents, now)
	rows, err := Load(c.roots.LedgerPath())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("ledger: %+v", rows)
	}
	byAgent := map[string]Record{}
	for _, r := range rows {
		byAgent[r.Agent] = r
	}
	if byAgent["grok"].SessionID != "sess-g" || byAgent["grok"].PID != 40 || byAgent["grok"].Session != "dev" {
		t.Fatalf("grok row: %+v", byAgent["grok"])
	}
	if byAgent["pi"].SessionID != "sess-p" || byAgent["pi"].SessionFile != "/sess/p.jsonl" {
		t.Fatalf("pi row: %+v", byAgent["pi"])
	}
	// Same snapshot must not rewrite the file.
	before, _ := os.Stat(c.roots.LedgerPath())
	c.Sync(sock, panes, agents, now.Add(time.Minute))
	after, _ := os.Stat(c.roots.LedgerPath())
	if !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("unchanged set rewrote the ledger")
	}
	// Closing the pi pane drops it. A broken registry keeps the grok row.
	if err := os.WriteFile(filepath.Join(home, "active_sessions.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	c.Sync(sock, panes[:1], agents[:1], now.Add(2*time.Minute))
	rows, _ = Load(c.roots.LedgerPath())
	if len(rows) != 1 || rows[0].Agent != "grok" || rows[0].SessionID != "sess-g" {
		t.Fatalf("after close and bad registry: %+v", rows)
	}
	if err := os.WriteFile(filepath.Join(home, "active_sessions.json"), []byte("[]"), 0o644); err != nil {
		t.Fatal(err)
	}
	c.Sync(sock, panes[:1], agents[:1], now.Add(3*time.Minute))
	rows, _ = Load(c.roots.LedgerPath())
	if len(rows) != 0 {
		t.Fatalf("empty registry must drop grok, got %+v", rows)
	}
}

func TestPlan(t *testing.T) {
	rows := []Record{
		{Agent: "grok", SessionID: "g1", Cwd: "/work", PID: 1, PIDStart: 10, Session: "dev", Window: "1", WindowName: "grok"},
		{Agent: "pi", SessionID: "p1", SessionFile: "/sess/my file.jsonl", PID: 2, PIDStart: 20, Session: "dev", Window: "0", WindowName: "pi"},
		{Agent: "pi", SessionID: "p2", PID: 3, PIDStart: 30, Session: "other", Window: "0"},
	}
	alive := map[int]bool{1: true}
	open := map[string]bool{"pi\x00p2": true}
	acts := Plan(rows, func(pid int, start int64) bool { return alive[pid] }, func(agent, id string) bool {
		return open[agent+"\x00"+id]
	})
	if len(acts) != 3 {
		t.Fatal(len(acts))
	}
	// Window 0 before window 1.
	if acts[0].Record.SessionID != "p1" || acts[0].Command != "pi --session '/sess/my file.jsonl'" {
		t.Fatalf("pi command: %+v", acts[0])
	}
	if acts[1].Record.SessionID != "g1" || acts[1].Skip != "still running" || acts[1].Command != "" {
		t.Fatalf("grok skip: %+v", acts[1])
	}
	if acts[2].Skip != "session is already open" {
		t.Fatalf("open skip: %+v", acts[2])
	}
}
