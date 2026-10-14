#!/usr/bin/env bash
set -euo pipefail
SOCK=terdr-load
ROOT=/home/user/terdr
BIN=$ROOT/bin/terdr
tmux -L "$SOCK" kill-server 2>/dev/null || true
mkdir -p /tmp/mockbin
cp /bin/sleep /tmp/mockbin/pi
tmux -L "$SOCK" new-session -d -s s0 'sleep 1000'
tmux -L "$SOCK" source-file "$ROOT/terdr.tmux"
sleep .3
PATH=/tmp/mockbin:$PATH \
  TERDR_TMUX_SOCKET=$SOCK \
  /home/user/terdr/bin/terdr daemon >/tmp/terdr-load-daemon.log 2>&1 &
daemon=$!
sleep .5
for sessions in 5 15; do
  echo "=== $sessions sessions x 3 windows ==="
  for i in $(seq 1 $sessions); do
    tmux -L "$SOCK" new-session -d -s "s$i" "PATH=/tmp/mockbin:\$PATH pi 1000" 2>/dev/null || true
    for w in 1 2; do
      tmux -L "$SOCK" new-window -t "s$i" "PATH=/tmp/mockbin:\$PATH pi 1000" 2>/dev/null || true
    done
  done
  sleep 1
  for iter in 1 2; do
    /usr/bin/time -f "summary: %e s" env TERDR_TMUX_SOCKET=$SOCK $BIN summary >/dev/null
  done
  echo "  daemon: $(ps -p $(cat $BIN 2>/dev/null >/dev/null; pgrep -f 'terdr daemon' | head -1) -o pmem=,pcpu=,rss= 2>/dev/null)"
  echo "  top clients:"; pgrep -x terdr | while read pid; do
    ps -p "$pid" -o pid,pmem,pcpu,rss,cmd | tail -n +2
  done | sort -k3 -rn | head -3
  echo
done
kill -9 "$daemon" 2>/dev/null || true
tmux -L "$SOCK" kill-server 2>/dev/null || true
echo "LOAD TEST DONE"
