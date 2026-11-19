package launcher

import (
	"strings"
	"testing"
)

func TestEveryChatPromptSaysWhenToDraftAndWhichPathToTake(t *testing.T) {
	for _, harness := range []string{"claude", "codex", "copilot"} {
		prompt := chatPromptFor(harness)
		i := strings.Index(prompt, "get_drafting_skill")
		if i < 0 {
			t.Errorf("%s prompt never names get_drafting_skill", harness)
			continue
		}
		near := strings.ToLower(prompt[max(0, i-600):min(len(prompt), i+1000)])
		for _, want := range []string{
			"tells you to draft", // only on the engineer's word
			"names a skill",      // an engineer-named skill beats the fleet's
			"quick draft",        // the other path
			"draft_campaign",
			"installed nowhere", // the refusal to relay
			"another skill",
			"drafting", // launch_ship's flag
			"with you", // and what it means
			"link",
			"never change", // the fleet's drafting skill stays the engineer's
		} {
			if !strings.Contains(near, want) {
				t.Errorf("%s prompt near get_drafting_skill lacks %q: %q", harness, want, near)
			}
		}
	}
}

func TestEveryChatPromptKeepsWayfinderForChartingAnExistingCampaignAndDraftingForANewOne(t *testing.T) {
	for _, harness := range []string{"claude", "codex", "copilot"} {
		prompt := chatPromptFor(harness)
		if !strings.Contains(prompt, "You never create a campaign yourself") {
			t.Errorf("%s prompt lost the rule that AdmiralBot never creates a campaign", harness)
		}
		i := strings.Index(prompt, "lay out or chart a campaign")
		if i < 0 {
			t.Errorf("%s prompt lost the lay out or chart sentence", harness)
			continue
		}
		near := prompt[i:min(len(prompt), i+600)]
		if !strings.Contains(strings.ToLower(near), "existing") || !strings.Contains(near, "drafting") {
			t.Errorf("%s prompt beside the chart sentence does not tell charting an existing campaign from drafting a new one: %q", harness, near)
		}
	}
}
