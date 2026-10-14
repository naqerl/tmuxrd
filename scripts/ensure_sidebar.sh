#!/usr/bin/env bash
BIN="/home/user/tmuxrd/bin/tmuxrd"
enabled=$(tmux show-option -gqv @tmuxrd_enabled)
[ "$enabled" = "0" ] && exit 0
if ! tmux list-panes -F '#{@tmuxrd_sidebar}' | grep -q '^1$'; then
  newpane=$(tmux split-window -hb -d -P -F '#{pane_id}' -l 32 "$BIN client")
  tmux set-option -p -t "$newpane" @tmuxrd_sidebar 1
fi
