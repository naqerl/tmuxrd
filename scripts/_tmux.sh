# Shared helper: resolve the tmux binary against TERDR_TMUX_SOCKET so scripts
# never fall back to the caller's default tmux server.
tmuxi() {
  if [ -n "${TERDR_TMUX_SOCKET:-}" ]; then
    command tmux -L "$TERDR_TMUX_SOCKET" "$@"
  else
    command tmux "$@"
  fi
}

# pin_window_name <window-target>
#
# Keep a window named after its agent. tmux renames a window from its *active*
# pane, so focusing the tmuxrd sidebar pane renames the window to "tmuxrd" and
# the sidebar then reads "tmuxrd (pi)". Pinning automatic-rename-format to a
# window option set from the agent pane keeps automatic-rename on (the window
# still renames when the agent changes) while the sidebar can never drive it.
pin_window_name() {
  _w=$1
  _agent=$(tmuxi list-panes -t "$_w" -F '#{pane_current_command} #{@tmuxrd_sidebar}' 2>/dev/null \
             | awk '$2 != "1" { print $1; exit }')
  case "$_agent" in
    pi|claude|codex|opencode|cursor|kimi|hermes|gemini|grok)
      tmuxi set-option -w -t "$_w" @tmuxrd_agent "$_agent"
      tmuxi set-option -w -t "$_w" automatic-rename-format \
        '#{?@tmuxrd_agent,#{@tmuxrd_agent},#{pane_current_command}}'
      ;;
  esac
}
