package launcher

import (
	"strings"
	"testing"
)

// Claude Code refuses a heredoc whose body has both { and " under dontAsk, so the
// chat's allow rules never match it. The skill's chat ask example must stay clear
// of that pair (the line form in docs/cli.md), whatever else changes.
func TestSkillAskExampleSurvivesDontAsk(t *testing.T) {
	_, after, ok := strings.Cut(string(skillText), "admiral chat ask <<'EOF'\n")
	if !ok {
		t.Fatal("the skill has no chat ask example")
	}
	body, _, _ := strings.Cut(after, "\nEOF")
	if strings.Contains(body, "{") && strings.Contains(body, `"`) {
		t.Errorf("the example body has both { and \":\n%s", body)
	}
}
