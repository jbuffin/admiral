package conversation_test

// Reading a Copilot events.jsonl into conversation items.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/conversation"
)

// A real Copilot 1.0.109 events.jsonl: line 46 is the ask_user start and line 52 its tool.execution_complete.
const copilotSampleAskStart = 46

func copilotSample(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("testdata/copilot-sample.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func sampleLines(t *testing.T, from, to int) string {
	t.Helper()
	lines := strings.SplitAfter(copilotSample(t), "\n")
	return strings.Join(lines[from-1:to], "")
}

func readCop(t *testing.T, data string) []conversation.Item {
	t.Helper()
	items, err := conversation.ReadCopilot(strings.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return items
}

// cl is one events.jsonl line in the recorded shape.
func cl(typ, ts, data string) string {
	return fmt.Sprintf(`{"type":%q,"id":"i-%s-%s","parentId":null,"timestamp":%q,"data":%s}`+"\n", typ, typ, ts, "2026-11-11T20:10:"+ts+"Z", data)
}

func copilotTurn(prompt, reply string) string {
	return cl("user.message", "01.000", fmt.Sprintf(`{"content":%q,"interactionId":"x"}`, prompt)) +
		cl("assistant.message", "02.000", fmt.Sprintf(`{"messageId":"m1","content":%q,"toolRequests":[],"turnId":"0"}`, reply)) +
		cl("assistant.turn_end", "03.000", `{"turnId":"0"}`)
}

func TestCopilotSampleParsesIntoItems(t *testing.T) {
	items := readCop(t, copilotSample(t))

	ids := map[string]bool{}
	for _, it := range items {
		if it.ID == "" || ids[it.ID] {
			t.Errorf("item id %q is empty or repeated", it.ID)
		}
		ids[it.ID] = true
	}
	want := []conversation.Kind{"user", "tool_call", "assistant", "turn_end", "user", "question", "assistant", "turn_end"}
	if fmt.Sprint(kinds(items)) != fmt.Sprint(want) {
		t.Fatalf("kinds = %v\nwant    %v", kinds(items), want)
	}

	if items[0].Text != "Run the shell command: echo hello-from-sample. Then reply with one word." {
		t.Errorf("first user text = %q", items[0].Text)
	}
	if tc := items[1].Tool; tc == nil || tc.Name != "bash" || tc.Input != "echo hello-from-sample" || !tc.Done || tc.Error || !strings.Contains(tc.Output, "hello-from-sample") {
		t.Errorf("bash call = %+v", tc)
	}
	if items[2].Text != "Done." {
		t.Errorf("assistant text = %q", items[2].Text)
	}
	// From the typed prompt to the last model call's end, not from the memory line before it.
	if items[3].Duration != 3353*time.Millisecond {
		t.Errorf("first turn worked for %v, want 3.353s", items[3].Duration)
	}
	if q := items[5].Question; q == nil || q.Text != "Which colour, red or blue?" || q.Pending || !strings.Contains(q.Answer, "red") {
		t.Errorf("answered question = %+v", q)
	}
	if items[6].Text != "Red." {
		t.Errorf("second assistant text = %q", items[6].Text)
	}
	if items[7].Duration != 24816*time.Millisecond {
		t.Errorf("second turn worked for %v, want 24.816s", items[7].Duration)
	}
}

func TestCopilotQuestionIsPendingUntilItsToolCompletes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	appendTo(t, path, sampleLines(t, 1, copilotSampleAskStart))
	r := conversation.NewReader(conversation.Options{Harness: "copilot"})
	f, info := open(t, path)
	if _, err := r.Refresh(f, info); err != nil {
		t.Fatal(err)
	}
	var q *conversation.Item
	for _, it := range r.Items() {
		if it.Kind == conversation.KindQuestion {
			it := it
			q = &it
		}
	}
	if q == nil || !q.Question.Pending || q.Question.Text != "Which colour, red or blue?" {
		t.Fatalf("before the answer: %+v", q)
	}

	appendTo(t, path, sampleLines(t, copilotSampleAskStart+1, copilotSampleAskStart+6))
	f, info = open(t, path)
	up, err := r.Refresh(f, info)
	if err != nil {
		t.Fatal(err)
	}
	var got *conversation.Item
	for _, it := range up.Items {
		if it.ID == q.ID {
			it := it
			got = &it
		}
	}
	if got == nil || got.Question.Pending || !strings.Contains(got.Question.Answer, "red") {
		t.Fatalf("after the answer the question is %+v", got)
	}
}

func TestCopilotAskUserTakesBothArgumentShapes(t *testing.T) {
	old := cl("user.message", "01.000", `{"content":"pick"}`) +
		cl("tool.execution_start", "02.000", `{"toolCallId":"t1","toolName":"ask_user","arguments":{"question":"Which one?","choices":["a","b"],"allow_freeform":true},"turnId":"0"}`)
	items := readCop(t, old)
	if len(items) != 2 || items[1].Kind != conversation.KindQuestion {
		t.Fatalf("kinds = %v", kinds(items))
	}
	q := items[1].Question
	if q.Text != "Which one?" || !q.Pending || len(q.Options) != 2 || q.Options[0].Label != "a" || q.Options[1].Label != "b" {
		t.Errorf("old-shape question = %+v", q)
	}
}

func TestCopilotModelLinesNeverDoubleText(t *testing.T) {
	data := copilotTurn("hi", "Done.") +
		cl("model.message", "02.500", `{"kind":"message","turn":0,"callId":"c1","message":{"content":"Done.","role":"assistant"}}`) +
		cl("model.response", "02.600", `{"kind":"response","turn":0,"callId":"c1","response":{"content":"Done.","role":"assistant"}}`)
	items := readCop(t, data)
	if fmt.Sprint(kinds(items)) != "[user assistant turn_end]" {
		t.Fatalf("kinds = %v", kinds(items))
	}
}

func TestCopilotEngineerTypedUserLinesOnly(t *testing.T) {
	data := cl("user.message", "00.500", `{"content":"","transformedContent":"<memories>m</memories>","source":"memory","agentTaskId":"a1"}`) +
		cl("user.message", "00.700",`{"content":"background result","source":"agent","agentTaskId":"a2"}`) +
		copilotTurn("typed", "ok")
	items := readCop(t, data)
	if fmt.Sprint(kinds(items)) != "[user assistant turn_end]" || items[0].Text != "typed" {
		t.Fatalf("items = %v", items)
	}
}

func TestCopilotUnknownMalformedAndTruncatedLinesAreSkipped(t *testing.T) {
	data := cl("user.message", "01.000", `{"content":"one"}`) +
		"not json at all\n" +
		cl("something.new", "01.500", `{"x":1}`) +
		cl("assistant.message", "02.000", `{"messageId":"m","content":"two","toolRequests":[]}`) +
		`{"type":"assistant.message","data":{"messageId":"m2","content":"cut`
	items := readCop(t, data)
	if fmt.Sprint(kinds(items)) != "[user assistant]" || items[1].Text != "two" {
		t.Fatalf("items = %v", items)
	}
}

func TestCopilotPartialLineWaitsForItsNewline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	line := cl("assistant.message", "02.000", `{"messageId":"m","content":"whole","toolRequests":[]}`)
	appendTo(t, path, cl("user.message", "01.000", `{"content":"q"}`)+line[:len(line)/2])
	r := conversation.NewReader(conversation.Options{Harness: "copilot"})
	f, info := open(t, path)
	if _, err := r.Refresh(f, info); err != nil {
		t.Fatal(err)
	}
	if n := len(r.Items()); n != 1 {
		t.Fatalf("held %d items before the line is whole, want 1", n)
	}
	appendTo(t, path, line[len(line)/2:])
	f, info = open(t, path)
	if _, err := r.Refresh(f, info); err != nil {
		t.Fatal(err)
	}
	items := r.Items()
	if len(items) != 2 || items[1].Text != "whole" {
		t.Fatalf("items = %v", items)
	}
}

