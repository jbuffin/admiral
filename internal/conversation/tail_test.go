package conversation_test

import (
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/conversation"
)

func asst(text string) conversation.Item {
	return conversation.Item{Kind: conversation.KindAssistant, Text: text}
}

func tool(name, summary string) conversation.Item {
	return conversation.Item{Kind: conversation.KindToolCall, Tool: &conversation.ToolCall{Name: name, Summary: summary}}
}

func ask(text string, pending bool) conversation.Item {
	return conversation.Item{Kind: conversation.KindQuestion, Question: &conversation.Question{Text: text, Pending: pending}}
}

func TestTail(t *testing.T) {
	user := conversation.Item{Kind: conversation.KindUser, Text: "please fix it"}
	end := conversation.Item{Kind: conversation.KindTurnEnd}
	compaction := conversation.Item{Kind: conversation.KindCompaction, Text: "compacted"}
	notice := conversation.Item{Kind: conversation.KindNotice, Text: "a notice"}
	cases := []struct {
		name  string
		items []conversation.Item
		want  string
	}{
		{"a pending question is the tail", []conversation.Item{asst("Looking."), ask("Which database?", true)}, "Which database?"},
		{"the latest tool call gives its summary", []conversation.Item{asst("Looking."), tool("Bash", "go test ./...")}, "go test ./..."},
		{"assistant text gives its first non-empty line", []conversation.Item{tool("Bash", "ls"), asst("\n\nAll green.\nSecond line.")}, "All green."},
		{"an answered question does not win", []conversation.Item{asst("Done asking."), ask("Which database?", false)}, "Done asking."},
		{"a later tool call takes over a pending question", []conversation.Item{ask("Which database?", true), tool("Read", "main.go")}, "main.go"},
		{"a later assistant item takes over a pending question", []conversation.Item{ask("Which database?", true), asst("Carrying on.")}, "Carrying on."},
		{"a tool call with no summary shows its name", []conversation.Item{asst("Looking."), tool("TodoWrite", "")}, "TodoWrite"},
		{"other kinds are skipped", []conversation.Item{tool("Bash", "make check"), user, end, compaction, notice}, "make check"},
		{"nothing readable is blank", []conversation.Item{user, end, compaction, notice}, ""},
		{"no items is blank", nil, ""},
		{"control and bidi characters are stripped", []conversation.Item{asst("a\x1b[31mb\u202ec\u2066d\u0085e\tf")}, "a[31mbcdef"},
	}
	for _, c := range cases {
		if got := conversation.Tail(c.items); got != c.want {
			t.Errorf("%s: Tail = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestTailIsOneLineOfAtMost200Characters(t *testing.T) {
	long := strings.Repeat("é", 300)
	got := conversation.Tail([]conversation.Item{asst(long)})
	if n := len([]rune(got)); n != 200 {
		t.Errorf("Tail of a 300-character line has %d characters, want 200", n)
	}
	if !strings.HasPrefix(long, got) {
		t.Errorf("Tail = %q, want the start of the line cut on a character boundary", got)
	}
	if got := conversation.Tail([]conversation.Item{ask("first\nsecond", true)}); got != "first" {
		t.Errorf("Tail of a two-line question = %q, want %q", got, "first")
	}
}

func TestTailOfAClaudeTranscriptFollowsItsLatestItem(t *testing.T) {
	asked := `{"type":"assistant","uuid":"q1","message":{"content":[{"type":"tool_use","id":"tq","name":"AskUserQuestion","input":{"questions":[{"question":"Which database?","options":[{"label":"pg"}]}]}}]}}` + "\n"
	ran := `{"type":"assistant","uuid":"a1","message":{"content":[{"type":"tool_use","id":"tb","name":"Bash","input":{"description":"Run the tests","command":"go test"}}]}}` + "\n"
	if got := conversation.Tail(read(t, asked)); got != "Which database?" {
		t.Errorf("Tail with a pending question = %q, want %q", got, "Which database?")
	}
	if got := conversation.Tail(read(t, asked+ran)); got != "Run the tests" {
		t.Errorf("Tail after a later tool call = %q, want %q", got, "Run the tests")
	}
}
