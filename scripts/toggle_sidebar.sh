#!/usr/bin/env bash
# prefix-b: toggle the sidebar in every window of every session.
#
# Enabling delegates creation to `tmuxrd adapt --create`, which builds each
# window's sidebar in the shape that window's width calls for (a 32-column left
# pane on a desktop, a 2-row top strip on a phone).
# shellcheck source=scripts/_tmux.sh
. "$(dirname "$0")/_tmux.sh"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BIN="$ROOT/bin/tmuxrd"
panes=$(tmuxi list-panes -a -F '#{pane_id} #{@tmuxrd_sidebar}' | awk '$2==1{print $1}')
if [ -n "$panes" ]; then
  for p in $panes; do tmuxi kill-pane -t "$p"; done
  tmuxi set-option -g @tmuxrd_enabled 0
else
  tmuxi set-option -g @tmuxrd_enabled 1
  "$BIN" adapt --create
  for w in $(tmuxi list-windows -a -F '#{session_name}:#{window_index}'); do
    pin_window_name "$w"
  done
fi
exit 0
