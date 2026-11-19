package launcher

import (
	"strings"
	"testing"
)

func TestEveryChatPromptSaysADoneShipInALocalOnlyCheckoutHasACommitOnItsBranchNotAPR(t *testing.T) {
	for _, harness := range []string{"claude", "codex", "copilot"} {
		prompt := chatPromptFor(harness)
		if !strings.Contains(prompt, "the report result is done and a PR exists") {
			t.Errorf("%s prompt lost the rule that a GitHub ship is done only with a PR", harness)
		}
		i := strings.Index(strings.ToLower(prompt), "local-only")
		if i < 0 {
			t.Errorf("%s prompt never speaks of a local-only checkout", harness)
			continue
		}
		s := sentenceAround(prompt, i)
		for _, want := range []string{"commit", "branch", "PR"} {
			if !strings.Contains(s, want) {
				t.Errorf("%s prompt's local-only sentence = %q, want it to say %q", harness, s, want)
			}
		}
	}
}
