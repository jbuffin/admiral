package conversation_test

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/conversation"
)

// Claude Code writes a question's tool_use line only once it is resolved, so the
// pending question is the sample cut just before its answer: line 97 is that
// tool_use, and its tool_result is the next line.
const sampleLinePendingQuestion = 97

func readSample(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/claude-sample.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func read(t *testing.T, data string) []conversation.Item {
	t.Helper()
	items, err := conversation.ReadClaudeCode(strings.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return items
}

func kinds(items []conversation.Item) []conversation.Kind {
	var ks []conversation.Kind
	for _, it := range items {
		ks = append(ks, it.Kind)
	}
	return ks
}

func open(t *testing.T, path string) (*os.File, os.FileInfo) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	info, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	return f, info
}

func appendTo(t *testing.T, path, text string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(text); err != nil {
		t.Fatal(err)
	}
}

func userLine(i int) string {
	return fmt.Sprintf(`{"type":"user","uuid":"u%04d","message":{"content":"message %d"}}`+"\n", i, i)
}

func userLines(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString(userLine(i))
	}
	return b.String()
}

// refresh reads a transcript file whole through a new Reader.
func refresh(t *testing.T, o conversation.Options, content string) *conversation.Reader {
	t.Helper()
	path := filepath.Join(t.TempDir(), "t.jsonl")
	appendTo(t, path, content)
	r := conversation.NewReader(o)
	f, info := open(t, path)
	if _, err := r.Refresh(f, info); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestSampleParsesIntoItems(t *testing.T) {
	items := read(t, string(readSample(t)))

	var seq []conversation.Item
	ids := map[string]bool{}
	for _, it := range items {
		if ids[it.ID] || it.ID == "" {
			t.Errorf("item id %q is empty or repeated", it.ID)
		}
		ids[it.ID] = true
		if it.Kind != conversation.KindNotice {
			seq = append(seq, it)
		}
	}

	want := []conversation.Kind{
		"user", "assistant", "turn_end", // hello
		"user", "tool_call", "assistant", "turn_end", // Bash ls -la
		"user", "question", "assistant", "turn_end", // answered question
		"user", "tool_call", "assistant", "turn_end", // Agent, launched
		"assistant", "turn_end", // after the subagent's hand-back
		"compaction",
		"user", "question", "assistant", "turn_end", // second question
	}
	if fmt.Sprint(kinds(seq)) != fmt.Sprint(want) {
		t.Fatalf("kinds = %v\nwant    %v", kinds(seq), want)
	}

	if seq[0].Text != "Say hello in one short sentence." || seq[1].Text != "Hello, nice to meet you!" {
		t.Errorf("first turn = %q / %q", seq[0].Text, seq[1].Text)
	}
	if seq[2].Duration != 1287*time.Millisecond {
		t.Errorf("first turn worked for %v, want 1.287s", seq[2].Duration)
	}
	if q := seq[8].Question; q == nil || q.Text != "Which colour do you prefer: red or blue?" || q.Pending || !strings.Contains(q.Answer, "Red") ||
		len(q.Options) != 2 || q.Options[0].Label != "Red" || q.Options[0].Description != "You prefer red." || q.Options[1].Label != "Blue" {
		t.Errorf("answered question = %+v", q)
	}

	// Thinking blocks, the compaction summary and the /compact command lines
	// never come out as text.
	for _, it := range items {
		if it.Kind == conversation.KindNotice {
			continue
		}
		for _, leak := range []string{"CAQS", "This session is being continued", "<command-name>", "<local-command", "<system-reminder>"} {
			if strings.Contains(it.Text, leak) {
				t.Errorf("%s item leaks %q: %q", it.Kind, leak, it.Text)
			}
		}
	}
}

func TestResultFoldsIntoItsCall(t *testing.T) {
	items := read(t, string(readSample(t)))
	var bash conversation.Item
	for _, it := range items {
		if it.Kind == conversation.KindToolCall && it.Tool.Name == "Bash" {
			bash = it
		}
	}
	if !bash.Tool.Done || !strings.Contains(bash.Tool.Output, "total 0") {
		t.Errorf("Bash call = %+v, want its ls output folded in", bash.Tool)
	}

	// A result written after the call was read arrives as an update under the call's id.
	path := filepath.Join(t.TempDir(), "t.jsonl")
	appendTo(t, path, `{"type":"assistant","uuid":"a1","message":{"content":[{"type":"tool_use","id":"tu1","name":"Bash","input":{"command":"ls"}}]}}`+"\n")
	r := conversation.NewReader(conversation.Options{})
	f, info := open(t, path)
	up, err := r.Refresh(f, info)
	if err != nil || len(up.Items) != 1 || up.Items[0].Tool.Done || up.Items[0].Tool.Summary != "ls" {
		t.Fatalf("first refresh = %+v, %v", up, err)
	}
	appendTo(t, path, `{"type":"user","uuid":"u2","message":{"content":[{"type":"tool_result","tool_use_id":"tu1","content":"file.txt"}]}}`+"\n")
	f, info = open(t, path)
	up, err = r.Refresh(f, info)
	if err != nil || len(up.Items) != 1 || up.Items[0].ID != "a1" || up.Items[0].Tool.Output != "file.txt" || !up.Items[0].Tool.Done {
		t.Fatalf("second refresh = %+v, %v", up, err)
	}
	if len(r.Items()) != 1 {
		t.Errorf("reader holds %d items, want the one call", len(r.Items()))
	}
}

func TestQuestionIsPendingUntilAnswered(t *testing.T) {
	lines := bytes.SplitAfter(readSample(t), []byte("\n"))
	items := read(t, string(bytes.Join(lines[:sampleLinePendingQuestion], nil)))

	last := items[len(items)-1]
	if last.Kind != conversation.KindQuestion {
		t.Fatalf("last item = %s, want a question", last.Kind)
	}
	q := last.Question
	if !q.Pending || q.Answer != "" || q.Text != "Which animal do you prefer: cat or dog?" ||
		len(q.Options) != 2 || q.Options[0].Label != "Cat" || q.Options[0].Description != "You prefer cats." || q.Options[1].Label != "Dog" {
		t.Errorf("pending question = %+v", q)
	}

	full := read(t, string(readSample(t)))
	var answered *conversation.Question
	for _, it := range full {
		if it.ID == last.ID {
			answered = it.Question
		}
	}
	if answered == nil || answered.Pending || !strings.Contains(answered.Answer, "Cat") {
		t.Errorf("the same question in the full sample = %+v, want answered Cat", answered)
	}
}

func TestUnknownAndMalformedLinesAreSkipped(t *testing.T) {
	items := read(t, strings.Join([]string{
		`{"type":"user","uuid":"u1","message":{"role":"user","content":"hello"}}`,
		`{"type":"a-line-type-from-the-future","uuid":"x1","payload":{"a":1}}`,
		`{not json at all`,
		`{"type":"assistant","isSidechain":true,"uuid":"s1","message":{"content":[{"type":"text","text":"from a subagent file"}]}}`,
		`{"type":"assistant","uuid":"a1","message":{"content":[{"type":"text","text":"hi"}]}}`,
	}, "\n")+"\n")
	if len(items) != 2 || items[0].Text != "hello" || items[1].Text != "hi" {
		t.Errorf("items = %+v, want the user line then the assistant line", items)
	}

	_, err := conversation.ReadClaudeCode(strings.NewReader("{not json\nplain words\n"))
	if !errors.Is(err, conversation.ErrUnreadable) {
		t.Errorf("a file with nothing parseable: err = %v, want ErrUnreadable", err)
	}
}

func TestMetadataOnlyFileIsEmpty(t *testing.T) {
	items, err := conversation.ReadClaudeCode(strings.NewReader(
		`{"type":"mode","mode":"normal","sessionId":"s"}` + "\n" + `{"type":"permission-mode","permissionMode":"auto"}` + "\n"))
	if err != nil || len(items) != 0 {
		t.Errorf("items = %v, err = %v, want an empty conversation", items, err)
	}
}

func TestReaderLimits(t *testing.T) {
	big := strings.Repeat("x", 900<<10)

	t.Run("keeps the last 200 items", func(t *testing.T) {
		r := refresh(t, conversation.Options{}, userLines(3000))
		items := r.Items()
		if len(items) != 200 || items[0].ID != "u2800" || items[199].ID != "u2999" {
			t.Errorf("held %d items from %s to %s, want u2800 to u2999", len(items), items[0].ID, items[len(items)-1].ID)
		}
	})

	t.Run("cuts a tool's output", func(t *testing.T) {
		r := refresh(t, conversation.Options{},
			`{"type":"assistant","uuid":"c1","message":{"content":[{"type":"tool_use","id":"tc","name":"Read","input":{}}]}}`+"\n"+
				`{"type":"user","uuid":"r1","message":{"content":[{"type":"tool_result","tool_use_id":"tc","content":"`+big+`"}]}}`+"\n")
		out := r.Items()[0].Tool.Output
		if len(out) > 16<<10+64 || !strings.HasSuffix(out, "[output cut]") {
			t.Errorf("output is %d bytes, want a preview of about 16 KB ending in the marker", len(out))
		}
	})

	t.Run("cuts a question's options", func(t *testing.T) {
		wide := strings.Repeat("x", 20<<10)
		opts := strings.Repeat(`{"label":"`+wide+`","description":"`+wide+`"},`, 20)
		r := refresh(t, conversation.Options{},
			`{"type":"assistant","uuid":"q1","message":{"content":[{"type":"tool_use","id":"tq","name":"AskUserQuestion","input":{"questions":[{"question":"?","options":[`+opts+`{}]}]}}]}}`+"\n")
		q := r.Items()[0].Question
		if len(q.Options) != 16 || len(q.Options[0].Label) > 300 || len(q.Options[0].Description) > 3<<10 {
			t.Errorf("question has %d options, the first %d and %d bytes; want 16, cut to 256 B and 2 KB", len(q.Options), len(q.Options[0].Label), len(q.Options[0].Description))
		}
	})

	t.Run("makes at most 8 items from one question call", func(t *testing.T) {
		r := refresh(t, conversation.Options{},
			`{"type":"assistant","uuid":"q2","message":{"content":[{"type":"tool_use","id":"tm","name":"AskUserQuestion","input":{"questions":[`+strings.Repeat(`{"question":"?"},`, 5000)+`{}]}}]}}`+"\n")
		if n := len(r.Items()); n != 8 {
			t.Errorf("one call made %d question items, want 8", n)
		}
	})

	t.Run("holds about 4 MB of text", func(t *testing.T) {
		chunk := strings.Repeat("y", 60<<10)
		var b strings.Builder
		for i := 0; i < 150; i++ {
			b.WriteString(fmt.Sprintf(`{"type":"user","uuid":"u%04d","message":{"content":"%s"}}`+"\n", i, chunk))
		}
		items := refresh(t, conversation.Options{}, b.String()).Items()
		total := 0
		for _, it := range items {
			total += len(it.Text)
		}
		if total > 4<<20 || total < 3<<20 || items[len(items)-1].ID != "u0149" {
			t.Errorf("holds %d bytes in %d items ending at %s, want just under 4 MB ending at u0149", total, len(items), items[len(items)-1].ID)
		}
	})

	t.Run("skips a line over 1 MB", func(t *testing.T) {
		items := refresh(t, conversation.Options{}, userLine(1)+
			`{"type":"user","uuid":"huge","message":{"content":"`+strings.Repeat("z", 1<<20)+`"}}`+"\n"+userLine(2)).Items()
		if len(items) != 2 || items[0].ID != "u0001" || items[1].ID != "u0002" {
			t.Errorf("items = %+v, want the two short lines", items)
		}
	})

	t.Run("text view holds 256 KB", func(t *testing.T) {
		chunk := strings.Repeat("y", 60<<10)
		var b strings.Builder
		for i := 0; i < 10; i++ {
			b.WriteString(fmt.Sprintf(`{"type":"user","uuid":"u%04d","message":{"content":"%s"}}`+"\n", i, chunk))
		}
		items := refresh(t, conversation.TextView(), b.String()).Items()
		if len(items) != 4 || items[3].ID != "u0009" {
			t.Errorf("text view holds %d items ending at %s, want the last 4 within 256 KB", len(items), items[len(items)-1].ID)
		}
	})
}

func TestPageBefore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.jsonl")
	appendTo(t, path, userLines(450))
	f, info := open(t, path)
	o := conversation.Options{}

	newest, more, err := conversation.Page(f, info, o, "", 200)
	if err != nil || len(newest) != 200 || !more || newest[0].ID != "u0250" || newest[199].ID != "u0449" {
		t.Fatalf("newest: %d items, more %v, err %v", len(newest), more, err)
	}
	older, more, err := conversation.Page(f, info, o, "u0250", 200)
	if err != nil || len(older) != 200 || !more || older[0].ID != "u0050" || older[199].ID != "u0249" {
		t.Fatalf("older: %d items, more %v, err %v", len(older), more, err)
	}
	last, more, err := conversation.Page(f, info, o, "u0050", 200)
	if err != nil || len(last) != 50 || more || last[0].ID != "u0000" {
		t.Fatalf("last: %d items, more %v, err %v", len(last), more, err)
	}
	if _, _, err := conversation.Page(f, info, o, "nope", 10); !errors.Is(err, conversation.ErrUnknownID) {
		t.Errorf("unknown id: err = %v, want ErrUnknownID", err)
	}
}

