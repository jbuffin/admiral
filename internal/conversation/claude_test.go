package conversation_test

// The Claude Code adapter test (issue #264, ADR 0030): one recorded transcript,
// read into the harness-neutral model. testdata/claude-sample.jsonl is a real
// Claude Code 2.1.322 session recorded for this test (scrubbed of paths, account
// ids and the engineer's context): a text answer, a Bash call, an AskUserQuestion
// the engineer answered, a background Agent run, /compact and a second
// AskUserQuestion that was answered too. Claude Code does not write a question's
// tool_use line to the file until the question is resolved, so the pending
// question is the same recording cut just before its answer.
//
// The API under test is assumed, not yet written:
//
//	ReadClaudeCode(io.Reader) ([]Item, error)   // ErrUnreadable when no line parses
//	Item{ID, Kind, Text, Tool, Question, Duration}
//	Kind: KindUser, KindAssistant, KindToolCall, KindQuestion, KindTurnEnd, KindCompaction
//	Tool{Name, Output}
//	Question{Text, Options []Option{Label, Description}, Answer, Pending}

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/conversation"
)

const samplePath = "testdata/claude-sample.jsonl"

// sampleLinePendingQuestion is the 1-based line of the second AskUserQuestion's
// tool_use in the sample; its tool_result is the next line.
const sampleLinePendingQuestion = 97

func readSample(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(samplePath)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func read(t *testing.T, data []byte) []conversation.Item {
	t.Helper()
	items, err := conversation.ReadClaudeCode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("ReadClaudeCode: %v", err)
	}
	return items
}

// itemText is every string an item carries, to look for what must not leak.
func itemText(it conversation.Item) string {
	s := it.Text
	if it.Tool != nil {
		s += "\n" + it.Tool.Name + "\n" + it.Tool.Output
	}
	if it.Question != nil {
		s += "\n" + it.Question.Text + "\n" + it.Question.Answer
		for _, o := range it.Question.Options {
			s += "\n" + o.Label + "\n" + o.Description
		}
	}
	return s
}

