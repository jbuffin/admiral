package conversation_test

// A Copilot single-choice ask_user, recorded from 1.0.109, offers its titles as options.

import (
	"os"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/conversation"
)

// Copilot 1.0.109 describes each choice as {const, title} under oneOf, not enum. Line 1 is the model's first try,
// whose requestedSchema is a JSON string; line 2 is the retry the pane showed.
func TestCopilotOneOfSingleChoiceBecomesOptions(t *testing.T) {
	raw, err := os.ReadFile("testdata/copilot-ask-user-oneof.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	items, err := conversation.ReadCopilot(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	var newest *conversation.Question
	for _, it := range items {
		if it.Kind == "question" && it.Question != nil && it.Question.Pending {
			newest = it.Question
		}
	}
	if newest == nil {
		t.Fatalf("no pending question in %d items", len(items))
	}
	var got []string
	for _, o := range newest.Options {
		got = append(got, o.Label)
	}
	if strings.Join(got, ",") != "Red,Green,Blue" {
		t.Errorf("the newest pending question's options = %v, want [Red Green Blue]", got)
	}
}
