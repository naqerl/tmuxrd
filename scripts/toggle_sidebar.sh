#!/usr/bin/env bash
# shellcheck source=scripts/_tmux.sh
. "$(dirname "$0")/_tmux.sh"
BIN="/home/user/terdr/bin/tmuxrd"
panes=$(tmuxi list-panes -a -F '#{pane_id} #{@tmuxrd_sidebar}' | awk '$2==1{print $1}')
if [ -n "$panes" ]; then
  for p in $panes; do tmuxi kill-pane -t "$p"; done
  tmuxi set-option -g @tmuxrd_enabled 0
else
  tmuxi list-windows -a -F '#{session_name}:#{window_index}' | while IFS= read -r w; do
    if ! tmuxi list-panes -t "$w" -F '#{@tmuxrd_sidebar}' | grep -q '^1$'; then
      newpane=$(tmuxi split-window -hb -t "$w" -d -P -F '#{pane_id}' -l 32 "$BIN client")
      tmuxi set-option -p -t "$newpane" @tmuxrd_sidebar 1
    fi
  done
  tmuxi set-option -g @tmuxrd_enabled 1
fi
exit 0
