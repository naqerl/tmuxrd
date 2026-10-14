#!/usr/bin/env bash
for p in $(tmux list-panes -a -F '#{pane_id} #{@tmuxrd_sidebar}' | awk '$2==1{print $1}'); do
  c=$(tmux display-message -p -t "$p" '#{window_panes}' 2>/dev/null)
  echo "p=$p c=$c" >> /tmp/cleanup.log
  if [ "$c" = "1" ]; then
    tmux kill-pane -t "$p" 2>>/tmp/cleanup.log
    echo "killed=$? p=$p" >> /tmp/cleanup.log
  fi
done
exit 0
