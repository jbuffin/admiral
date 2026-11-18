# Copilot: does a launch-time `--allow-tool` stop an assisted-mode prompt?

Research for [#822](https://github.com/jbuffin/admiral/issues/822), part of [#803](https://github.com/jbuffin/admiral/issues/803). November 2026, GitHub Copilot CLI 1.0.114 (`copilot --version`), run interactively in tmux the way a ship runs.

## Answer

**Yes.** A launch-time `--allow-tool` rule that matches the request stops the prompt in assisted mode. Copilot checks the rule first, so the judge is never called and no `permission.requested` event is written. The tool simply runs: `tool.execution_start` and then `tool.execution_complete`, with nothing in between.

The narrowest rule is the event's own identifier, wrapped in its kind:

| What the `permission.requested` event carries | Narrowest `--allow-tool` rule | Seen in this run |
| --- | --- | --- |
| `kind: "shell"`, `promptRequest.commandIdentifiers: ["gh pr"]` | `shell(gh pr)` | matched |
| same | `shell(gh pr create)` | **did not match**: still prompted |
| same | `shell(gh)` | **did not match**: still prompted |
| same | `shell(gh:*)` | matched (wider: every `gh` command) |
| `kind: "shell"`, `commandIdentifiers: ["touch"]` | `shell(touch)` | matched |
| `kind: "shell"`, `commandIdentifiers: ["gh label"]` | `shell(gh pr)` (the wrong subcommand) | did not match: prompted |
| `kind: "url"`, `promptRequest.url: "https://example.com"` | `url(example.com)` | matched |
| `kind: "write"`, `promptRequest.fileName: "<abs path>/notes.txt"` | `write(notes.txt)` | matched |

So, for a shell prompt, the rule is `shell(<identifier>)` for each entry of `commandIdentifiers`, taken word for word. It is not built from `fullCommandText` or `staticTokens`. Copilot's help says the same thing (`copilot help permissions`, "Tool rules"): "Only the first subcommand is part of the name, so "shell(gh pr)" covers "gh pr create --fill" and "shell(gh pr create)" does not. Warning: allowing "shell(gh pr)" also allows "gh pr merge" and "gh pr close" … shell rules cannot allow only "gh pr create"." The runs confirm it.

## Caveats for #803

- **Shell rules are coarser than the command.** `shell(gh pr)` also allows `gh pr merge` and `gh pr close`, and `shell(git push)` allows every push. The never-list check therefore has to run on the identifier, so that it judges everything the rule will allow, and not only on the command that raised the prompt. A prompt for `gh pr create` produces the rule `shell(gh pr)`, and that rule allows merges.
- **`shell(gh)` is not wider than `shell(gh pr)`.** For tools that Copilot names with their subcommand (git, gh, npm, cargo, go, …, per the help), the bare name matches only the bare command. The wide form is `shell(gh:*)`. Never generate it, because it covers `gh api` and `gh pr merge`.
- **Compound commands** (`a && b`, pipes) list one identifier per segment in `commands[]` and `commandSegments[]`, and each one needs its own rule. This run did not test it; it comes from the event shape.
- **Write paths.** The event gives an absolute `fileName`. A relative rule such as `write(notes.txt)` matches "by trailing path components … in any directory" (help), so `write(.env)` would match every `.env`. Use the path relative to the checkout only for files the never list allows. An absolute rule is tied to one checkout, and each ship has a different worktree path. `possiblePaths` on shell requests is a token guess (`["create"]` for `gh pr create --fill`, `["label","create","probe-label"]` for `gh label create …`), so it cannot be used to build rules.
- **URLs.** A shell command that reaches a URL raises a separate `kind: "url"` request after its `shell(...)` rule passes. `url(<host>)` covers it. The prompt also offers `parentDomainPattern: "https://*.example.com"`, which is wider, and network access is on the never list anyway.
- **The judge's approvals are logged as non-human.** When the judge approves, Copilot still writes `permission.requested`, with `promptRequest.assistedApproval.recommendation: "approve"`, and then `permission.completed` with `decisionSource: "assisted_approval"` (see `out/control-judge-approves`). A person's answer is `decisionSource: "human_response"` (the fixture `internal/conversation/testdata/copilot-server-stream.events.jsonl`). The "allowed by hand" filter keys on `decisionSource == "human_response"`.
- **Judge precedence.** `--help` says `--assisted-approval` "takes precedence over --allow-all-tools when the judge engages". With a specific rule, the judge never engaged in any run. No `permission.requested` was written and no judge evaluation was recorded. Specific rules therefore come before the judge, and that sentence is about allow-all only.
- **Interactive only.** `defaultPermissionMode` "is ignored … outside interactive runs" (`copilot help config`). In a `-p` run the equivalent is `--assisted-approval`, and a request that still needs a prompt is denied there. Ships are interactive, so these tests ran interactively.
- **Version.** Proven on 1.0.114 only. The rule grammar has been stable in `--help`, but the identifier rules (which tools get a subcommand) are Copilot's own list. Re-run `run-all.sh` after a Copilot update before relying on a new tool's identifier.
- **Testing gotcha.** On a Homebrew cask install, `--no-auto-update` runs the cask's bundled build (0.0.406 here) and not the auto-updated 1.0.114, so the scripts leave it off.

## How it was run

`run-case.sh` starts one interactive Copilot (`-i "<prompt>"`, model `gpt-5.6-luna`) in tmux, on a private socket. It uses a throwaway `COPILOT_HOME` that holds only a `settings.json` (`experimental: true`, `enabledFeatureFlags.AUTO_APPROVAL: true`, `defaultPermissionMode: "assisted"`) and a `config.json` that trusts a throwaway git repo with no remote. The login comes from the Keychain, and `~/.copilot` is never touched. The script asks the model to run one command, polls `session-state/*/events.jsonl`, and records one of these results:

- `prompted`: a `permission.requested` event with no `permission.completed`
- `ran-without-prompt`: the tool completed and the turn ended with no permission event
- `completed-without-person`: a `permission.completed` event that no person answered

The pane of every `prompted` case shows Copilot's approval dialog (`out/*/pane.txt`).

Left alone, the judge approved `gh pr create --fill` because the user asked for it (`out/control-judge-approves`). Every other case therefore sets `assistedApprovalInstructions` so the judge always asks ("Never recommend approve … always recommend asking the user"). With that set, a missing prompt can only come from a rule. Every control case shows `assistedApproval.recommendation: "requireApproval"`.

The commands are harmless: `gh pr create` and `gh label create` in a repo with no remote fail locally, `touch` and the file write land in the temp repo, and `curl -sI https://example.com` is one HEAD request.

```
run-all.sh            # gh, other, paths groups; writes out/<case>/
python3 -I summarize.py out
python3 -I trim.py out  # keeps only permission.* and tool.execution_* events
```

## Results

```
control-judge-approves: completed-without-person   (no judge note)
    requested kind=shell ids=['gh pr'] judge=approve
    completed result=approved source=assisted_approval
control-strict: prompted                            gh pr create --fill, no rule
    requested kind=shell ids=['gh pr'] paths=['create'] judge=requireApproval
allow-ghpr: ran-without-prompt                      --allow-tool 'shell(gh pr)'
allow-ghprcreate: prompted                          --allow-tool 'shell(gh pr create)'
    requested kind=shell ids=['gh pr'] judge=requireApproval
allow-gh-star: ran-without-prompt                   --allow-tool 'shell(gh:*)'
allow-gh-bare: prompted                             --allow-tool 'shell(gh)'
    requested kind=shell ids=['gh pr'] judge=requireApproval
control-touch: prompted                             touch marker.txt, no rule
    requested kind=shell ids=['touch'] judge=requireApproval
allow-touch: ran-without-prompt                     --allow-tool 'shell(touch)'
control-ghlabel: prompted                           gh label create probe-label, no rule
    requested kind=shell ids=['gh label'] judge=requireApproval
allow-ghlabel-other: prompted                       --allow-tool 'shell(gh pr)'
    requested kind=shell ids=['gh label'] judge=requireApproval
control-curl: prompted                              curl -sI https://example.com, --allow-tool 'shell(curl)' only
    requested kind=url url=https://example.com parentDomainPattern=https://*.example.com judge=requireApproval
allow-curl-url: ran-without-prompt                  --allow-tool 'shell(curl)' --allow-tool 'url(example.com)'
control-write: prompted                             create notes.txt with the edit tool, no rule
    requested kind=write fileName=<work>/notes.txt judge=requireApproval
allow-write-rel: ran-without-prompt                 --allow-tool 'write(notes.txt)'
```

A key event, from `out/control-strict/events.jsonl` (trimmed):

```json
{"type":"permission.requested","data":{"permissionRequest":{"kind":"shell","fullCommandText":"gh pr create --fill",
 "commands":[{"identifier":"gh pr","readOnly":false,"allowMatchIdentifier":null}], …},
 "promptRequest":{"kind":"commands","commandIdentifiers":["gh pr"],"canOfferSessionApproval":true,
 "assistedApproval":{"recommendation":"requireApproval","reason":"User instruction requires asking before approving any shell command.", …}}}}
```

In `out/allow-ghpr/events.jsonl` the same command shows only `tool.execution_start` and `tool.execution_complete`.

## Probe

No probe was registered in `internal/probes`. A probe there fills one capability ID in the capability store, and `--allow-tool` versus assisted mode is not yet a capability Admiral switches on. When the Copilot injection lands, a `copilot` probe can reuse `runCopilot` with `run-case.sh`'s settings. It would need an interactive session in tmux, because `defaultPermissionMode` is ignored under `-p`. A `-p --assisted-approval` run would be cheaper, with a rule-matched call that runs and an unmatched call that is denied. That run was not tested here.
