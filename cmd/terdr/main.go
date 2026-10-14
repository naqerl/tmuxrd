package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/naqerl/terdr/internal/detect"
	"github.com/naqerl/terdr/internal/tmux"
)

func glyph(s detect.State) string {
	switch s {
	case detect.Idle:
		return "✅"
	case detect.Working:
		return "⚙"
	case detect.Blocked:
		return "⛔"
	default:
		return "·"
	}
}

func render() {
	results := detect.Detect()
	fmt.Print("\033[2J\033[H")
	fmt.Println(" terdr — agent panes")
	fmt.Println(" -------------------")
	for _, r := range results {
		fmt.Printf(" %s  %-7s  %-8s  %s\n", glyph(r.State), r.Agent, r.State, shortPath(r.Path))
	}
	fmt.Printf("\n %d agents\n", len(results))
}

func shortPath(p string) string {
	// basename-ish
	if p == "" {
		return "-"
	}
	// last component
	i := len(p) - 1
	for i > 0 && p[i] != '/' {
		i--
	}
	if i >= 0 && i < len(p)-1 {
		return p[i+1:]
	}
	return p
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: terdr run|summary")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "summary":
		results := detect.Detect()
		c := map[detect.State]int{}
		for _, r := range results {
			c[r.State]++
		}
		fmt.Printf("agents=%d idle=%d working=%d blocked=%d\n", len(results), c[detect.Idle], c[detect.Working], c[detect.Blocked])
	case "run":
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		render()
		for {
			select {
			case <-sig:
				return
			case <-ticker.C:
				render()
			}
		}
	case "jump":
		// terdr jump %id
		if len(os.Args) < 3 {
			return
		}
		tmux.SelectPane(os.Args[2])
	default:
		fmt.Println("unknown command")
		os.Exit(2)
	}
}