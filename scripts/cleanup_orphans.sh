#!/usr/bin/env bash
for p in $(tmux list-panes -a -F '#{pane_id} #{@terdr_sidebar}' | awk '$2==1{print $1}'); do
  c=$(tmux display-message -p -t "$p" '#{window_panes}' 2>/dev/null)
  if [ "$c" = "1" ]; then
    tmux kill-pane -t "$p" 2>/dev/null
  fi
done
exit 0