# terdr.tmux — load via:  tmux source-file /home/user/terdr/terdr.tmux
# (or run-shell /home/user/terdr/terdr.tmux)

bind-key b run-shell "/home/user/terdr/scripts/toggle_sidebar.sh >/dev/null 2>&1"
set-hook -g after-new-window 'run-shell "/home/user/terdr/scripts/ensure_sidebar.sh >/dev/null 2>&1"'
set-hook -g after-new-session 'run-shell "/home/user/terdr/scripts/ensure_sidebar.sh >/dev/null 2>&1"'
set-hook -g pane-exited 'run-shell "/home/user/terdr/scripts/cleanup_orphans.sh >/dev/null 2>&1"'
set-hook -g after-select-pane 'run-shell "pkill -USR1 -x terdr >/dev/null 2>&1"'
set-hook -g after-select-window 'run-shell "pkill -USR1 -x terdr >/dev/null 2>&1"'
set-option -g status-right "#(/home/user/terdr/bin/terdr summary) | %H:%M"
set-option -g status-interval 2
display-message "terdr loaded"