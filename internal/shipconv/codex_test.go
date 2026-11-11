package shipconv

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// codexRollout is where Codex would write the rollout called name, under the fixture's Codex home.
func (f *fixture) codexRollout(name string) string {
	h := fnv.New32a()
	h.Write([]byte(name))
	id := fmt.Sprintf("%s%08x", sid[:len(sid)-8], h.Sum32())
	return filepath.Join(f.codex, "sessions", "2026", "11", "11", "rollout-2026-11-11T15-22-11-"+id+".jsonl")
}

// codexShip adds a Codex ship whose SessionStart hook recorded path. A Codex launch has no --session-id to bind the path to.
func (f *fixture) codexShip(name, source, path string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.harness[name] = "codex"
	f.options[name] = "1700000000000000000 " + source + " " + path
	f.starts[name] = "'/bin/codex' '--' 'go'"
}

func (f *fixture) setOption(name, source, path string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.options[name] = "1700000000000000000 " + source + " " + path
}

// codexLine is the engineer's message as a rollout holds it, once as response_item and once as event_msg, so a test does not depend on which stream the adapter reads.
func codexLine(i int, text string) string {
	ts := fmt.Sprintf("2026-11-11T20:20:%02d.000Z", i%60)
	msg, _ := json.Marshal(map[string]any{"timestamp": ts, "ordinal": 2 * i, "type": "response_item",
		"payload": map[string]any{"type": "message", "role": "user", "content": []map[string]any{{"type": "input_text", "text": text}}}})
	ev, _ := json.Marshal(map[string]any{"timestamp": ts, "ordinal": 2*i + 1, "type": "event_msg",
		"payload": map[string]any{"type": "item_completed", "item": map[string]any{"type": "UserMessage", "id": fmt.Sprintf("um-%s-%d", text, i),
			"content": []map[string]any{{"type": "text", "text": text, "text_elements": []any{}}}}}})
	return string(msg) + "\n" + string(ev) + "\n"
}

func codexLines(from, to int) string {
	var sb strings.Builder
	for i := from; i <= to; i++ {
		sb.WriteString(codexLine(i, fmt.Sprintf("message %03d", i)))
	}
	return sb.String()
}

