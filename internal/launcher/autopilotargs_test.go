package launcher

import (
	"strings"
	"testing"
)

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
		{"model joined", "claude", nil, []string{"--model=opus"}},
		{"disallowedTools two values", "claude", nil, []string{"--disallowedTools", "Edit(/x/**)", "NotebookEdit(/x/**)"}},
		{"disallowed-tools alias", "claude", nil, []string{"--disallowed-tools", "Edit(/x/**)"}},
		{"disallowedTools then model", "claude", nil, []string{"--disallowedTools", "Edit(/x/**)", "--model", "opus"}},
		{"all together", "claude", nil, []string{"--model", "opus", "--advisor", "opus", "--effort", "high"}},
		{"mode equal to the dock's", "claude", auto, []string{"--permission-mode", "auto"}},
		{"mode stricter than an auto dock: plan", "claude", auto, []string{"--permission-mode", "plan"}},
		{"mode of a later dock value wins", "claude", []string{"--permission-mode", "bypassPermissions", "--permission-mode", "plan"}, []string{"--permission-mode", "plan"}},
		{"no dock mode: plan", "claude", nil, []string{"--permission-mode", "plan"}},
		{"no dock mode: manual", "claude", nil, []string{"--permission-mode", "manual"}},

		{"codex -m", "codex", nil, []string{"-m", "x"}},
		{"dock args of other flags are ignored", "claude", []string{"--model", "x"}, []string{"--permission-mode", "plan"}},
		{"disallowedTools when the dock sets other flags", "claude", []string{"--model", "x"}, []string{"--disallowedTools", "Edit(/x/**)"}},

		{"codex -s read-only", "codex", nil, []string{"-s", "read-only"}},
		{"codex -sread-only", "codex", nil, []string{"-sread-only"}},
		{"codex -c effort", "codex", nil, []string{"-c", "model_reasoning_effort=high"}},
		{"codex -c key with spaces and quotes", "codex", nil, []string{"-c", ` "model_reasoning_effort" =high`}},

		{"copilot --reasoning-effort", "copilot", nil, []string{"--reasoning-effort", "high"}},
		{"copilot --add-dir", "copilot", nil, []string{"--add-dir", "/x"}},
		{"copilot --available-tools several", "copilot", nil, []string{"--available-tools", "a", "b", "--model", "x"}},
		{"copilot --available-tools none", "copilot", nil, []string{"--available-tools"}},
		{"copilot --deny-tool several", "copilot", nil, []string{"--deny-tool", "shell(rm)", "write"}},
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
		{"add-dir is not on claude's list", "claude", nil, []string{"--add-dir", "/x"}, "--add-dir"},
		{"model with no value", "claude", nil, []string{"--model"}, "--model"},
		{"stray value", "claude", nil, []string{"opus"}, "opus"},
		{"stray value after a value flag", "claude", nil, []string{"--model", "opus", "extra"}, "extra"},
		{"dash-led token ends disallowedTools", "claude", nil, []string{"--disallowedTools", "Edit(/x/**)", "--dangerously-skip-permissions"}, "--dangerously-skip-permissions"},
		{"model takes no dash-led value", "claude", nil, []string{"--model", "--dangerously-skip-permissions"}, "--dangerously-skip-permissions"},
		{"model joined dash-led value", "claude", nil, []string{"--model=-x"}, "-x"},
		{"disallowedTools over a dock's list", "claude", []string{"--disallowedTools", "Bash"}, []string{"--disallowedTools", "Edit(/x/**)"}, "--disallowedTools"},
		{"mode default is not a claude mode", "claude", nil, []string{"--permission-mode", "default"}, "default"},
		{"auto dock: bypassPermissions", "claude", auto, []string{"--permission-mode", "bypassPermissions"}, "bypassPermissions"},
		{"plan dock: manual is looser", "claude", plan, []string{"--permission-mode", "manual"}, "manual"},
		{"no dock mode: auto", "claude", nil, []string{"--permission-mode", "auto"}, "auto"},
		{"unknown mode", "claude", auto, []string{"--permission-mode", "bogus"}, "bogus"},
		{"last dock mode is the baseline", "claude", []string{"--permission-mode", "plan", "--permission-mode", "auto"}, []string{"--permission-mode", "bypassPermissions"}, "bypassPermissions"},

		{"codex sandbox workspace-write", "codex", nil, []string{"--sandbox", "workspace-write"}, "workspace-write"},
		{"codex config other key", "codex", nil, []string{"-c", "model_reasoning_effort.x=1"}, "model_reasoning_effort.x=1"},
		{"codex config approval policy", "codex", nil, []string{"-c", "approval_policy=never"}, "approval_policy=never"},
		{"codex does not take claude's flags", "codex", nil, []string{"--effort", "high"}, "--effort"},

		{"copilot allow-all-tools", "copilot", nil, []string{"--allow-all-tools"}, "--allow-all-tools"},
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

// A joined value is named once in the refusal.
func TestAutopilotArgs_JoinedValueNamedOnce(t *testing.T) {
	err := AutopilotArgs("claude", nil, []string{"--permission-mode=bypassPermissions"})
	if err == nil || strings.Count(err.Error(), "bypassPermissions") != 1 {
		t.Errorf("error %v should name bypassPermissions once", err)
	}
}
