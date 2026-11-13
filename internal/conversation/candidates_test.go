package conversation_test

// A PR URL in a tool result is a candidate; one in tool input, user text or assistant text is not.

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/conversation"
)

const (
	prResult = "https://github.com/o/r/pull/7"
	prOther  = "https://github.com/o/r/pull/8"
	prText   = "https://github.com/o/r/pull/9"
	prInput  = "https://github.com/o/r/pull/10"
)

func wantCandidates(t *testing.T, items []conversation.Item, want ...string) {
	t.Helper()
	got := conversation.PRCandidates(items)
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("PRCandidates = %q, want %q", got, want)
	}
}

func TestClaudeToolResultURLIsACandidateButNothingElseIs(t *testing.T) {
	data := `{"type":"user","uuid":"u1","message":{"content":"please look at ` + prText + `"}}` + "\n" +
		`{"type":"assistant","uuid":"a1","message":{"content":[{"type":"text","text":"I will open ` + prText + `"},{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"gh pr view ` + prInput + `"}}]}}` + "\n" +
		`{"type":"user","uuid":"u2","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"Creating pull request\n` + prResult + `\n"}]}}` + "\n" +
		`{"type":"assistant","uuid":"a2","message":{"content":[{"type":"tool_use","id":"t2","name":"Bash","input":{"command":"gh pr view"}}]}}` + "\n" +
		`{"type":"user","uuid":"u3","message":{"content":[{"type":"tool_result","tool_use_id":"t2","content":"see (` + prOther + `), and again ` + prResult + `."}]}}` + "\n"
	items, err := conversation.ReadClaudeCode(strings.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	wantCandidates(t, items, prResult, prOther)
}

func TestCodexToolResultURLIsACandidateButNothingElseIs(t *testing.T) {
	line := func(ord int, payload string) string {
		return fmt.Sprintf(`{"timestamp":"2026-11-11T20:21:00.%03dZ","ordinal":%d,"type":"response_item","payload":%s}`+"\n", ord, ord, payload)
	}
	data := line(1, `{"type":"message","role":"user","content":[{"type":"input_text","text":"look at `+prText+`"}]}`) +
		line(2, `{"type":"message","role":"assistant","content":[{"type":"output_text","text":"opening `+prText+`"}]}`) +
		line(3, `{"type":"custom_tool_call","id":"ctc_1","status":"completed","call_id":"c1","name":"exec","input":"gh pr view `+prInput+`"}`) +
		line(4, `{"type":"custom_tool_call_output","id":"ctco_1","call_id":"c1","output":[{"type":"input_text","text":"Script completed\nOutput:\n"},{"type":"input_text","text":"`+prResult+`\n"}]}`)
	wantCandidates(t, readCodex(t, data), prResult)
}

func TestCopilotToolResultURLIsACandidateButNothingElseIs(t *testing.T) {
	data := cl("user.message", "01.000", `{"content":"look at `+prText+`","interactionId":"x"}`) +
		cl("assistant.message", "02.000", `{"messageId":"m1","content":"opening `+prText+`","toolRequests":[{"toolCallId":"tc1","name":"bash","arguments":{"command":"gh pr view `+prInput+`"},"type":"function"}],"turnId":"0"}`) +
		cl("tool.execution_start", "03.000", `{"toolCallId":"tc1","toolName":"bash","arguments":{"command":"gh pr view `+prInput+`"},"turnId":"0"}`) +
		cl("tool.execution_complete", "04.000", `{"toolCallId":"tc1","success":true,"result":{"content":"`+prResult+`\n<shellId: 0 completed with exit code 0>"}}`)
	wantCandidates(t, readCop(t, data), prResult)
}

func TestACandidateMustBeAPullRequestURL(t *testing.T) {
	data := `{"type":"assistant","uuid":"a1","message":{"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{}}]}}` + "\n" +
		`{"type":"user","uuid":"u1","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"https://github.com/o/r/issues/3 https://github.com/o/r/pull/ https://github.com/o/r/pulls ` + prResult + `/files https://example.com/o/r/pullman/1"}]}}` + "\n"
	items, err := conversation.ReadClaudeCode(strings.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	wantCandidates(t, items, prResult)
}
