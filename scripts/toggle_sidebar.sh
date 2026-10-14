#!/usr/bin/env bash
BIN="/home/user/terdr/bin/tmuxrd"
panes=$(tmux list-panes -a -F '#{pane_id} #{@tmuxrd_sidebar}' | awk '$2==1{print $1}')
if [ -n "$panes" ]; then
  for p in $panes; do tmux kill-pane -t "$p"; done
  tmux set-option -g @tmuxrd_enabled 0
else
  tmux list-windows -a -F '#{session_name}:#{window_index}' | while IFS= read -r w; do
    if ! tmux list-panes -t "$w" -F '#{@tmuxrd_sidebar}' | grep -q '^1$'; then
      newpane=$(tmux split-window -hb -t "$w" -d -P -F '#{pane_id}' -l 32 "$BIN client")
      tmux set-option -p -t "$newpane" @tmuxrd_sidebar 1
    fi
  done
  tmux set-option -g @tmuxrd_enabled 1
fi
exit 0