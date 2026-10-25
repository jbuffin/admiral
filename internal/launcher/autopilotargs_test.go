package launcher

import (
	"strings"
	"testing"
)

// AutopilotArgs is the per-harness allowlist for a caller with ADMIRAL_AUTOPILOT=1
// (#175): only the engineer's listed flags pass, and --permission-mode only when
// it is no looser than the dock's.
func TestAutopilotArgs(t *testing.T) {
	auto := []string{"--permission-mode", "auto"}
	plan := []string{"--permission-mode=plan"}

	passes := []struct {
		name    string
		harness string
		dock    []string
		args    []string
	}{
		{"empty", "claude", nil, nil},
		{"model spaced", "claude", nil, []string{"--model", "opus"}},
		{"model joined", "claude", nil, []string{"--model=opus"}},
		{"model takes a dash-led value", "claude", nil, []string{"--model", "--dangerously-skip-permissions"}},
		{"advisor spaced", "claude", nil, []string{"--advisor", "opus"}},
		{"advisor joined", "claude", nil, []string{"--advisor=opus"}},
		{"effort spaced", "claude", nil, []string{"--effort", "high"}},
		{"effort joined", "claude", nil, []string{"--effort=high"}},
		{"disallowedTools two values", "claude", nil, []string{"--disallowedTools", "Edit(/x/**)", "NotebookEdit(/x/**)"}},
		{"disallowedTools joined", "claude", nil, []string{"--disallowedTools=Edit(/x/**)"}},
		{"disallowedTools then model", "claude", nil, []string{"--disallowedTools", "Edit(/x/**)", "--model", "opus"}},
		{"all together", "claude", nil, []string{"--model", "opus", "--advisor", "opus", "--effort", "high"}},
		{"mode equal to the dock's", "claude", auto, []string{"--permission-mode", "auto"}},
		{"mode stricter than an auto dock: plan", "claude", auto, []string{"--permission-mode", "plan"}},
		{"mode stricter than an auto dock: default", "claude", auto, []string{"--permission-mode=default"}},
		{"mode equal to a joined dock's", "claude", plan, []string{"--permission-mode", "plan"}},
		{"mode of a later dock value wins", "claude", []string{"--permission-mode", "bypassPermissions", "--permission-mode", "plan"}, []string{"--permission-mode", "plan"}},
		{"no dock mode: plan", "claude", nil, []string{"--permission-mode", "plan"}},
		{"no dock mode: dontAsk", "claude", nil, []string{"--permission-mode", "dontAsk"}},
		{"no dock mode: default", "claude", nil, []string{"--permission-mode", "default"}},
		{"no dock mode: manual", "claude", nil, []string{"--permission-mode", "manual"}},
		{"dock args of other flags are ignored", "claude", []string{"--model", "x"}, []string{"--permission-mode", "default"}},

		{"codex -m", "codex", nil, []string{"-m", "x"}},
		{"codex --model", "codex", nil, []string{"--model", "x"}},
		{"codex --model joined", "codex", nil, []string{"--model=x"}},
		{"codex -s read-only", "codex", nil, []string{"-s", "read-only"}},
		{"codex --sandbox read-only", "codex", nil, []string{"--sandbox", "read-only"}},
		{"codex --sandbox= read-only", "codex", nil, []string{"--sandbox=read-only"}},
		{"codex -c effort", "codex", nil, []string{"-c", "model_reasoning_effort=high"}},
		{"codex --config effort", "codex", nil, []string{"--config", "model_reasoning_effort=high"}},
		{"codex --config= effort", "codex", nil, []string{"--config=model_reasoning_effort=high"}},
		{"codex -c<kv> effort", "codex", nil, []string{"-cmodel_reasoning_effort=high"}},
		{"codex -c=<kv> effort", "codex", nil, []string{"-c=model_reasoning_effort=high"}},
		{"codex -c key with spaces and quotes", "codex", nil, []string{"-c", ` "model_reasoning_effort" =high`}},

		{"copilot --model", "copilot", nil, []string{"--model", "x"}},
		{"copilot --reasoning-effort", "copilot", nil, []string{"--reasoning-effort", "high"}},
		{"copilot --add-dir", "copilot", nil, []string{"--add-dir", "/x"}},
		{"copilot --available-tools several", "copilot", nil, []string{"--available-tools", "a", "b", "--model", "x"}},
		{"copilot --available-tools none", "copilot", nil, []string{"--available-tools"}},
		{"copilot --deny-tool several", "copilot", nil, []string{"--deny-tool", "shell(rm)", "write"}},
		{"copilot --deny-tool joined", "copilot", nil, []string{"--deny-tool=shell(rm)"}},
	}
	for _, tc := range passes {
		if err := AutopilotArgs(tc.harness, tc.dock, tc.args); err != nil {
			t.Errorf("%s: %s %q (dock %q) refused: %v", tc.name, tc.harness, tc.args, tc.dock, err)
		}
	}

	refused := []struct {
		name    string
		harness string
		dock    []string
		args    []string
		value   string // the text the error must carry
	}{
		{"unknown flag", "claude", nil, []string{"--dangerously-skip-permissions"}, "--dangerously-skip-permissions"},
		{"unknown flag after a good one", "claude", nil, []string{"--model", "opus", "--dangerously-skip-permissions"}, "--dangerously-skip-permissions"},
		{"add-dir is not on claude's list", "claude", nil, []string{"--add-dir", "/x"}, "--add-dir"},
		{"model with no value", "claude", nil, []string{"--model"}, "--model"},
		{"effort with no value", "claude", nil, []string{"--effort"}, "--effort"},
		{"stray value", "claude", nil, []string{"opus"}, "opus"},
		{"stray value after a value flag", "claude", nil, []string{"--model", "opus", "extra"}, "extra"},
		{"disallowedTools with no value", "claude", nil, []string{"--disallowedTools"}, "--disallowedTools"},
		{"dash-led token ends disallowedTools", "claude", nil, []string{"--disallowedTools", "Edit(/x/**)", "--dangerously-skip-permissions"}, "--dangerously-skip-permissions"},
		{"auto dock: bypassPermissions", "claude", auto, []string{"--permission-mode", "bypassPermissions"}, "bypassPermissions"},
		{"plan dock: default is looser", "claude", plan, []string{"--permission-mode", "default"}, "default"},
		{"plan dock: joined default", "claude", plan, []string{"--permission-mode=default"}, "default"},
		{"no dock mode: acceptEdits", "claude", nil, []string{"--permission-mode", "acceptEdits"}, "acceptEdits"},
		{"no dock mode: auto", "claude", nil, []string{"--permission-mode", "auto"}, "auto"},
		{"unknown mode", "claude", auto, []string{"--permission-mode", "bogus"}, "bogus"},
		{"mode with no value", "claude", nil, []string{"--permission-mode"}, "--permission-mode"},
		{"last dock mode is the baseline", "claude", []string{"--permission-mode", "plan", "--permission-mode", "auto"}, []string{"--permission-mode", "bypassPermissions"}, "bypassPermissions"},

		{"codex sandbox workspace-write", "codex", nil, []string{"--sandbox", "workspace-write"}, "workspace-write"},
		{"codex -s danger-full-access", "codex", nil, []string{"-s", "danger-full-access"}, "danger-full-access"},
		{"codex --sandbox= workspace-write", "codex", nil, []string{"--sandbox=workspace-write"}, "workspace-write"},
		{"codex config other key", "codex", nil, []string{"-c", "model_reasoning_effort.x=1"}, "model_reasoning_effort.x=1"},
		{"codex config approval policy", "codex", nil, []string{"-c", "approval_policy=never"}, "approval_policy=never"},
		{"codex bypass flag", "codex", nil, []string{"--dangerously-bypass-approvals-and-sandbox"}, "--dangerously-bypass-approvals-and-sandbox"},
		{"codex -m with no value", "codex", nil, []string{"-m"}, "-m"},
		{"codex does not take claude's flags", "codex", nil, []string{"--effort", "high"}, "--effort"},

		{"copilot allow-all-tools", "copilot", nil, []string{"--allow-all-tools"}, "--allow-all-tools"},
		{"copilot does not take claude's flags", "copilot", nil, []string{"--advisor", "x"}, "--advisor"},
		{"copilot stray value", "copilot", nil, []string{"--model", "x", "stray"}, "stray"},
	}
	for _, tc := range refused {
		err := AutopilotArgs(tc.harness, tc.dock, tc.args)
		if err == nil {
			t.Errorf("%s: %s %q (dock %q) not refused", tc.name, tc.harness, tc.args, tc.dock)
		} else if !strings.Contains(err.Error(), tc.value) {
			t.Errorf("%s: error %q does not name %q", tc.name, err, tc.value)
		}
	}
}
