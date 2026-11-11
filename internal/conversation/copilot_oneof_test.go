package conversation_test

// A Copilot single-choice ask_user, recorded from 1.0.109, offers its titles as options.

import (
	"os"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/conversation"
)

// Copilot 1.0.109 describes each choice as {const, title} under oneOf, not enum. Line 1 is the model's first try,
// whose requestedSchema is a JSON string: Copilot refused it (line 2, trimmed to the fields the decoder reads) and
// showed no form. Line 3 is the retry the pane showed.
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

func readLines(t *testing.T, lines string) []conversation.Item {
	t.Helper()
	items, err := conversation.ReadCopilot(strings.NewReader(lines))
	if err != nil {
		t.Fatal(err)
	}
	return items
}

func TestCopilotRefusedAskUserIsAFailedToolCallNotAQuestion(t *testing.T) {
	raw, err := os.ReadFile("testdata/copilot-ask-user-oneof.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	first := strings.SplitAfterN(string(raw), "\n", 3)
	items := readLines(t, first[0]+first[1])
	if len(items) != 1 || items[0].Kind == "question" || items[0].Tool == nil || !items[0].Tool.Error || !items[0].Tool.Done {
		t.Errorf("a refused ask_user = %+v, want one failed, finished tool call", items)
	}
}

func TestCopilotQuestionWhoseCompletionFailedSettlesAsAFailedToolCall(t *testing.T) {
	items := readLines(t, `{"type":"tool.execution_start","id":"s","timestamp":"2026-11-11T20:00:00Z","data":{"toolCallId":"c","toolName":"ask_user","arguments":{"message":"Pick","requestedSchema":{"type":"object","properties":{"a":{"type":"string","enum":["x","y"]}}}}}}
{"type":"tool.execution_complete","id":"e","timestamp":"2026-11-11T20:00:01Z","data":{"toolCallId":"c","success":false,"error":{"message":"boom"}}}
`)
	if len(items) != 1 || items[0].Kind == "question" || items[0].Tool == nil || !items[0].Tool.Error {
		t.Errorf("a failed ask_user = %+v, want a failed tool call", items)
	}
}

func TestCopilotSchemaWithEnumAndOneOfReadsOneOf(t *testing.T) {
	items := readLines(t, `{"type":"tool.execution_start","id":"s","timestamp":"2026-11-11T20:00:00Z","data":{"toolCallId":"c","toolName":"ask_user","arguments":{"message":"Pick","requestedSchema":{"type":"object","properties":{"a":{"type":"string","enum":["x","y"],"oneOf":[{"const":"x","title":"X"},{"const":"y","title":"Y"}]}}}}}}
`)
	if len(items) != 1 || items[0].Question == nil || len(items[0].Question.Options) != 2 {
		t.Errorf("enum plus oneOf = %+v, want two options", items)
	}
}