func TestClearServesOnlyWhatIsWrittenAfter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.jsonl")
	appendTo(t, path, userLines(3))
	r := conversation.NewReader(conversation.Options{})
	f, info := open(t, path)
	if _, err := r.Refresh(f, info); err != nil || len(r.Items()) != 3 {
		t.Fatalf("before: %d items, err %v", len(r.Items()), err)
	}

	r.Clear(info.Size())
	if len(r.Items()) != 0 {
		t.Fatalf("after Clear the reader holds %d items", len(r.Items()))
	}
	appendTo(t, path, userLine(9))
	f, info = open(t, path)
	up, err := r.Refresh(f, info)
	if err != nil || len(up.Items) != 1 || up.Items[0].ID != "u0009" || len(r.Items()) != 1 {
		t.Errorf("after Clear: update %+v, held %d, err %v; want only u0009", up, len(r.Items()), err)
	}
}

func TestTextViewKeepsNonMetaNotices(t *testing.T) {
	items := read(t, strings.Join([]string{
		`{"type":"user","uuid":"n1","message":{"content":"<task-notification>\n<summary>done</summary>"}}`,
		`{"type":"user","isMeta":true,"uuid":"n2","message":{"content":"Another Claude session sent a message:\nhello"}}`,
		`{"type":"user","isMeta":true,"uuid":"m1","message":{"content":"<system-reminder>x</system-reminder>"}}`,
		`{"type":"user","uuid":"x/1","message":{"content":"forged"}}`,
		`{"type":"assistant","uuid":"x","message":{"content":[{"type":"text","text":"a"},{"type":"tool_use","id":"t","name":"Bash","input":{}}]}}`,
	}, "\n")+"\n")

	want := []conversation.Kind{"notice", "notice", "user", "assistant", "tool_call"}
	if fmt.Sprint(kinds(items)) != fmt.Sprint(want) {
		t.Fatalf("kinds = %v, want %v", kinds(items), want)
	}
	if !conversation.TextOnly(items[0]) || conversation.TextOnly(items[1]) || conversation.TextOnly(items[4]) {
		t.Errorf("text view keeps: subagent report %v, other session %v, tool call %v; want true, false, false",
			conversation.TextOnly(items[0]), conversation.TextOnly(items[1]), conversation.TextOnly(items[4]))
	}
	if items[2].ID != "x/1" || items[4].ID != "x#1" {
		t.Errorf("ids = %q and %q, want x/1 and x#1", items[2].ID, items[4].ID)
	}
}
