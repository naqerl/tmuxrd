package tmux

import (
	"os"
	"os/exec"
	"strings"
)

type Pane struct {
	ID       string
	PID      string
	Cmd      string
	Path     string
	Session  string
	Window   string
}

func Run(args ...string) (string, error) {
	cmdArgs := []string{}
	if v := os.Getenv("TERDR_TMUX_SOCKET"); v != "" {
		cmdArgs = append(cmdArgs, "-L", v)
	}
	cmdArgs = append(cmdArgs, args...)
	out, err := exec.Command("tmux", cmdArgs...).Output()
	return strings.TrimRight(string(out), "\n"), err
}

func ListPanes() ([]Pane, error) {
	out, err := Run("list-panes", "-a", "-F", "#{session_name}\t#{window_index}\t#{pane_id}\t#{pane_pid}\t#{pane_current_command}\t#{pane_current_path}")
	if err != nil {
		return nil, err
	}
	var panes []Pane
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 6 {
			continue
		}
		panes = append(panes, Pane{ID: fields[2], PID: fields[3], Cmd: fields[4], Path: fields[5], Session: fields[0], Window: fields[1]})
	}
	return panes, nil
}

func Capture(paneID string) string {
	out, _ := Run("capture-pane", "-p", "-t", paneID)
	return out
}

func CurrentPane() string {
	out, _ := Run("display-message", "-p", "#{pane_id}")
	return strings.TrimSpace(out)
}

func SelectPane(paneID string) {
	Run("switch-client", "-t", paneID)
	Run("select-pane", "-t", paneID)
}

func FocusPane(paneID string) { SelectPane(paneID) }

func Option(name, value string) { Run("set-option", "-gq", name, value) }

func IsTmux() bool {
	return os.Getenv("TMUX") != ""
}