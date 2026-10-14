# Shared helper: resolve the tmux binary against TERDR_TMUX_SOCKET so scripts
# never fall back to the caller's default tmux server.
tmuxi() {
  if [ -n "${TERDR_TMUX_SOCKET:-}" ]; then
    command tmux -L "$TERDR_TMUX_SOCKET" "$@"
  else
    command tmux "$@"
  fi
}
