#!/bin/sh
set -u
name=$(basename "$0" .sh)

# One line per allowed use: file|fixed substring of the line|why. No line numbers.
exceptions='
e2e/push_follow_test.go|context.WithTimeout(context.Background(), hold)|a hold window on a socket read
e2e/ships_helpers_test.go|time.After(pollEvery)|pacing in a hand-written poll
e2e/autopilot_gate_test.go|time.Now().Add(500 * time.Millisecond)|a short tail that catches a late duplicate
e2e/lock_test.go|cmd.WaitDelay = 500 * time.Millisecond|a cleanup grace: a killed lock leaves its command holding our pipes
e2e/tailscale_rebind_test.go|net.DialTimeout("tcp", addr, time.Second)|pacing: a timed-out dial in a dialer loop decides nothing
'

shapes='(deadline|end) :?= time\.Now\(\)\.Add\(|for .*time\.Now\(\)\.Add\(|time\.Now\(\)\.(Before|After)\(|context\.With(Timeout|Deadline)\(|time\.(After|NewTimer|AfterFunc)\(|http\.Client\{[^}]*Timeout:|net\.DialTimeout\(|WaitDelay =|deadline\+\+|attempt == [0-9]'

raw=$(git grep -nE "$shapes" -- 'e2e/*.go' ':!e2e/deadline_test.go' ':!e2e/harness_test.go' ':!e2e/harness_ports_test.go')
status=$?
[ "$status" -le 1 ] || exit 2
[ -z "$raw" ] && exit 0

hits=
while IFS= read -r line; do
	file=${line%%:*}
	rest=${line#*:}
	text=${rest#*:}
	allowed=
	while IFS='|' read -r efile esub _why; do
		[ -n "$efile" ] || continue
		[ "$efile" = "$file" ] || continue
		case $text in *"$esub"*) allowed=1 ;; esac
	done <<EOF
$exceptions
EOF
	[ -n "$allowed" ] || hits="$hits$line
"
done <<EOF
$raw
EOF

[ -z "$hits" ] && exit 0
printf '%s: %s\n' "$name" "wait with eventually or until on the test run's one deadline, not a clock of the test's own" >&2
printf '%s' "$hits" >&2
exit 1
