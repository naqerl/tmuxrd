#!/usr/bin/env bash
BIN="/home/user/terdr/bin/terdr"
panes=$(tmux list-panes -a -F '#{pane_id} #{@terdr_sidebar}' | awk '$2==1{print $1}')
if [ -n "$panes" ]; then
  for p in $panes; do tmux kill-pane -t "$p"; done
  tmux set-option -g @terdr_enabled 0
else
  tmux list-windows -a -F '#{session_name}:#{window_index}' | while IFS= read -r w; do
    if ! tmux list-panes -t "$w" -F '#{@terdr_sidebar}' | grep -q '^1$'; then
      newpane=$(tmux split-window -hb -t "$w" -d -P -F '#{pane_id}' -l 32 "$BIN run")
      tmux set-option -p -t "$newpane" @terdr_sidebar 1
    fi
  done
  tmux set-option -g @terdr_enabled 1
fi
exit 0