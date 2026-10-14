#!/usr/bin/env bash
# after-new-window / after-new-session: make sure every window has a sidebar in
# the shape its width calls for.
#
# The shape decision (and the split itself) lives in `tmuxrd adapt --create`, so
# the desktop sidebar and the mobile strip are built by one implementation. This
# script only adds the window-name pinning that has to happen after panes exist.
# shellcheck source=scripts/_tmux.sh
. "$(dirname "$0")/_tmux.sh"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BIN="$ROOT/bin/tmuxrd"
enabled=$(tmuxi show-option -gqv @tmuxrd_enabled)
[ "$enabled" = "0" ] && exit 0
"$BIN" adapt --create
# Keep each window named after its agent. tmux renames a window from its active
# pane, so without this a sidebar that takes focus renames the window to
# "tmuxrd".
for w in $(tmuxi list-windows -a -F '#{session_name}:#{window_index}'); do
  pin_window_name "$w"
done
exit 0