func TestClaudeCodeSample(t *testing.T) {
	data := readSample(t)
	items := read(t, data)

	// Claude Code writes the subagent's completion as a <task-notification> user
	// line that isn't the engineer's. Whether it is an item is open (see the test
	// writer's report), so the sequence is checked without it.
	var seq []conversation.Item
	for _, it := range items {
		if it.Kind == conversation.KindUser && strings.HasPrefix(strings.TrimSpace(it.Text), "<task-notification>") {
			continue
		}
		seq = append(seq, it)
	}

	want := []conversation.Kind{
		conversation.KindUser, conversation.KindAssistant, conversation.KindTurnEnd, // hello
		conversation.KindUser, conversation.KindToolCall, conversation.KindAssistant, conversation.KindTurnEnd, // Bash ls -la
		conversation.KindUser, conversation.KindQuestion, conversation.KindAssistant, conversation.KindTurnEnd, // answered question
		conversation.KindUser, conversation.KindToolCall, conversation.KindAssistant, conversation.KindTurnEnd, // Agent, launched
		conversation.KindAssistant, conversation.KindTurnEnd, // after the subagent's hand-back
		conversation.KindCompaction,
		conversation.KindUser, conversation.KindQuestion, conversation.KindAssistant, conversation.KindTurnEnd, // second question, answered
	}
	var got []conversation.Kind
	for _, it := range seq {
		got = append(got, it.Kind)
	}
	if len(got) != len(want) {
		t.Fatalf("kinds = %v\nwant    %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("kind[%d] = %v, want %v\nkinds = %v\nwant    %v", i, got[i], want[i], got, want)
		}
	}

	// Plain text.
	if seq[0].Text != "Say hello in one short sentence." || seq[1].Text != "Hello, nice to meet you!" {
		t.Errorf("first turn = %q / %q", seq[0].Text, seq[1].Text)
	}

	// A tool call has its output folded in, and the call and its result are one item.
	bash := seq[4]
	if bash.Tool == nil || bash.Tool.Name != "Bash" || !strings.Contains(bash.Tool.Output, "total 0") {
		t.Errorf("Bash tool call = %+v", bash.Tool)
	}
	// A background subagent is one tool-call item too; its output is the launch
	// message, since the run's own report comes back as a separate user line.
	agent := seq[12]
	if agent.Tool == nil || agent.Tool.Name != "Agent" || !strings.Contains(agent.Tool.Output, "Async agent launched") {
		t.Errorf("Agent tool call = %+v", agent.Tool)
	}

	// An answered question keeps its options and says what was answered. It isn't a tool call.
	q1 := seq[8].Question
	if q1 == nil || q1.Text != "Which colour do you prefer: red or blue?" || q1.Pending || !strings.Contains(q1.Answer, "Red") {
		t.Fatalf("answered question = %+v", q1)
	}
	if len(q1.Options) != 2 || q1.Options[0].Label != "Red" || q1.Options[0].Description != "You prefer red." || q1.Options[1].Label != "Blue" {
		t.Errorf("answered question options = %+v", q1.Options)
	}
	if q2 := seq[19].Question; q2 == nil || q2.Pending || !strings.Contains(q2.Answer, "Cat") {
		t.Errorf("second answered question = %+v", q2)
	}

	// Turn boundaries carry "worked for N" from the turn_duration lines.
	var durations []time.Duration
	for _, it := range seq {
		if it.Kind == conversation.KindTurnEnd {
			durations = append(durations, it.Duration)
		}
	}
	wantDur := []time.Duration{1287 * time.Millisecond, 4161 * time.Millisecond, 2177 * time.Millisecond, 3436 * time.Millisecond, 1018 * time.Millisecond, 303195 * time.Millisecond}
	if len(durations) != len(wantDur) {
		t.Fatalf("turn durations = %v, want %v", durations, wantDur)
	}
	for i := range wantDur {
		if durations[i] != wantDur[i] {
			t.Errorf("turn %d worked for %v, want %v", i, durations[i], wantDur[i])
		}
	}

	// Nothing from thinking, meta, the compaction summary or the /compact command
	// lines leaks through as text, and the marker stands in for all of them.
	for i, it := range items {
		text := itemText(it)
		for _, leak := range []string{
			"CAQS",                             // a thinking block's signature
			"This session is being continued",  // the compaction summary
			"<command-name>", "<local-command", // the /compact command lines
			"<system-reminder>", "Another Claude session", // meta user lines
		} {
			if strings.Contains(text, leak) {
				t.Errorf("item %d (%s) leaks %q: %q", i, it.Kind, leak, text)
			}
		}
	}

	// Ids are set, unique, and the same on a second read: u2 pages by them.
	seen := map[string]bool{}
	for i, it := range items {
		if it.ID == "" || seen[it.ID] {
			t.Errorf("item %d (%s) has id %q (empty or repeated)", i, it.Kind, it.ID)
		}
		seen[it.ID] = true
	}
	again := read(t, data)
	if len(again) != len(items) {
		t.Fatalf("second read gave %d items, first gave %d", len(again), len(items))
	}
	for i := range items {
		if again[i].ID != items[i].ID {
			t.Errorf("item %d id changed between reads: %q then %q", i, items[i].ID, again[i].ID)
		}
	}
}

func TestClaudeCodePendingQuestion(t *testing.T) {
	lines := bytes.SplitAfter(readSample(t), []byte("\n"))
	items := read(t, bytes.Join(lines[:sampleLinePendingQuestion], nil))

	if len(items) < 2 {
		t.Fatalf("got %d items", len(items))
	}
	if prompt := items[len(items)-2]; prompt.Kind != conversation.KindUser || !strings.Contains(prompt.Text, "which animal I prefer") {
		t.Errorf("item before the question = %s %q", prompt.Kind, prompt.Text)
	}
	last := items[len(items)-1]
	if last.Kind != conversation.KindQuestion || last.Question == nil {
		t.Fatalf("last item = %s, want a question", last.Kind)
	}
	q := last.Question
	if !q.Pending || q.Answer != "" || q.Text != "Which animal do you prefer: cat or dog?" {
		t.Errorf("pending question = %+v", q)
	}
	if len(q.Options) != 2 || q.Options[0].Label != "Cat" || q.Options[0].Description != "You prefer cats." || q.Options[1].Label != "Dog" {
		t.Errorf("pending question options = %+v", q.Options)
	}
}

