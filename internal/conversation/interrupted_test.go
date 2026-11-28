package conversation_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/conversation"
)

// A harness's own record of an Escape becomes one "interrupted" item; the line shapes are those Claude Code and Codex write.

func claudeInterruptLine(id, text string) string {
	return fmt.Sprintf(`{"type":"user","uuid":%q,"timestamp":"2026-11-12T10:00:03.000Z","message":{"role":"user","content":[{"type":"text","text":%q}]}}`+"\n", id, text)
}

const claudeTurn = `{"type":"user","uuid":"u1","timestamp":"2026-11-12T10:00:00.000Z","message":{"role":"user","content":"look into it"}}` + "\n" +
	`{"type":"assistant","uuid":"a1","timestamp":"2026-11-12T10:00:01.000Z","message":{"role":"assistant","content":[{"type":"text","text":"on it"}]}}` + "\n"

func kindStrings(items []conversation.Item) []string {
	var out []string
	for _, it := range items {
		out = append(out, string(it.Kind))
	}
	return out
}

func TestClaudeInterruptedLinesBecomeAnInterruptedItem(t *testing.T) {
	for _, raw := range []string{"[Request interrupted by user]", "[Request interrupted by user for tool use]", "[Request interrupted by user]\n"} {
		items := read(t, claudeTurn+claudeInterruptLine("i1", raw)+userLine(2))
		if got, want := kindStrings(items), []string{"user", "assistant", "interrupted", "user"}; fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("%q: kinds = %v, want %v", raw, got, want)
		}
		if got := items[2].Text; got != "Interrupted" {
			t.Errorf("%q: text = %q, want %q", raw, got, "Interrupted")
		}
		for _, it := range items {
			if it.Kind == conversation.KindUser && strings.Contains(it.Text, "Request interrupted") {
				t.Errorf("%q still shows as the engineer's message %q", raw, it.Text)
			}
		}
	}
}

func TestClaudeInterruptedLineWithPlainStringContentIsInterruptedToo(t *testing.T) {
	items := read(t, claudeTurn+`{"type":"user","uuid":"i2","message":{"role":"user","content":"[Request interrupted by user]"}}`+"\n")
	if got := kindStrings(items); fmt.Sprint(got) != fmt.Sprint([]string{"user", "assistant", "interrupted"}) {
		t.Errorf("kinds = %v, want user, assistant, interrupted", got)
	}
}

func TestClaudeNearMissesStayWhatTheyWereBefore(t *testing.T) {
	for _, text := range []string{
		"[Image #1] what is this?",
		"[Request interrupted by the server]",
		"please explain what [Request interrupted by user] means in a transcript",
	} {
		items := read(t, claudeInterruptLine("n1", text))
		if len(items) != 1 || items[0].Kind != conversation.KindUser || items[0].Text != text {
			t.Errorf("%q read as %+v, want one user item with the same text", text, items)
		}
	}
}

func TestTheChatsTextViewKeepsTheInterruptedItem(t *testing.T) {
	items := read(t, claudeTurn+claudeInterruptLine("i1", "[Request interrupted by user]"))
	if len(items) != 3 || items[2].Kind != "interrupted" || !conversation.TextOnly(items[2]) {
		t.Errorf("items %v: the text view must keep the interrupted item", kindStrings(items))
	}
}

func codexAborted(ordinal int, reason string) string {
	return fmt.Sprintf(`{"timestamp":"2026-11-12T10:00:0%d.000Z","ordinal":%d,"type":"event_msg","payload":{"type":"turn_aborted","root_turn_id":"turn-%d","turn_id":"turn-%d","reason":%q,"started_at":1795830229,"completed_at":1795830235,"duration_ms":5915}}`+"\n", ordinal, ordinal, ordinal, ordinal, reason)
}

const codexTurn = `{"timestamp":"2026-11-12T10:00:00.000Z","ordinal":1,"type":"event_msg","payload":{"type":"item_completed","turn_id":"turn-1","item":{"type":"UserMessage","id":"cu1","content":[{"type":"text","text":"look into it"}]}}}` + "\n" +
	`{"timestamp":"2026-11-12T10:00:01.000Z","ordinal":2,"type":"event_msg","payload":{"type":"item_completed","turn_id":"turn-1","item":{"type":"AgentMessage","id":"ca1","content":[{"type":"Text","text":"on it"}]}}}` + "\n"

func TestCodexTurnAbortedForInterruptedBecomesAnInterruptedItem(t *testing.T) {
	items := readCodex(t, codexTurn+codexAborted(3, "interrupted"))
	if got, want := kindStrings(items), []string{"user", "assistant", "interrupted"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("kinds = %v, want %v", got, want)
	}
	if items[2].Text != "Interrupted" || !conversation.TextOnly(items[2]) {
		t.Errorf("interrupted item = %+v, want text %q kept by the text view", items[2], "Interrupted")
	}
}

func TestCodexTurnAbortedForAnotherReasonAddsNothing(t *testing.T) {
	items := readCodex(t, codexTurn+codexAborted(3, "replaced"))
	if got, want := kindStrings(items), []string{"user", "assistant"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("kinds = %v, want %v", got, want)
	}
}
