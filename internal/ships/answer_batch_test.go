package ships

// A Claude pane takes a whole AskUserQuestion call's answers in one request; every other shape is refused and sends nothing.

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/conversation"
)

// callItems is one AskUserQuestion call of four questions: the second takes several answers.
func callItems(id string, pending bool) []conversation.Item {
	var items []conversation.Item
	for i, multi := range []bool{false, true, false, false} {
		it := askItem(id+"#"+string(rune('0'+i)), pending, "A", "B", "C")
		it.Question.MultiSelect = multi
		items = append(items, it)
	}
	return items
}

type batchAnswer map[string]any

func batchBody(t *testing.T, reason, item string, answers ...batchAnswer) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{"card": "question", "reason": reason, "item": item, "answers": answers})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func refused(code int) bool { return code >= 400 && code < 500 }

// sentAny waits for the pane to have read something and returns it.
func (p *answerPane) sentAny() string {
	p.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		b, _ := os.ReadFile(p.stdin)
		if len(b) > 0 || time.Now().After(deadline) {
			return string(b)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func fourAnswers() []batchAnswer {
	return []batchAnswer{{"options": []int{2}}, {"options": []int{1, 3}}, {"other": "something else"}, {"options": []int{1}}}
}

func TestClaudeBatchAnswerIsSentOnlyWhilePaneAsksThatWholeCall(t *testing.T) {
	p := newAnswerPane(t, "claude", callItems("call", true))
	p.needsYou("question")
	post := func(body string) int {
		code, msg := p.post(body)
		t.Logf("%s -> %d %s", body, code, msg)
		return code
	}
	for name, body := range map[string]string{
		"three answers for four questions":    batchBody(t, "question", "call#0", fourAnswers()[:3]...),
		"five answers for four questions":     batchBody(t, "question", "call#0", append(fourAnswers(), batchAnswer{"options": []int{1}})...),
		"an option past the last":             batchBody(t, "question", "call#0", batchAnswer{"options": []int{4}}, fourAnswers()[1], fourAnswers()[2], fourAnswers()[3]),
		"option zero":                         batchBody(t, "question", "call#0", batchAnswer{"options": []int{0}}, fourAnswers()[1], fourAnswers()[2], fourAnswers()[3]),
		"two options for a single-choice":     batchBody(t, "question", "call#0", batchAnswer{"options": []int{1, 2}}, fourAnswers()[1], fourAnswers()[2], fourAnswers()[3]),
		"a question left unanswered":          batchBody(t, "question", "call#0", fourAnswers()[0], batchAnswer{}, fourAnswers()[2], fourAnswers()[3]),
		"an Other that is empty once cleaned": batchBody(t, "question", "call#0", fourAnswers()[0], fourAnswers()[1], batchAnswer{"other": "\x1b\n\t \r"}, fourAnswers()[3]),
		"another call's item":                 batchBody(t, "question", "elsewhere#0", fourAnswers()...),
		"the reason the pane is not on":       batchBody(t, "stop", "call#0", fourAnswers()...),
	} {
		if code := post(body); !refused(code) {
			t.Errorf("%s = %d, want a 4xx refusal", name, code)
		}
	}
	if got := p.sent(""); got != "" {
		t.Fatalf("refused answers sent %q, want nothing", got)
	}

	dirty := fourAnswers()
	dirty[2] = batchAnswer{"other": "alpha\x1bbeta\ngamma\r\ndelta"}
	if code := post(batchBody(t, "question", "call#0", dirty...)); code != http.StatusOK {
		t.Fatalf("the whole call answered at once = %d, want 200", code)
	}
	got := p.sentAny()
	for _, want := range []string{"alpha", "gamma", "delta"} {
		if !strings.Contains(got, want) {
			t.Errorf("the pane read %q, want the Other text with %q in it", got, want)
		}
	}
	for _, bad := range []string{"alpha\x1b", "gamma\n", "gamma\r"} {
		if strings.Contains(got, bad) {
			t.Errorf("the pane read %q, which holds %q: the Other text was sent with its control bytes", got, bad)
		}
	}
}

func TestClaudeBatchAnswerIsRefusedWhenAnyQuestionOfTheCallIsNotPending(t *testing.T) {
	items := callItems("call", true)
	items[1].Question.Pending = false
	p := newAnswerPane(t, "claude", items)
	p.needsYou("question")
	if code, msg := p.post(batchBody(t, "question", "call#0", fourAnswers()...)); !refused(code) {
		t.Errorf("a call with one question answered already = %d %s, want a 4xx refusal", code, msg)
	}
	if got := p.sent(""); got != "" {
		t.Errorf("sent %q, want nothing", got)
	}
}

func TestClaudeBatchAnswerIsRefusedForACallThatIsNotTheNewest(t *testing.T) {
	p := newAnswerPane(t, "claude", append(callItems("old", true), callItems("new", true)...))
	p.needsYou("question")
	if code, msg := p.post(batchBody(t, "question", "old#0", fourAnswers()...)); !refused(code) {
		t.Errorf("an answer to the older call = %d %s, want a 4xx refusal", code, msg)
	}
	if got := p.sent(""); got != "" {
		t.Errorf("sent %q, want nothing", got)
	}
}

func TestCopilotAndCodexSendNothingForABatchOrAMultiSelect(t *testing.T) {
	for harness, reason := range map[string]string{"copilot": "question", "codex": "bell"} {
		batch := callItems("call", true)
		p := newAnswerPane(t, harness, batch)
		p.needsYou(reason)
		if code, msg := p.post(batchBody(t, reason, "call#0", fourAnswers()...)); !refused(code) {
			t.Errorf("%s: a batch answer = %d %s, want a 4xx refusal", harness, code, msg)
		}
		if code, msg := p.post(`{"card":"question","reason":"` + reason + `","item":"call#1","option":1}`); !refused(code) {
			t.Errorf("%s: an answer to a question of a batch = %d %s, want a 4xx refusal", harness, code, msg)
		}
		multi := askItem("solo", true, "A", "B")
		multi.Question.MultiSelect = true
		p = newAnswerPane(t, harness, []conversation.Item{multi})
		p.needsYou(reason)
		if code, msg := p.post(batchBody(t, reason, "solo", batchAnswer{"options": []int{1, 2}})); !refused(code) {
			t.Errorf("%s: a multi-select answer = %d %s, want a 4xx refusal", harness, code, msg)
		}
		if got := p.sent(""); got != "" {
			t.Errorf("%s: sent %q, want nothing", harness, got)
		}
	}
}
