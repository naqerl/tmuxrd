#!/usr/bin/env bash
BIN="/home/user/terdr/bin/terdr"
enabled=$(tmux show-option -gqv @terdr_enabled)
[ "$enabled" = "0" ] && exit 0
if ! tmux list-panes -F '#{@terdr_sidebar}' | grep -q '^1$'; then
  newpane=$(tmux split-window -hb -d -P -F '#{pane_id}' -l 32 "$BIN run")
  tmux set-option -p -t "$newpane" @terdr_sidebar 1
fi
