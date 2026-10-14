# tmuxrd.tmux — load via:  tmux source-file /home/user/terdr/tmuxrd.tmux
# (or run-shell /home/user/terdr/tmuxrd.tmux)

run-shell -b "/home/user/terdr/bin/tmuxrd daemon >/dev/null 2>&1"

bind-key b run-shell -b "/home/user/terdr/scripts/toggle_sidebar.sh >/dev/null 2>&1"
set-hook -g after-new-window 'run-shell -b "/home/user/terdr/scripts/ensure_sidebar.sh >/dev/null 2>&1"'
set-hook -g after-new-session 'run-shell -b "/home/user/terdr/scripts/ensure_sidebar.sh >/dev/null 2>&1"'
set-hook -g pane-exited 'run-shell -b "/home/user/terdr/scripts/cleanup_orphans.sh >/dev/null 2>&1; /home/user/terdr/bin/tmuxrd notify >/dev/null 2>&1 || true"'
set-hook -g after-kill-pane 'run-shell -b "/home/user/terdr/scripts/cleanup_orphans.sh >/dev/null 2>&1; /home/user/terdr/bin/tmuxrd notify >/dev/null 2>&1 || true"'
set-hook -g pane-focus-in 'run-shell -b "/home/user/terdr/bin/tmuxrd notify >/dev/null 2>&1 || true"'
set-hook -g after-select-pane 'run-shell -b "/home/user/terdr/bin/tmuxrd notify >/dev/null 2>&1 || true"'
set-hook -g after-select-window 'run-shell -b "/home/user/terdr/bin/tmuxrd notify >/dev/null 2>&1 || true"'
set-hook -g after-split-window 'run-shell -b "/home/user/terdr/bin/tmuxrd notify >/dev/null 2>&1 || true"'
set-hook -g window-layout-changed 'run-shell -b "/home/user/terdr/bin/tmuxrd notify >/dev/null 2>&1 || true"'
set-option -g status-right "#(/home/user/terdr/bin/tmuxrd summary) | %H:%M"
set-option -g status-interval 1
display-message "tmuxrd loaded"