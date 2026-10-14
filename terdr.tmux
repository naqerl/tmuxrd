# terdr.tmux — load via:  tmux source-file /home/user/terdr/terdr.tmux
# (or run-shell /home/user/terdr/terdr.tmux)

bind-key b run-shell "/home/user/terdr/scripts/toggle_sidebar.sh"
set-hook -g after-new-window 'run-shell "/home/user/terdr/scripts/ensure_sidebar.sh"'
set-hook -g after-new-session 'run-shell "/home/user/terdr/scripts/ensure_sidebar.sh"'
set-option -g status-right "#(/home/user/terdr/bin/terdr summary) | %H:%M"
set-option -g status-interval 2
display-message "terdr loaded"