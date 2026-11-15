#!/bin/bash
# Respawn panes with -k in a tight loop; on a failure, retry once after 50 ms and say whether that worked.
T=$(mktemp -d); sock=$T/s
tmux -f /dev/null -S $sock new-session -d -s a "sleep 1000"
for w in 1 2 3 4 5 6 7; do tmux -S $sock new-window -d -t a "sleep 1000"; done
for round in $(seq ${1:-300}); do
  for w in 0 1 2 3 4 5 6 7; do
    (
      if ! out=$(tmux -S $sock respawn-pane -k -t a:$w "sleep 1000" 2>&1); then
        sleep 0.05
        if out2=$(tmux -S $sock respawn-pane -k -t a:$w "sleep 1000" 2>&1); then r=ok; else r="failed again: $out2"; fi
        echo "FAIL $out | retry: $r"
      fi
    ) &
  done
  wait
done
tmux -S $sock kill-server; rm -rf $T
