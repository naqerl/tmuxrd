# tmuxrd.tmux — the agent status sidebar.
#
# Load it with:
#   tmux source-file /path/to/tmuxrd.tmux
#
# To install somewhere other than the default root, set @tmuxrd_root first:
#   set -g @tmuxrd_root "/path/to/tmuxrd"
#   source-file "/path/to/tmuxrd.tmux"

# Repo root. A pre-set @tmuxrd_root wins, so the default is only a fallback
# (the true-branch is a no-op just to keep tmux's if-shell happy).
if-shell -F '#{@tmuxrd_root}' 'source-file /dev/null' 'set-option -gq @tmuxrd_root /home/user/src/tmuxrd'

run-shell -b "#{@tmuxrd_root}/bin/tmuxrd daemon >/dev/null 2>&1"

bind-key b run-shell -b "#{@tmuxrd_root}/scripts/toggle_sidebar.sh >/dev/null 2>&1"
set-hook -g after-new-window 'run-shell -b "#{@tmuxrd_root}/scripts/ensure_sidebar.sh #{session_name}:#{window_index} >/dev/null 2>&1"'
set-hook -g after-new-session 'run-shell -b "#{@tmuxrd_root}/scripts/ensure_sidebar.sh #{session_name}:#{window_index} >/dev/null 2>&1"'
set-hook -g pane-exited 'run-shell -b "#{@tmuxrd_root}/scripts/cleanup_orphans.sh >/dev/null 2>&1; #{@tmuxrd_root}/bin/tmuxrd notify >/dev/null 2>&1 || true"'
set-hook -g after-kill-pane 'run-shell -b "#{@tmuxrd_root}/scripts/cleanup_orphans.sh >/dev/null 2>&1; #{@tmuxrd_root}/bin/tmuxrd notify >/dev/null 2>&1 || true"'
set-hook -g pane-focus-in 'run-shell -b "#{@tmuxrd_root}/bin/tmuxrd notify >/dev/null 2>&1 || true"'
set-hook -g after-select-pane 'run-shell -b "#{@tmuxrd_root}/bin/tmuxrd notify >/dev/null 2>&1 || true"'
set-hook -g after-select-window 'run-shell -b "#{@tmuxrd_root}/bin/tmuxrd notify window #{session_name}:#{window_index} >/dev/null 2>&1 || true"'
set-hook -g after-split-window 'run-shell -b "#{@tmuxrd_root}/bin/tmuxrd notify >/dev/null 2>&1 || true"'
set-hook -g window-layout-changed 'run-shell -b "#{@tmuxrd_root}/bin/tmuxrd notify layout >/dev/null 2>&1 || true"'
display-message "tmuxrd loaded"