func TestCodexConversationEndpoint_States(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	path := f.codexRollout("a")

	f.harness["bare"] = "codex"
	f.options["bare"] = ""
	f.wantState("bare", "none") // SessionStart has not run, or sent a null transcript_path

	f.codexShip("fresh", "startup", path)
	f.wantState("fresh", "none") // not written yet

	appendTo(t, path, `{"timestamp":"2026-11-11T20:00:00.000Z","ordinal":0,"type":"session_meta","payload":{"id":"x"}}`+"\n")
	if code, p, body := f.get("fresh", ""); code != 200 || p.State != "ok" || len(p.Items) != 0 || p.More {
		t.Errorf("a metadata-only rollout = %d %s, want ok with no items", code, body)
	}

	appendTo(t, path, codexLines(1, 2))
	if code, p, body := f.get("fresh", ""); code != 200 || p.State != "ok" || len(p.Items) != 2 || p.Items[0].Text != "message 001" || p.Items[1].Text != "message 002" {
		t.Errorf("GET = %d %s, want ok with messages 001 and 002, once each", code, body)
	}

	for _, source := range []string{"resume", "clear", "compact", "fork", "unknown"} {
		name := "src-" + source
		f.codexShip(name, source, path)
		f.wantState(name, "ok") // any SessionStart source is the hook's word
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	f.wantState("fresh", "gone")

	bad := f.codexRollout("garbage")
	appendTo(t, bad, "not a rollout\nneither is this\n")
	f.codexShip("garbage", "startup", bad)
	f.wantState("garbage", "unreadable")

	f.codexShip("relative", "startup", "sessions/2026/11/11/rollout.jsonl")
	f.wantState("relative", "none")
}

func TestCodexConversationEndpoint_RefusesWhatIsNotARegularRolloutUnderSessions(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	outside := filepath.Join(t.TempDir(), "rollout-2026-11-11T15-22-11-"+sid+".jsonl")
	appendTo(t, outside, codexLines(1, 1))

	f.codexShip("outside", "startup", outside)
	f.wantState("outside", "unreadable") // a real rollout, but not under this Codex home

	beside := filepath.Join(f.codex, "history.jsonl")
	appendTo(t, beside, codexLines(1, 1))
	f.codexShip("beside", "startup", beside)
	f.wantState("beside", "unreadable") // under the Codex home, outside sessions/

	f.codexShip("claudes", "startup", f.path("zz"))
	appendTo(t, f.path("zz"), codexLines(1, 1))
	f.wantState("claudes", "unreadable") // another harness's tree

	if err := os.Symlink(outside, f.codexRollout("link")); err != nil {
		t.Fatal(err)
	}
	f.codexShip("linkfile", "startup", f.codexRollout("link"))
	f.wantState("linkfile", "unreadable")

	if err := os.Symlink(filepath.Dir(outside), filepath.Join(f.codex, "sessions", "2025")); err != nil {
		t.Fatal(err)
	}
	f.codexShip("linkdir", "startup", filepath.Join(f.codex, "sessions", "2025", filepath.Base(outside)))
	f.wantState("linkdir", "unreadable")

	if err := os.Mkdir(f.codexRollout("dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	f.codexShip("dir", "startup", f.codexRollout("dir"))
	f.wantState("dir", "unreadable") // a directory where the file should be

	fifo := f.codexRollout("fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("no fifo here: %v", err)
	}
	f.codexShip("fifo", "startup", fifo)
	f.wantState("fifo", "unreadable") // must not block the request either
}

func TestCodexConversationEndpoint_Pages(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	appendTo(t, f.codexRollout("big"), codexLines(1, 250))
	f.codexShip("big", "startup", f.codexRollout("big"))

	code, last, body := f.get("big", "")
	if code != 200 || last.State != "ok" || len(last.Items) != 200 || !last.More ||
		last.Items[0].Text != "message 051" || last.Items[199].Text != "message 250" {
		t.Fatalf("newest page = %d %.200s, want 200 items message 051..250 with more", code, body)
	}
	_, older, body := f.get("big", "?before="+url.QueryEscape(last.Items[0].ID))
	if older.State != "ok" || len(older.Items) != 50 || older.More || older.Items[0].Text != "message 001" || older.Items[49].Text != "message 050" {
		t.Errorf("page before message 051 = %.200s, want 50 items message 001..050 and more=false", body)
	}
}

func TestCodexConversationEndpoint_FollowsAChangedPath(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	first, second := f.codexRollout("first"), f.codexRollout("second")
	appendTo(t, first, codexLines(1, 2))
	appendTo(t, second, codexLine(7, "after the clear"))
	f.codexShip("s", "startup", first)
	if _, p, body := f.get("s", ""); p.State != "ok" || len(p.Items) != 2 {
		t.Fatalf("first rollout = %s, want 2 items", body)
	}

	f.setOption("s", "clear", second) // /new: SessionStart fires again with a new rollout
	if _, p, body := f.get("s", ""); p.State != "ok" || len(p.Items) != 1 || p.Items[0].Text != "after the clear" {
		t.Errorf("after the path changed = %s, want only the new rollout's item", body)
	}
}

func TestCodexConversationSocket_SwitchesToTheNewRolloutWhenTheOptionChanges(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	first, second := f.codexRollout("first"), f.codexRollout("second")
	appendTo(t, first, codexLines(1, 2))
	appendTo(t, second, codexLine(7, "after the clear"))
	f.codexShip("s", "startup", first)
	sub := dial(t, serve(t, f))
	sub.send("conversation_open", "s")
	sub.opened("s")
	appendTo(t, first, codexLines(3, 3))
	if got := texts(sub.next("an append to the first rollout")); len(got) != 1 || got[0] != "message 003" {
		t.Fatalf("pushed %v, want [message 003]", got)
	}

	f.setOption("s", "clear", second)
	var seen []string
	for len(seen) == 0 || seen[len(seen)-1] != "after the clear" {
		seen = append(seen, texts(sub.next("the new rollout's items"))...)
		if len(seen) > 5 {
			t.Fatalf("pushes after the switch = %v, want the new rollout's item", seen)
		}
	}
	appendTo(t, first, codexLines(4, 4))
	appendTo(t, second, codexLine(8, "still new"))
	if got := texts(sub.next("an append to the new rollout")); len(got) != 1 || got[0] != "still new" {
		t.Errorf("pushed %v, want [still new]: the old rollout is no longer followed", got)
	}
}

func TestCodexConversationSocket_DeliversAppends(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	appendTo(t, f.codexRollout("a"), codexLines(1, 3))
	f.codexShip("s", "startup", f.codexRollout("a"))
	sub := dial(t, serve(t, f))
	sub.send("conversation_open", "s")
	sub.opened("s")
	appendTo(t, f.codexRollout("a"), codexLines(4, 4))
	m := sub.next("the appended item")
	if got := texts(m); len(got) != 1 || got[0] != "message 004" || m["ship"] != "s" {
		t.Errorf("pushed %v for ship %v, want [message 004] for s", got, m["ship"])
	}
}

func TestCodexSessionStartOptionWithAPathOutsideSessionsPushesNothing(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	outside := filepath.Join(t.TempDir(), "rollout-2026-11-11T15-22-11-"+sid+".jsonl")
	appendTo(t, outside, codexLines(1, 1))
	f.codexShip("s", "startup", outside)
	appendTo(t, f.codexRollout("ours"), codexLines(1, 1))
	f.codexShip("ok", "startup", f.codexRollout("ours"))
	sub := dial(t, serve(t, f))
	sub.send("conversation_open", "s")
	sub.send("conversation_open", "ok")
	sub.opened("ok") // the control: Codex ships are followed, and only the outside one is not
	appendTo(t, outside, codexLines(2, 2))
	appendTo(t, f.codexRollout("ours"), codexLines(2, 2))
	if m := sub.next("an append to the rollout under sessions/"); m["ship"] != "ok" {
		t.Fatalf("push was for %v, want ok", m["ship"])
	}
	sub.quiet("a rollout outside sessions/")
}

func codexAssistantLine(i int, text string) string {
	msg, _ := json.Marshal(map[string]any{"timestamp": "2026-11-11T20:21:00.000Z", "ordinal": 2 * i, "type": "response_item",
		"payload": map[string]any{"type": "message", "role": "assistant", "content": []map[string]any{{"type": "output_text", "text": text}}}})
	ev, _ := json.Marshal(map[string]any{"timestamp": "2026-11-11T20:21:00.000Z", "ordinal": 2*i + 1, "type": "event_msg",
		"payload": map[string]any{"type": "item_completed", "item": map[string]any{"type": "AgentMessage", "id": fmt.Sprintf("am-%d", i),
			"content": []map[string]any{{"type": "Text", "text": text}}, "phase": "final_answer"}}})
	return string(msg) + "\n" + string(ev) + "\n"
}

func TestTailsFollowACodexRollout(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	path := f.codexRollout("a")
	appendTo(t, path, codexAssistantLine(1, "First thought.\nmore"))
	f.codexShip("alpha", "startup", path)
	tails := tailsOf(f)

	if !tails.Poll() || tails.Tail("alpha") != "First thought." {
		t.Fatalf("tail = %q, want the first line of the assistant text", tails.Tail("alpha"))
	}
	reads := tails.reads
	if tails.Poll() || tails.reads != reads {
		t.Errorf("an unchanged rollout was read again (%d reads, was %d)", tails.reads, reads)
	}
	appendTo(t, path, codexAssistantLine(2, "Second thought."))
	if !tails.Poll() || tails.Tail("alpha") != "Second thought." {
		t.Errorf("tail after an append = %q, want %q", tails.Tail("alpha"), "Second thought.")
	}
}

func TestTailsOfACodexShipWithARefusedRolloutStayBlank(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	outside := filepath.Join(t.TempDir(), "rollout-2026-11-11T15-22-11-"+sid+".jsonl")
	appendTo(t, outside, codexAssistantLine(1, "Not ours."))
	f.codexShip("alpha", "startup", outside)
	appendTo(t, f.codexRollout("ours"), codexAssistantLine(1, "Ours."))
	f.codexShip("beta", "startup", f.codexRollout("ours"))
	tails := tailsOf(f)
	tails.Poll()
	if tails.Tail("beta") != "Ours." { // the control: the adapter is there, and refuses only alpha's
		t.Fatalf("tail of a rollout under sessions/ = %q, want %q", tails.Tail("beta"), "Ours.")
	}
	if tails.Tail("alpha") != "" {
		t.Errorf("tail = %q, want blank for a rollout outside sessions/", tails.Tail("alpha"))
	}
}

func TestCodexConversationEndpoint_ASymlinkedSessionsRootIsFine(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	real := filepath.Join(t.TempDir(), "dotfiles-sessions")
	if err := os.MkdirAll(filepath.Join(real, "2026", "11", "11"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(f.codex, "sessions")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(f.codex, "sessions")); err != nil { // a dotfiles-managed ~/.codex
		t.Fatal(err)
	}
	appendTo(t, f.codexRollout("a"), codexLines(1, 1))
	f.codexShip("s", "startup", f.codexRollout("a"))
	f.wantState("s", "ok")
}
