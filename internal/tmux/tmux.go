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
}

func Run(args ...string) (string, error) {
	out, err := exec.Command("tmux", args...).Output()
	return strings.TrimRight(string(out), "\n"), err
}

func ListPanes() ([]Pane, error) {
	out, err := Run("list-panes", "-a", "-F", "#{pane_id}\t#{pane_pid}\t#{pane_current_command}\t#{pane_current_path}")
	if err != nil {
		return nil, err
	}
	var panes []Pane
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 4 {
			continue
		}
		panes = append(panes, Pane{ID: fields[0], PID: fields[1], Cmd: fields[2], Path: fields[3]})
	}
	return panes, nil
}

func Capture(paneID string) string {
	out, _ := Run("capture-pane", "-p", "-t", paneID)
	return out
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