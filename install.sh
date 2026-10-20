#!/usr/bin/env bash
#
# tmuxrd installer / updater.
#
#   curl -fsSL https://raw.githubusercontent.com/naqerl/tmuxrd/main/install.sh | bash
#
# It installs (or updates) the tmuxrd binary, wires ~/.tmux.conf to load it,
# and — if a tmux server is running — restarts the daemon and rebuilds the
# sidebar panes so the running tmux picks up the new binary.
#
# Run it from a checkout and it builds and installs that checkout; run it via
# curl and it downloads the latest release for this OS/arch.
#
# Environment overrides:
#   TMUXRD_ROOT     install directory              (default: $HOME/.tmuxrd)
#   TMUXRD_CONF     tmux config to wire            (default: $HOME/.tmux.conf)
#   TMUXRD_VERSION  release tag, e.g. v0.0.2       (default: latest)
#
set -euo pipefail

REPO="naqerl/tmuxrd"
RELEASES="https://github.com/${REPO}/releases"

ROOT="${TMUXRD_ROOT:-$HOME/.tmuxrd}"
TMUX_CONF="${TMUXRD_CONF:-$HOME/.tmux.conf}"
VERSION="${TMUXRD_VERSION:-latest}"

log() { printf 'tmuxrd: %s\n' "$*"; }
warn() { printf 'tmuxrd: warning: %s\n' "$*" >&2; }
die() { printf 'tmuxrd: error: %s\n' "$*" >&2; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }

# tmuxi addresses the same server the rest of tmuxrd does: TERDR_TMUX_SOCKET
# when set (tests, non-default servers), otherwise tmux's own default.
tmuxi() {
  if [ -n "${TERDR_TMUX_SOCKET:-}" ]; then
    command tmux -L "$TERDR_TMUX_SOCKET" "$@"
  else
    command tmux "$@"
  fi
}

# A scratch directory for the release download, removed on exit. It is a global
# (not a local of the download function) because the EXIT trap outlives the
# function and would otherwise trip `set -u`.
WORKDIR=""
cleanup() { if [ -n "$WORKDIR" ]; then rm -rf "$WORKDIR"; fi; }
trap cleanup EXIT

# --- where do the files come from? ------------------------------------------
# A checkout wins: if this script sits next to tmuxrd.tmux we build and install
# it. That is also what the test suite exercises. Otherwise download a release.
SELF="${BASH_SOURCE[0]:-}"
SRC=""
if [ -n "${TMUXRD_SOURCE_DIR:-}" ]; then
  SRC="$TMUXRD_SOURCE_DIR"
elif [ -n "$SELF" ] && [ -f "$SELF" ]; then
  d="$(cd "$(dirname "$SELF")" && pwd)"
  if [ -f "$d/tmuxrd.tmux" ]; then SRC="$d"; fi
fi

detect_target() {
  case "$(uname -s)" in
    Linux) os=linux ;;
    Darwin) os=darwin ;;
    *) die "unsupported OS $(uname -s) (releases cover linux and darwin)" ;;
  esac
  case "$(uname -m)" in
    x86_64 | amd64) arch=amd64 ;;
    arm64 | aarch64) arch=arm64 ;;
    *) die "unsupported architecture $(uname -m)" ;;
  esac
}

# install_file copies a file over another atomically. A plain `cp` over the
# running tmuxrd binary fails with ETXTBSY ("Text file busy"); writing a temp
# file and renaming it leaves any running process on its old inode.
install_file() {
  local src="$1" dst="$2" tmp="${2}.new.$$"
  cp "$src" "$tmp"
  chmod +x "$tmp" 2>/dev/null || true
  mv -f "$tmp" "$dst"
}

