package e2e

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// AdmiralBot records a remind commitment on a PR; the card comes when GitHub says the PR merged, with no chat turn.
func TestAdmiralBot_PRMergedCommitmentFiresAsACardWhenGitHubSaysMerged(t *testing.T) {
	t.Parallel()
	const pr = "https://github.com/acme/widgets/pull/12"
	const promise = "tell me the widgets PR landed"
	var g *fakeGH
	c := newBotWorld(t, botOpts{sources: func(w *world) string {
		g = w.newFakeGH()
		g.token("", "fake-token-chat")
		return rawSourcesIn(chatFleet, srcDef{Key: "gh", Plugin: "github", Name: "GitHub", Settings: map[string]any{"views": []any{}}})
	}})
	w := c.world
	w.extra = append(w.extra, "ADMIRAL_COMMITMENT_GH_POLL=300ms")
	g.prState(pr, "OPEN")
	w.writeCue("mcp promise")
	w.start()
	c.start()

	r := c.mcpCall("promise", "make_commitment", map[string]any{"text": promise, "trigger": "pr_merged", "subject": pr, "action": "remind"})
	if r.IsError {
		t.Fatalf("make_commitment was refused: %s", r.text())
	}
	cardOf := func() *chatMsg {
		for _, m := range c.session().Messages {
			if m.Kind == "commitment" && strings.Contains(m.Text, promise) {
				return &m
			}
		}
		return nil
	}
	waitCond(t, 15*time.Second, "Admiral asked gh about the PR", func() bool { return g.asked(pr) })
	holdThat(t, "a card appeared while the PR is open", func() bool { return cardOf() == nil })

	typed := c.stdin()
	g.prState(pr, "MERGED")
	waitCond(t, 15*time.Second, "the card for the merged PR", func() bool { return cardOf() != nil })
	var card struct {
		Outcome string `json:"outcome"`
	}
	if m := cardOf(); json.Unmarshal(m.Card, &card) != nil || card.Outcome != "reminded" {
		t.Errorf("card = %s, want outcome reminded", m.Card)
	}
	if got := c.stdin(); got != typed {
		t.Errorf("the chat was sent %q after the card, want %q: a fired commitment starts no turn", got, typed)
	}
}
