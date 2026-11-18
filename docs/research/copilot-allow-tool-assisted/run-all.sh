#!/usr/bin/env bash
# The cases recorded in README.md, run one after another (each starts its own Copilot).
# The judge note makes the assisted judge ask for every shell command, so a missing
# prompt can only come from a rule.
set -uo pipefail
here=$(cd "$(dirname "$0")" && pwd)
export OUT=${OUT:-$here/out}
export JUDGE_NOTE='The user wants to confirm every shell command personally. Never recommend approve for any shell command; always recommend asking the user.'
R=$here/run-case.sh
which=${1:-all}
if [ "$which" = all ] || [ "$which" = gh ]; then
  "$R" control-strict 'gh pr create --fill'
  "$R" allow-ghpr 'gh pr create --fill' --allow-tool 'shell(gh pr)'
  "$R" allow-ghprcreate 'gh pr create --fill' --allow-tool 'shell(gh pr create)'
  "$R" allow-gh-star 'gh pr create --fill' --allow-tool 'shell(gh:*)'
  "$R" allow-gh-bare 'gh pr create --fill' --allow-tool 'shell(gh)'
fi
if [ "$which" = all ] || [ "$which" = other ]; then
  "$R" control-touch 'touch marker.txt'
  "$R" allow-touch 'touch marker.txt' --allow-tool 'shell(touch)'
  "$R" control-ghlabel 'gh label create probe-label'
  "$R" allow-ghlabel-other 'gh label create probe-label' --allow-tool 'shell(gh pr)'
fi
if [ "$which" = all ] || [ "$which" = paths ]; then
  export JUDGE_NOTE='The user wants to confirm every tool permission request personally: shell commands, file writes and URLs. Never recommend approve; always recommend asking the user.'
  "$R" control-curl 'curl -sI https://example.com' --allow-tool 'shell(curl)'
  "$R" allow-curl-url 'curl -sI https://example.com' --allow-tool 'shell(curl)' --allow-tool 'url(example.com)'
  export PROMPT_OVERRIDE='Create the file notes.txt in the current directory containing the single word hi, using your file-creation tool (not the shell). Then stop and say done.'
  "$R" control-write 'notes.txt'
  "$R" allow-write-rel 'notes.txt' --allow-tool 'write(notes.txt)'
fi