// Format drift (ADR 0030): what the adapter doesn't know it skips, and a file
// with nothing it can read is told apart from a missing one.
func TestClaudeCodeSkipsWhatItDoesNotKnow(t *testing.T) {
	input := strings.Join([]string{
		`{"type":"user","uuid":"u1","message":{"role":"user","content":"hello"}}`,
		`{"type":"a-line-type-from-the-future","uuid":"x1","payload":{"a":1}}`,
		`{not json at all`,
		`{"type":"assistant","isSidechain":true,"uuid":"s1","message":{"content":[{"type":"text","text":"from a subagent file"}]}}`,
		`{"type":"assistant","uuid":"a1","message":{"content":[{"type":"text","text":"hi"}]}}`,
	}, "\n") + "\n"
	items := read(t, []byte(input))
	if len(items) != 2 || items[0].Kind != conversation.KindUser || items[0].Text != "hello" ||
		items[1].Kind != conversation.KindAssistant || items[1].Text != "hi" {
		t.Errorf("items = %+v, want the user line then the assistant line", items)
	}

	_, err := conversation.ReadClaudeCode(strings.NewReader("{not json\nplain words\n"))
	if !errors.Is(err, conversation.ErrUnreadable) {
		t.Errorf("a file with nothing parseable: err = %v, want conversation.ErrUnreadable", err)
	}
}

// A ship before its first prompt has only metadata lines: that is an empty
// conversation, not a file that can't be read.
func TestClaudeCodeMetadataOnlyIsEmpty(t *testing.T) {
	input := `{"type":"mode","mode":"normal","sessionId":"s"}` + "\n" + `{"type":"permission-mode","permissionMode":"auto"}` + "\n"
	items, err := conversation.ReadClaudeCode(strings.NewReader(input))
	if err != nil || len(items) != 0 {
		t.Errorf("items = %v, err = %v, want an empty conversation", items, err)
	}
}

// The reader follows a file by offset: a tool result written after the call was
// read folds into the call, under the same id, and a missing file says so.
func TestReaderFoldsALaterResult(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.jsonl")
	call := `{"type":"assistant","uuid":"a1","message":{"content":[{"type":"tool_use","id":"tu1","name":"Bash","input":{"command":"ls"}}]}}` + "\n"
	result := `{"type":"user","uuid":"u2","message":{"content":[{"type":"tool_result","tool_use_id":"tu1","content":"file.txt"}]}}` + "\n"
	if err := os.WriteFile(path, []byte(call), 0o600); err != nil {
		t.Fatal(err)
	}
	r := conversation.NewReader(nil)
	up, err := r.Refresh(path)
	if err != nil || len(up.Items) != 1 || up.Items[0].Tool.Done || up.Items[0].Tool.Summary != "ls" {
		t.Fatalf("first refresh = %+v, %v", up, err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(result); err != nil {
		t.Fatal(err)
	}
	f.Close()
	up, err = r.Refresh(path)
	if err != nil || len(up.Items) != 1 || up.Items[0].ID != "a1" || up.Items[0].Tool.Output != "file.txt" || !up.Items[0].Tool.Done {
		t.Fatalf("second refresh = %+v, %v", up, err)
	}
	if items := r.Items(); len(items) != 1 || items[0].Tool.Output != "file.txt" {
		t.Errorf("items = %+v", items)
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Refresh(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("removed file: err = %v, want fs.ErrNotExist", err)
	}
}