func TestCopilotSubagentRunIsOneToolCall(t *testing.T) {
	data := cl("user.message", "01.000", `{"content":"delegate"}`) +
		cl("assistant.message", "02.000", `{"messageId":"m1","content":"","toolRequests":[{"toolCallId":"T1","name":"task","arguments":{"prompt":"go"}}]}`) +
		cl("tool.execution_start", "02.100", `{"toolCallId":"T1","toolName":"task","arguments":{"prompt":"go"}}`) +
		cl("assistant.message", "03.000", `{"messageId":"m2","content":"inner thought text","toolRequests":[{"toolCallId":"I1","name":"bash","arguments":{"command":"ls"}}],"parentToolCallId":"T1"}`) +
		cl("tool.execution_start", "03.100", `{"toolCallId":"I1","toolName":"bash","arguments":{"command":"ls"},"parentToolCallId":"T1"}`) +
		cl("tool.execution_complete", "03.200", `{"toolCallId":"I1","success":true,"result":{"content":"inner output"},"parentToolCallId":"T1"}`) +
		cl("assistant.turn_end", "03.300", `{"turnId":"1","parentToolCallId":"T1"}`) +
		cl("tool.execution_complete", "04.000", `{"toolCallId":"T1","success":true,"result":{"content":"sub done"}}`) +
		cl("assistant.message", "05.000", `{"messageId":"m3","content":"Final.","toolRequests":[]}`) +
		cl("assistant.turn_end", "05.100", `{"turnId":"2"}`)
	items := readCop(t, data)
	if fmt.Sprint(kinds(items)) != "[user tool_call assistant turn_end]" {
		t.Fatalf("kinds = %v", kinds(items))
	}
	if items[1].Tool.Name != "task" || !items[1].Tool.Done {
		t.Errorf("parent call = %+v", items[1].Tool)
	}
	for _, it := range items {
		if strings.Contains(it.Text, "inner") {
			t.Errorf("a subagent's text shows at the top level: %+v", it)
		}
	}
}