# install_tree moves an unpacked/installed tree into place.
install_tree() {
  local from="$1"
  mkdir -p "$ROOT/bin" "$ROOT/scripts"
  install_file "$from/bin/tmuxrd" "$ROOT/bin/tmuxrd"
  cp "$from/tmuxrd.tmux" "$ROOT/tmuxrd.tmux"
  cp "$from"/scripts/*.sh "$ROOT/scripts/"
  if [ -f "$from/README.md" ]; then cp "$from/README.md" "$ROOT/README.md"; fi
  if [ -d "$from/extensions" ]; then
    mkdir -p "$ROOT/extensions"
    cp -a "$from/extensions/." "$ROOT/extensions/"
  fi
}

# pi keeps its conversation id inside the process. When pi is installed, copy
# the extension that writes the id where the daemon can read it. A machine
# without pi is left alone. Re-running replaces the file; it is managed.
pi_agent_dir() {
  if [ -n "${PI_CODING_AGENT_DIR:-}" ]; then
    printf '%s\n' "$PI_CODING_AGENT_DIR"
    return
  fi
  printf '%s\n' "$HOME/.pi/agent"
}

pi_detected() {
  if have pi; then return 0; fi
  if [ -d "$(pi_agent_dir)" ]; then return 0; fi
  return 1
}

install_pi_extension() {
  local src="$ROOT/extensions/pi/tmuxrd-session.ts"
  [ -f "$src" ] || return 0
  if ! pi_detected; then
    return 0
  fi
  local dest
  dest="$(pi_agent_dir)/extensions"
  mkdir -p "$dest"
  local tmp="$dest/tmuxrd-session.ts.new.$$"
  cp "$src" "$tmp"
  mv -f "$tmp" "$dest/tmuxrd-session.ts"
  log "installed the pi session extension into $dest"
}

verify_checksum() {
  # Best effort: the release publishes checksums.txt next to the archives.
  local file="$1" name="$2" want got sums
  local tool=""
  if have sha256sum; then tool="sha256sum"; elif have shasum; then tool="shasum -a 256"; fi
  [ -n "$tool" ] || { warn "no sha256 tool; skipping checksum verification"; return 0; }
  if [ "$VERSION" = latest ]; then
    sums="$RELEASES/latest/download/checksums.txt"
  else
    sums="$RELEASES/download/$VERSION/checksums.txt"
  fi
  want="$(curl -fsSL "$sums" 2>/dev/null | awk -v n="$name" '$2 == n || $2 == "*"n { print $1 }')" || true
  if [ -z "$want" ]; then
    warn "no published checksum for $name; skipping verification"
    return 0
  fi
  got="$($tool "$file" | awk '{print $1}')"
  [ "$want" = "$got" ] || die "checksum mismatch for $name (want $want, got $got)"
  log "checksum ok"
}

install_from_source() {
  [ -f "$SRC/Makefile" ] || die "$SRC is not a tmuxrd checkout"
  if [ -f "$SRC/bin/tmuxrd" ] && [ -f "$SRC/tmuxrd.tmux" ]; then
    log "using the prebuilt binary in $SRC"
  elif have go; then
    log "building $SRC"
    (cd "$SRC" && make build) || die "build failed"
  else
    die "no prebuilt binary and no go toolchain to build $SRC"
  fi
  [ -x "$SRC/bin/tmuxrd" ] || die "no binary at $SRC/bin/tmuxrd"

  if [ "$SRC" != "$ROOT" ]; then
    install_tree "$SRC"
  fi
}

install_from_release() {
  have curl || die "curl is required to download a release"
  have tar || die "tar is required"
  detect_target
  local name="tmuxrd_${os}_${arch}.tar.gz"
  local url
  if [ "$VERSION" = latest ]; then
    url="$RELEASES/latest/download/$name"
  else
    url="$RELEASES/download/$VERSION/$name"
  fi
  local tmp
  WORKDIR="$(mktemp -d)"
  tmp="$WORKDIR"
  log "downloading $url"
  curl -fsSL "$url" -o "$tmp/$name" || die "download failed: $url"
  verify_checksum "$tmp/$name" "$name"
  tar -xzf "$tmp/$name" -C "$tmp" || die "could not extract $name"
  local unpack="$tmp/tmuxrd_${os}_${arch}"
  [ -x "$unpack/bin/tmuxrd" ] || unpack="$tmp"
  [ -x "$unpack/bin/tmuxrd" ] || die "unexpected archive layout in $name"
  install_tree "$unpack"
}

# --- wire ~/.tmux.conf ------------------------------------------------------
# Replace any previous wiring (a managed block, or the unmarked
# @tmuxrd_root / source-file lines an earlier manual install left behind) so
# re-running is idempotent and never double-sources the plugin.
wire_conf() {
  touch "$TMUX_CONF"
  local tmp
  tmp="$(mktemp)"
  grep -vE 'tmuxrd\.tmux|@tmuxrd_root|>>> tmuxrd|<<< tmuxrd' "$TMUX_CONF" >"$tmp" || true
  {
    printf '\n# >>> tmuxrd >>>\n'
    printf '# Managed by install.sh. Set @tmuxrd_root only if you move the checkout.\n'
    printf 'set -g @tmuxrd_root "%s"\n' "$ROOT"
    printf 'source-file "%s/tmuxrd.tmux"\n' "$ROOT"
    printf '# Clicking/tapping the sidebar also needs: set -g mouse on\n'
    printf '# <<< tmuxrd <<<\n'
  } >>"$tmp"
  mv "$tmp" "$TMUX_CONF"
  log "wired $TMUX_CONF"
}

# --- restart the daemon and refresh the panes -------------------------------
# Stop the daemon for the server being updated. `tmuxrd stop` addresses only
# that server's socket; older daemons (before `stop` existed) ignore the message,
# so on Linux fall back to finding them by environment rather than killing every
# tmuxrd daemon on the machine.
stop_daemon() {
  "$ROOT/bin/tmuxrd" stop >/dev/null 2>&1 || true
  have pgrep || return 0
  if [ ! -d /proc ]; then
    if have pkill; then pkill -f 'tmuxrd daemon' 2>/dev/null || true; fi
    return 0
  fi
  local want="${TERDR_TMUX_SOCKET:-}" pid args env
  for pid in $(pgrep -x tmuxrd 2>/dev/null || true); do
    args="$(tr '\0' ' ' </proc/"$pid"/cmdline 2>/dev/null || true)"
    case "$args" in *"tmuxrd daemon"*) ;; *) continue ;; esac
    env="$(tr '\0' '\n' </proc/"$pid"/environ 2>/dev/null || true)"
    if [ -n "$want" ]; then
      case "$env" in *"TERDR_TMUX_SOCKET=$want"*) kill "$pid" 2>/dev/null || true ;; esac
    else
      case "$env" in *"TERDR_TMUX_SOCKET="*) ;; *) kill "$pid" 2>/dev/null || true ;; esac
    fi
  done
}

restart_daemon() {
  if ! have tmux; then
    warn "tmux not found; skipping the daemon restart"
    return 0
  fi
  if ! tmuxi list-sessions >/dev/null 2>&1; then
    log "no tmux server is running; the sidebar appears the next time you start one"
    return 0
  fi

  stop_daemon

  # Older tmuxrd versions put a summary in status-right (and forced
  # status-interval 1). Take that back out — but only if it is ours, never the
  # user's own status line.
  sr="$(tmuxi show-options -g status-right 2>/dev/null || true)"
  case "$sr" in
    *"tmuxrd summary"*)
      tmuxi set-option -gu status-right
      tmuxi set-option -gu status-interval
      log "removed tmuxrd's old status-line edit"
      ;;
  esac

  tmuxi set-option -gq @tmuxrd_root "$ROOT"
  tmuxi source-file "$ROOT/tmuxrd.tmux" >/dev/null 2>&1 || true
  tmuxi run-shell -b "$ROOT/bin/tmuxrd daemon >/dev/null 2>&1"

  # Rebuild the sidebar panes so they run the new client (an old client would
  # keep its old code until its pane is replaced).
  if [ "$(tmuxi show-option -gqv @tmuxrd_enabled 2>/dev/null || true)" != "0" ]; then
    for p in $(tmuxi list-panes -a -F '#{pane_id} #{@tmuxrd_sidebar}' 2>/dev/null | awk '$2==1{print $1}'); do
      tmuxi kill-pane -t "$p" 2>/dev/null || true
    done
    "$ROOT/bin/tmuxrd" adapt --create >/dev/null 2>&1 || true
    bash "$ROOT/scripts/ensure_sidebar.sh" >/dev/null 2>&1 || true
  fi

  if [ "$(tmuxi show-options -g mouse 2>/dev/null | awk '{print $NF}' || true)" = off ]; then
    warn "tmux 'mouse' is off: clicking/tapping the sidebar will do nothing until you 'set -g mouse on'"
  fi
  log "daemon restarted and sidebar panes refreshed"
}

# --- go ---------------------------------------------------------------------
log "installing tmuxrd into $ROOT"
if [ -n "$SRC" ]; then
  install_from_source
  if [ "$SRC" = "$ROOT" ]; then log "installed in place from $SRC"; fi
else
  install_from_release
fi
[ -x "$ROOT/bin/tmuxrd" ] || die "install did not produce $ROOT/bin/tmuxrd"
chmod +x "$ROOT/bin/tmuxrd" 2>/dev/null || true
chmod +x "$ROOT"/scripts/*.sh 2>/dev/null || true

install_pi_extension
wire_conf
restart_daemon

log "version: $("$ROOT/bin/tmuxrd" version 2>/dev/null || echo unknown)"
log "done. press prefix-b to toggle the sidebar; see $ROOT/README.md"
