package conversation_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/conversation"
)

const liveThread = "00000000-0000-4000-8000-0000000000a1"

// liveSample is a recorded app-server stream, sanitised: the thread/resume answer, then what the server pushed.
func liveSample(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile("testdata/codex-live-sample.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

func feed(live *conversation.Live, frames ...string) {
	for _, f := range frames {
		live.Apply([]byte(f))
	}
}

func TestCodexLiveSampleDecodesIntoItems(t *testing.T) {
	live := conversation.NewLive(conversation.Options{}, liveThread)
	feed(live, liveSample(t)...)
	items := live.Items()

	const u1, u2 = "00000000-0000-4000-8000-0000000000c1", "00000000-0000-4000-8000-0000000000c2"
	const e1, e2 = "exec-00000000-0000-4000-8000-0000000000e1", "exec-00000000-0000-4000-8000-0000000000e2"
	type want struct {
		id   string
		kind conversation.Kind
		text string
	}
	for i, w := range []want{
		{u1, "user", "Use the request_user_input tool right now to ask me exactly one single-choice question: \"Pick a colour\" with two options, Red and Blue. Make no other tool calls."},
		{"call_q1#0", "question", ""},
		{"msg_answer1", "assistant", "Blue selected."},
		{"00000000-0000-4000-8000-0000000000b1#end", "turn_end", ""},
		{u2, "user", "Do exactly two tool calls. 1) shell, escalated: mkdir -p /home/user/probe-files && echo hello > /home/user/probe-files/a.txt . 2) apply_patch adding /home/user/probe-files/b.txt with one line: hi."},
		{"msg_commentary2", "assistant", "I’ll run the two requested operations in order and won’t retry either if denied."},
		{e1, "tool_call", ""},
		{e2, "tool_call", ""},
		{"msg_final2", "assistant", "The escalated shell command succeeded. The patch call returned no error, but I did not verify `b.txt` because you requested exactly two tool calls."},
		{"00000000-0000-4000-8000-0000000000b2#end", "turn_end", ""},
	} {
		if i >= len(items) {
			t.Fatalf("%d items %v, want 10 (reasoning, hooks, status and token noise dropped)", len(items), kinds(items))
		}
		if got := items[i]; got.ID != w.id || got.Kind != w.kind || got.Text != w.text {
			t.Errorf("item %d = %s %s %q, want %s %s %q", i, got.ID, got.Kind, got.Text, w.id, w.kind, w.text)
		}
	}
	if len(items) != 10 {
		t.Fatalf("%d items %v, want 10", len(items), kinds(items))
	}

	if items[0].At != "2026-11-11T21:36:08.102Z" {
		t.Errorf("user item At = %q, want the item's startedAtMs as UTC", items[0].At)
	}
	if q := items[1].Question; q == nil || q.Text != "Pick a colour" || q.Pending || q.MultiSelect || len(q.Options) != 2 ||
		q.Options[0].Label != "Red (Recommended)" || q.Options[0].Description != "Choose red." || q.Options[1].Label != "Blue" {
		t.Errorf("question = %+v, want the two options, answered by serverRequest/resolved so no longer pending", items[1].Question)
	}
	if items[3].Duration != 29530*time.Millisecond || items[9].Duration != 40562*time.Millisecond {
		t.Errorf("turn ends took %v and %v, want durationMs of each turn", items[3].Duration, items[9].Duration)
	}

	sh, patch := items[6].Tool, items[7].Tool
	if sh == nil || sh.Name != "Bash" || sh.Input != "/bin/zsh -lc 'mkdir -p /home/user/probe-files && echo hello > /home/user/probe-files/a.txt'" || !sh.Done || sh.Error {
		t.Errorf("command = %+v, want Bash with the command the server runs, done", sh)
	}
	if patch == nil || patch.Name != "apply_patch" || patch.Input != "/home/user/probe-files/b.txt" || !patch.Done || patch.Error {
		t.Errorf("file change = %+v, want apply_patch with the patch's files, one per line, done", patch)
	}
	if strings.Contains(patch.Summary+patch.Input, "hi") && strings.Contains(patch.Input, "\n+") {
		t.Errorf("file change input %q holds the diff; it approves files, not their text", patch.Input)
	}
}

func TestCodexLiveCardsNeedOnlyWhatTheServerSaid(t *testing.T) {
	sample := liveSample(t)
	const requestUserInput, resolved, commandStarted, commandApproval, commandDone = 7, 8, 24, 25, 28
	live := conversation.NewLive(conversation.Options{}, liveThread)

	feed(live, sample[:requestUserInput+1]...)
	pending := live.Pending()
	if len(pending) != 1 || pending[0].ID != "call_q1#0" || !pending[0].Question.Pending || len(pending[0].Question.Options) != 2 {
		t.Fatalf("pending = %+v, want the colour question with its two options", pending)
	}
	feed(live, sample[resolved])
	if got := live.Pending(); len(got) != 0 {
		t.Errorf("pending after serverRequest/resolved = %+v, want none", got)
	}

	feed(live, sample[resolved+1:commandStarted+1]...)
	last := live.Items()[len(live.Items())-1]
	if last.Tool == nil || last.Tool.Name != "Bash" || last.Tool.Done || last.Tool.Input == "" {
		t.Errorf("while the approval is open the command is %+v, want a Bash call not yet done with its command", last)
	}
	feed(live, sample[commandApproval:commandDone+1]...)
	for _, it := range live.Items() {
		if it.Tool != nil && it.Tool.Name == "Bash" && !it.Tool.Done {
			t.Errorf("the command is still open after item/completed: %+v", it)
		}
	}
}

func TestCodexLiveAnotherThreadIsNotThisConversation(t *testing.T) {
	live := conversation.NewLive(conversation.Options{}, liveThread)
	feed(live,
		`{"method":"item/completed","params":{"item":{"type":"agentMessage","id":"msg_other","text":"not ours","phase":"final_answer"},"threadId":"00000000-0000-4000-8000-0000000000f9","turnId":"t"}}`,
		`{"method":"item/tool/requestUserInput","id":3,"params":{"threadId":"00000000-0000-4000-8000-0000000000f9","turnId":"t","itemId":"call_other","questions":[{"id":"q","header":"h","question":"not ours","isSecret":false,"options":[{"label":"a","description":""}]}]}}`,
	)
	if got := live.Items(); len(got) != 0 {
		t.Errorf("items = %+v, want none from another thread", got)
	}
	if got := live.Pending(); len(got) != 0 {
		t.Errorf("pending = %+v, want none from another thread", got)
	}
}

func TestCodexLiveQuestionStopsBeingPendingWhenTheTurnMovesOn(t *testing.T) {
	live := conversation.NewLive(conversation.Options{}, liveThread)
	sample := liveSample(t)
	feed(live, sample[:8]...)
	if len(live.Pending()) != 1 {
		t.Fatalf("pending = %+v, want the question", live.Pending())
	}
	// The TUI's Esc ends the turn with no serverRequest/resolved the observer sees first.
	feed(live, `{"method":"turn/completed","params":{"threadId":"`+liveThread+`","turn":{"id":"00000000-0000-4000-8000-0000000000b1","items":[],"status":"interrupted","durationMs":1000}}}`)
	if got := live.Pending(); len(got) != 0 {
		t.Errorf("pending after turn/completed = %+v, want none", got)
	}
}

func TestCodexLiveOnlyASingleQuestionWithOptionsMakesACard(t *testing.T) {
	ask := func(questions string) []conversation.Item {
		live := conversation.NewLive(conversation.Options{}, liveThread)
		feed(live, `{"method":"item/tool/requestUserInput","id":5,"params":{"threadId":"`+liveThread+`","turnId":"t","itemId":"call_x","questions":[`+questions+`]}}`)
		return live.Pending()
	}
	one := `{"id":"a","header":"A","question":"First?","isSecret":false,"options":[{"label":"Yes","description":""},{"label":"No","description":""}]}`
	two := `{"id":"b","header":"B","question":"Second?","isSecret":false,"options":[{"label":"Up","description":""}]}`

	if got := ask(one + "," + two); len(got) != 2 || got[0].ID != "call_x#0" || got[1].ID != "call_x#1" {
		t.Errorf("two questions = %+v, want items call_x#0 and call_x#1 (they share the call, so the route sends them to the terminal)", got)
	}
	if got := ask(`{"id":"s","header":"S","question":"Token?","isSecret":true,"options":[{"label":"a","description":""},{"label":"b","description":""}]}`); len(got) != 1 || len(got[0].Question.Options) != 0 {
		t.Errorf("a secret question = %+v, want a pending item with no options, so no card", got)
	}
	if got := ask(`{"id":"f","header":"F","question":"Free text?","isSecret":false,"options":null}`); len(got) != 1 || len(got[0].Question.Options) != 0 {
		t.Errorf("a question with no options = %+v, want a pending item with no options, so no card", got)
	}
}

func TestCodexLiveSkipsWhatItCannotRead(t *testing.T) {
	live := conversation.NewLive(conversation.Options{}, liveThread)
	feed(live, "", "not json", `{"method":"item/completed","params":"x"}`, `{"id":7}`, `{"method":"item/completed","params":{"item":{"type":"somethingNew","id":"n"},"threadId":"`+liveThread+`"}}`)
	if got := live.Items(); len(got) != 0 {
		t.Errorf("items = %+v, want none from frames that are not conversation", got)
	}
}
