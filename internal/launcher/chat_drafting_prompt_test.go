package launcher

import (
	"strings"
	"testing"
)

func TestEveryChatPromptSaysWhenToDraftAndWhichPathToTake(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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

// sentenceAround is the sentence of s holding the byte at i, cut at ". ".
func sentenceAround(s string, i int) string {
	start := strings.LastIndex(s[:i], ". ") + 1
	end := len(s)
	if k := strings.Index(s[i:], ". "); k >= 0 {
		end = i + k + 1
	}
	return strings.TrimSpace(s[start:end])
}

func TestEveryChatPromptSendsEachDraftRequestToOnePathNotTwo(t *testing.T) {
	t.Parallel()
	for _, harness := range []string{"claude", "codex", "copilot"} {
		prompt := chatPromptFor(harness)
		for i, from := 0, 0; ; from = i + 1 {
			k := strings.Index(prompt[from:], "draft_campaign")
			if k < 0 {
				break
			}
			i = from + k
			if s := sentenceAround(prompt, i); !strings.Contains(s, "quick draft") {
				t.Errorf("%s prompt sends drafting to draft_campaign without tying it to a quick draft: %q", harness, s)
			}
		}
		n := strings.Index(prompt, "names a skill")
		if n < 0 {
			t.Errorf("%s prompt never says what a named skill does", harness)
			continue
		}
		if s := sentenceAround(prompt, n); !strings.Contains(s, "launch_ship") || !strings.Contains(s, "drafting") || strings.Contains(s, "draft_campaign") {
			t.Errorf("%s prompt does not send a named skill to launch_ship with drafting: %q", harness, s)
		}
		d := strings.Index(prompt, "get_drafting_skill")
		if s := sentenceAround(prompt, d); !strings.Contains(s, "launch_ship") || !strings.Contains(s, "drafting") {
			t.Errorf("%s prompt reads the fleet's drafting skill but does not launch the drafting ship with it: %q", harness, s)
		}
	}
}