func TestCopilotNoticeAndCompaction(t *testing.T) {
	data := copilotTurn("hi", "ok") +
		cl("system.notification", "04.000", `{"content":"Loaded instructions from the project","kind":{"type":"instruction_discovered"}}`) +
		cl("session.compaction_start", "05.000", `{}`) +
		cl("session.compaction_complete", "06.000", `{"success":true,"summaryContent":"SUMMARY-BODY"}`)
	items := readCop(t, data)
	if fmt.Sprint(kinds(items)) != "[user assistant turn_end notice compaction]" {
		t.Fatalf("kinds = %v", kinds(items))
	}
	if !strings.Contains(items[3].Text, "Loaded instructions from the project") {
		t.Errorf("notice text = %q", items[3].Text)
	}
	for _, it := range items {
		if strings.Contains(it.Text, "SUMMARY-BODY") {
			t.Errorf("the compaction summary shows as text: %+v", it)
		}
	}
}

func TestCopilotOneTurnEndPerPromptNotPerModelCall(t *testing.T) {
	data := cl("user.message", "01.000", `{"content":"work"}`) +
		cl("assistant.message", "02.000", `{"messageId":"a","content":"","toolRequests":[{"toolCallId":"t1","name":"bash","arguments":{"command":"ls"}}]}`) +
		cl("tool.execution_start", "02.100", `{"toolCallId":"t1","toolName":"bash","arguments":{"command":"ls"}}`) +
		cl("tool.execution_complete", "02.200", `{"toolCallId":"t1","success":true,"result":{"content":"x"}}`) +
		cl("assistant.turn_end", "02.300", `{"turnId":"0"}`) +
		cl("assistant.message", "03.000", `{"messageId":"b","content":"Done.","toolRequests":[]}`) +
		cl("assistant.turn_end", "03.500", `{"turnId":"1"}`)
	items := readCop(t, data)
	if fmt.Sprint(kinds(items)) != "[user tool_call assistant turn_end]" {
		t.Fatalf("kinds = %v", kinds(items))
	}
	if d := items[3].Duration; d != 2500*time.Millisecond {
		t.Errorf("worked for %v, want 2.5s from the prompt to the last model call", d)
	}
}
