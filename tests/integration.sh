#!/usr/bin/env bash
set -euo pipefail
SOCK="tmuxrd-int"
ROOT="/home/user/src/tmuxrd"
BIN="$ROOT/bin/tmuxrd"
tmux -L "$SOCK" kill-server 2>/dev/null || true
tmux -L "$SOCK" new-session -d -s t 'sleep 1000'
tmux -L "$SOCK" new-window
tmux -L "$SOCK" source-file "$ROOT/tmuxrd.tmux"
sleep 0.3

fail(){ echo "FAIL: $*"; tmux -L "$SOCK" kill-server; exit 1; }
assert_panes(){ [ "$(tmux -L "$SOCK" list-panes -a -F '#{@tmuxrd_sidebar}' | grep -c '^1$')" = "$1" ] || fail "expected $1 sidebar panes, got $(tmux -L "$SOCK" list-panes -a -F '#{@tmuxrd_sidebar}' | grep -c '^1$')"; }

# toggle on -> every window gets one
tmux -L "$SOCK" run-shell "$ROOT/scripts/toggle_sidebar.sh >/dev/null 2>&1"
sleep 0.8
assert_panes 2
# toggle off -> all removed
tmux -L "$SOCK" run-shell "$ROOT/scripts/toggle_sidebar.sh >/dev/null 2>&1"
sleep 0.4
assert_panes 0
# ensure new window respects disabled
tmux -L "$SOCK" new-window
sleep 0.4
assert_panes 0
# toggle on again
tmux -L "$SOCK" run-shell "$ROOT/scripts/toggle_sidebar.sh >/dev/null 2>&1"
sleep 0.8
assert_panes 3
# summary command works
$BIN summary >/dev/null || fail "summary failed"
# orphan cleanup on last pane kill
tmux -L "$SOCK" kill-pane -t t:0.0
sleep 0.5
# window count maybe reduced; no crash
tmux -L "$SOCK" kill-server
echo "ALL INTEGRATION TESTS PASSED"
