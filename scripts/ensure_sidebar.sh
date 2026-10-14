#!/usr/bin/env bash
. "$(dirname "$0")/_tmux.sh"
BIN="/home/user/terdr/bin/tmuxrd"
enabled=$(tmuxi show-option -gqv @tmuxrd_enabled)
[ "$enabled" = "0" ] && exit 0
if ! tmuxi list-panes -F '#{@tmuxrd_sidebar}' | grep -q '^1$'; then
  newpane=$(tmuxi split-window -hb -d -P -F '#{pane_id}' -l 32 "$BIN client")
  tmuxi set-option -p -t "$newpane" @tmuxrd_sidebar 1
  pin_window_name "$(tmuxi display-message -p '#{session_name}:#{window_index}')"
fi
exit 0
