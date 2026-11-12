package e2e

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

type fleetSpec struct {
	key           string
	model, effort string
	codexOnly     bool // a fleet with no Claude Code dock, so its chat is off
}

// chatDock is one fleet's chat in a world that has several: its own fake Claude
// dock (config dir, cue file), so the fleets never share a transcript or a cue.
type chatDock struct {
	w      *world
	t      *testing.T
	fleet  string
	cfgDir string
	cue    string
}

func (f *chatDock) at(t *testing.T) *chatDock {
	c := *f
	c.t, c.w = t, f.w.at(t)
	return &c
}

func (f *chatDock) sessionName() string { return chatSessionFor(f.fleet) }
func (f *chatDock) url(tail string) string {
	return f.w.in.LocalURL + "/api/chat/" + f.fleet + "/" + tail
}
func (f *chatDock) recDir() string { return filepath.Join(f.w.rec, f.sessionName()) }
func (f *chatDock) transcript(n string) string {
	return filepath.Join(f.cfgDir, "projects", "p", n+".jsonl")
}
func (f *chatDock) dir() string          { return filepath.Join(f.w.home, "chat", f.fleet) }
func (f *chatDock) logPath() string      { return filepath.Join(f.dir(), "conversation.jsonl") }
func (f *chatDock) questionsDir() string { return filepath.Join(f.dir(), "questions") }

func (f *chatDock) writeCue(lines ...string) {
	f.t.Helper()
	if err := os.WriteFile(f.cue, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		f.t.Fatal(err)
	}
}

func (f *chatDock) touch(name string) {
	f.t.Helper()
	if err := os.MkdirAll(f.recDir(), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.recDir(), name), nil, 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *chatDock) stdin() string {
	data, _ := os.ReadFile(filepath.Join(f.recDir(), "stdin"))
	return string(data)
}

func (f *chatDock) start() chatSession {
	f.t.Helper()
	code, body := send(f.t, http.MethodPost, f.url("start"), nil)
	if code != http.StatusOK {
		f.t.Fatalf("POST /api/chat/%s/start = %d, want 200: %s", f.fleet, code, body)
	}
	return f.session()
}

func (f *chatDock) session() chatSession { return fetchFleetChat(f.t, f.w.in.LocalURL, f.fleet) }

func (f *chatDock) waitSession(d time.Duration, what string, pred func(chatSession) bool) chatSession {
	f.t.Helper()
	deadline := time.Now().Add(d)
	for {
		s := f.session()
		if pred(s) {
			return s
		}
		if time.Now().After(deadline) {
			f.t.Fatalf("within %s GET /api/chat/%s/session never showed %s; last: %+v", d, f.fleet, what, s)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (f *chatDock) newChat() {
	f.t.Helper()
	if code, body := send(f.t, http.MethodPost, f.url("new"), nil); code != http.StatusOK {
		f.t.Fatalf("POST /api/chat/%s/new = %d, want 200: %s", f.fleet, code, body)
	}
}

// logEntries reads the fleet's conversation.jsonl; a missing file has none.
func (f *chatDock) logEntries() []map[string]any {
	f.t.Helper()
	file, err := os.Open(f.logPath())
	if err != nil {
		return nil
	}
	defer file.Close()
	var out []map[string]any
	sc := bufio.NewScanner(file)
	sc.Buffer(nil, 4<<20)
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			f.t.Fatalf("%s holds a line that is not JSON: %q", f.logPath(), sc.Text())
		}
		out = append(out, m)
	}
	return out
}

// logTexts lists the log's text messages as "role: text" and counts its markers
// and question cards.
func (f *chatDock) logTexts() (msgs []string, resets, questions int) {
	for _, m := range f.logEntries() {
		switch m["kind"] {
		case "reset":
			resets++
		case "question":
			questions++
		case "message", nil:
			role, _ := m["role"].(string)
			text, _ := m["text"].(string)
			msgs = append(msgs, role+": "+text)
		}
	}
	return
}

func (f *chatDock) waitLog(what string, pred func(msgs []string, resets, questions int) bool) {
	f.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		msgs, r, q := f.logTexts()
		if pred(msgs, r, q) {
			return
		}
		if time.Now().After(deadline) {
			f.t.Fatalf("within 10s %s never held %s; it has %v, %d markers, %d questions", f.logPath(), what, msgs, r, q)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// newFleetsWorld writes a config of the given fleets, in order, each with its own dock.
// cue gives a fleet's cue lines, written before Admiral starts.
func newFleetsWorld(t *testing.T, cue func(f *chatDock) []string, specs ...fleetSpec) (*world, map[string]*chatDock) {
	t.Helper()
	w := newWorld(t)
	chats := map[string]*chatDock{}
	var parts []string
	for i, sp := range specs {
		f := &chatDock{w: w, t: t, fleet: sp.key, cfgDir: filepath.Join(w.root, "cfg"+string(rune('a'+i))), cue: filepath.Join(w.root, "cue"+string(rune('a'+i)))}
		if err := os.MkdirAll(filepath.Join(f.cfgDir, "projects", "p"), 0o755); err != nil {
			t.Fatal(err)
		}
		chats[sp.key] = f
		fleet := map[string]any{"name": sp.key, "search_roots": []string{w.work}}
		if sp.codexOnly {
			fleet["codex"] = map[string]any{"executable": w.fakeExe("codex")}
		} else {
			env := map[string]string{"FAKE_RECORD_DIR": w.rec, "FAKE_CUE": f.cue, "CLAUDE_CONFIG_DIR": f.cfgDir}
			fleet["claude"] = map[string]any{"executable": w.fakeExe("claude"), "args": []string{"--fa-" + sp.key}, "env": env}
			fleet["chat"] = map[string]any{"model": sp.model, "effort": sp.effort}
		}
		b, err := json.Marshal(fleet)
		if err != nil {
			t.Fatal(err)
		}
		k, _ := json.Marshal(sp.key)
		parts = append(parts, string(k)+":"+string(b))
	}
	for _, f := range chats {
		if cue != nil {
			if lines := cue(f); lines != nil {
				f.writeCue(lines...)
			}
		}
	}
	writeConfig(t, w.home, `{"version":1,"fleets":{`+strings.Join(parts, ",")+`}}`)
	return w, chats
}

func chatCue(f *chatDock) []string {
	return []string{"record", "bracketedpaste", sessionStartCue(f.transcript("t1"), "startup"),
		"wait go-clear", sessionStartCue(f.transcript("t2"), "clear")}
}

func waitFleetWS(t *testing.T, c *websocket.Conn, fleet string, d time.Duration, what string, pred func(chatSession) bool) chatSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	var last string
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("within %s the websocket never delivered a `chat` message for fleet %s with %s (last: %s): %v", d, fleet, what, last, err)
		}
		var m chatSession
		if json.Unmarshal(data, &m) != nil || m.Type != "chat" {
			continue
		}
		if m.Fleet != fleet {
			continue
		}
		last = string(data)
		if pred(m) {
			return m
		}
	}
}

// The history is Admiral's: it lands in the fleet's own log, survives New chat
// and a restart, and a re-read transcript adds nothing.
func TestChatHistory_IsKeptInTheFleetsLog(t *testing.T) {
	t.Parallel()
	w, chats := newFleetsWorld(t, chatCue, fleetSpec{key: "harbor", model: "opus", effort: "high"})
	f := chats["harbor"]
	appendLines(t, f.transcript("t1"),
		userLine("u-1", "Where are the ships", nil),
		assistantLine("a-1", "msg_1", map[string]any{"type": "text", "text": "Two are afloat."}),
	)
	w.start()
	f.start()
	want := []string{"user: Where are the ships", "assistant: Two are afloat."}
	f.waitSession(10*time.Second, "the transcript's messages", messagesAre(want...))

	t.Run("messages land in the fleet's log", func(t *testing.T) {
		f.at(t).waitLog("the two messages once each", func(m []string, _, _ int) bool { return sameStrings(m, want) })
	})

	t.Run("a question is a card in the log", func(t *testing.T) {
		f := f.at(t)
		p := f.w.botStart(f.w.botEnvFor("harbor", "ADMIRAL_PORT="+f.w.in.Port), askStdin("Where does it go?"), "chat", "ask")
		waitFleetQuestions(t, f, p, 1)
		f.waitLog("a question card", func(_ []string, _, q int) bool { return q == 1 })
		for _, e := range f.logEntries() {
			if e["kind"] == "question" && !strings.Contains(jsonLine(e), "Where does it go?") {
				t.Errorf("the question card %v does not carry the question's title", e)
			}
		}
		_ = p.cmd.Process.Kill()
		<-p.done
	})

	t.Run("New chat adds a marker and keeps the history", func(t *testing.T) {
		f := f.at(t)
		f.newChat()
		f.waitLog("a marker after the two messages", func(m []string, r, _ int) bool { return r == 1 && sameStrings(m, want) })
		if s := f.waitSession(5*time.Second, "the history and one marker", func(s chatSession) bool { return resets(s.Messages) == 1 }); !messagesAre(want...)(s) {
			t.Errorf("after New chat the session serves %v, want the earlier history %v", texts(s.Messages), want)
		}
		f.touch("go-clear")
		appendLines(t, f.transcript("t2"), userLine("u-2", "After the reset", nil))
		f.waitLog("the new message after the marker", func(m []string, r, _ int) bool {
			return r == 1 && sameStrings(m, append(append([]string{}, want...), "user: After the reset"))
		})
	})

	t.Run("a restarted Admiral serves the history without duplicates", func(t *testing.T) {
		f := f.at(t)
		all := append(append([]string{}, want...), "user: After the reset")
		before := len(f.logEntries())
		f.w.restart()
		s := f.waitSession(10*time.Second, "the history", func(s chatSession) bool { return messagesAre(all...)(s) && resets(s.Messages) == 1 })
		if !uniqueIDs(s.Messages) {
			t.Errorf("the restarted session serves a message twice: %v", s.Messages)
		}
		holdThat(t, "the log grew while the restarted Admiral re-read the transcript", func() bool { return len(f.logEntries()) == before })
		if msgs, r, _ := f.logTexts(); !sameStrings(msgs, all) || r != 1 {
			t.Errorf("after the restart the log holds %v and %d markers, want %v and one", msgs, r, all)
		}
	})
}

func waitFleetQuestions(t *testing.T, f *chatDock, p *botProc, n int) botSession {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		code, body := send(t, http.MethodGet, f.url("session"), nil)
		if code != http.StatusOK {
			t.Fatalf("GET /api/chat/%s/session = %d: %s", f.fleet, code, body)
		}
		var s botSession
		if err := json.Unmarshal([]byte(body), &s); err != nil {
			t.Fatalf("session is not JSON: %v", err)
		}
		if len(s.Questions) == n {
			return s
		}
		if p != nil {
			p.failIfExited("the question")
		}
		if time.Now().After(deadline) {
			t.Fatalf("within 10s GET /api/chat/%s/session never showed %d questions; last: %s", f.fleet, n, body)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// Two fleets, two chats: a message, a log and a question belong to one fleet.
func TestChatFleets_TwoChatsStayApart(t *testing.T) {
	t.Parallel()
	w, chats := newFleetsWorld(t, chatCue,
		fleetSpec{key: "alpha", model: "opus", effort: "high"},
		fleetSpec{key: "beta", model: "haiku", effort: ""})
	a, b := chats["alpha"], chats["beta"]
	w.start()
	ws := dialWS(t, w.in.instance)
	a.start()
	b.start()

	t.Run("each chat runs in its own directory with its own settings", func(t *testing.T) {
		w := w.at(t)
		ra, rb := w.record(a.sessionName()), w.record(b.sessionName())
		if ra.cwd != a.dir() || rb.cwd != b.dir() {
			t.Errorf("cwd = %q and %q, want %q and %q", ra.cwd, rb.cwd, a.dir(), b.dir())
		}
		if !hasPair(ra.argv, "--model", "opus") || !hasPair(ra.argv, "--effort", "high") {
			t.Errorf("alpha's argv = %q, want --model opus --effort high", ra.argv)
		}
		if !hasPair(rb.argv, "--model", "haiku") || has(rb.argv, "--effort") {
			t.Errorf("beta's argv = %q, want --model haiku and no --effort", rb.argv)
		}
		if ra.env["ADMIRAL_CHAT_FLEET"] != "alpha" || rb.env["ADMIRAL_CHAT_FLEET"] != "beta" {
			t.Errorf("ADMIRAL_CHAT_FLEET = %q and %q, want each chat's own fleet", ra.env["ADMIRAL_CHAT_FLEET"], rb.env["ADMIRAL_CHAT_FLEET"])
		}
		for _, f := range []*chatDock{a, b} {
			skill := filepath.Join(f.dir(), ".claude", "skills", "admiralbot", "SKILL.md")
			if fi, err := os.Lstat(skill); err != nil || !fi.Mode().IsRegular() {
				t.Errorf("%s: %v, want the fleet's own skill", skill, err)
			}
		}
		if _, err := os.Lstat(filepath.Join(w.home, "chat", ".claude")); err == nil {
			t.Errorf("chat/.claude exists: the skill lives under chat/<fleet>/")
		}
	})

	t.Run("neither is a ship and both open in the bridge", func(t *testing.T) {
		w := w.at(t)
		for _, s := range w.in.ships(t) {
			if strings.HasPrefix(s.Session, "_chat") {
				t.Errorf("%s is in the ships list: %+v", s.Session, s)
			}
		}
		for _, f := range []*chatDock{a, b} {
			br, _ := w.joinBridge(t, f.sessionName(), 80, 24)
			br.close()
		}
		dir, rec := w.installFakeTmux()
		r := w.admiralWith(cliOpts{path: []string{dir}, env: []string{"FAKE_TMUX_RECORD=" + rec}}, "attach", b.sessionName())
		w.expectExit(r, 1, "attach "+b.sessionName())
		if got := attachCalls(w, rec); len(got) != 0 {
			t.Errorf("a refused attach still ran tmux attach: %q", got)
		}
	})

	t.Run("a message and its reply belong to one fleet", func(t *testing.T) {
		a, b := a.at(t), b.at(t)
		code, body := send(t, http.MethodPost, a.url("messages"), map[string]any{"text": "to alpha only"})
		if code < 200 || code > 299 {
			t.Fatalf("POST /api/chat/alpha/messages = %d: %s", code, body)
		}
		deadline := time.Now().Add(5 * time.Second)
		for !strings.Contains(a.stdin(), "to alpha only") {
			if time.Now().After(deadline) {
				t.Fatalf("within 5s alpha's pane never received the message; it has %q", a.stdin())
			}
			time.Sleep(50 * time.Millisecond)
		}
		appendLines(t, a.transcript("t1"), userLine("a-u1", "to alpha only", nil), assistantLine("a-a1", "msg_a", map[string]any{"type": "text", "text": "Alpha heard."}))
		appendLines(t, b.transcript("t1"), userLine("b-u1", "beta's own talk", nil))

		a.waitSession(10*time.Second, "alpha's messages", messagesAre("user: to alpha only", "assistant: Alpha heard."))
		b.waitSession(10*time.Second, "beta's message", messagesAre("user: beta's own talk"))
		waitFleetWS(t, ws, "alpha", 5*time.Second, "alpha's messages", messagesAre("user: to alpha only", "assistant: Alpha heard."))
		waitFleetWS(t, ws, "beta", 5*time.Second, "beta's message", messagesAre("user: beta's own talk"))
		a.waitLog("alpha's two messages", func(m []string, _, _ int) bool {
			return sameStrings(m, []string{"user: to alpha only", "assistant: Alpha heard."})
		})
		b.waitLog("beta's one message", func(m []string, _, _ int) bool { return sameStrings(m, []string{"user: beta's own talk"}) })
		if strings.Contains(b.stdin(), "to alpha only") {
			t.Errorf("beta's pane received alpha's message: %q", b.stdin())
		}
	})

	t.Run("a question is listed and answerable on its own fleet only", func(t *testing.T) {
		a, b := a.at(t), b.at(t)
		pa := a.w.botStart(a.w.botEnvFor("alpha", "ADMIRAL_PORT="+a.w.in.Port), askStdin("Alpha asks"), "chat", "ask")
		sa := waitFleetQuestions(t, a, pa, 1)
		id := qStr(sa.Questions[0], "id")
		if got := qStr(sa.Questions[0], "session"); got != a.sessionName() {
			t.Errorf("the question's session = %q, want %q", got, a.sessionName())
		}
		if files := dirNames(a.questionsDir()); len(files) == 0 {
			t.Errorf("alpha's questions directory is empty: %v", files)
		}
		holdThat(t, "beta lists or holds alpha's question", func() bool {
			return len(fetchBotFleet(t, b).Questions) == 0 && len(dirNames(b.questionsDir())) == 0
		})
		if code, body := send(t, http.MethodPost, b.url("questions/"+id+"/answer"), botAnswer("Freeform")); code != http.StatusNotFound {
			t.Errorf("answering alpha's question on beta = %d, want 404: %s", code, body)
		}
		if pa.exited() {
			t.Fatalf("alpha's chat ask ended after an answer on the wrong fleet")
		}
		// Beta's New chat withdraws beta's questions and leaves alpha's.
		pb := b.w.botStart(b.w.botEnvFor("beta", "ADMIRAL_PORT="+b.w.in.Port), askStdin("Beta asks"), "chat", "ask")
		waitFleetQuestions(t, b, pb, 1)
		b.newChat()
		b.w.expectExit(pb.wait(10*time.Second), 4, "beta's chat ask when beta's New chat withdrew its question")
		if got := fetchBotFleet(t, a).ids(); len(got) != 1 || got[0] != id {
			t.Errorf("after beta's New chat alpha lists %v, want its question %s", got, id)
		}
		if code, body := send(t, http.MethodPost, a.url("questions/"+id+"/answer"), botAnswer("Work queue")); code/100 != 2 {
			t.Fatalf("answering on alpha = %d: %s", code, body)
		}
		a.w.expectExit(pa.wait(10*time.Second), 0, "alpha's chat ask after its answer")
	})
}

func fetchBotFleet(t *testing.T, f *chatDock) botSession {
	t.Helper()
	code, body := send(t, http.MethodGet, f.url("session"), nil)
	if code != http.StatusOK {
		t.Fatalf("GET /api/chat/%s/session = %d: %s", f.fleet, code, body)
	}
	var s botSession
	if err := json.Unmarshal([]byte(body), &s); err != nil {
		t.Fatalf("session is not JSON: %v", err)
	}
	return s
}

func dirNames(dir string) []string {
	entries, _ := os.ReadDir(dir)
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

// An unknown fleet and a fleet whose chat is off are refused with the reason;
// the global routes are gone; the old single-chat layout is cleared at startup.
func TestChatFleets_RoutesNameTheFleetAndRefuseTheRest(t *testing.T) {
	t.Parallel()
	w, chats := newFleetsWorld(t, chatCue,
		fleetSpec{key: "alpha", model: "opus"},
		fleetSpec{key: "nodock", codexOnly: true},
		fleetSpec{key: "Harbor", model: "opus"},
		fleetSpec{key: "harbor", model: "opus"},
		fleetSpec{key: "questions", model: "opus"})
	// The layout of the single chat: a question left by the old chat, and its skill.
	old := filepath.Join(w.home, "chat", "questions")
	atomicWrite(t, filepath.Join(old, "old-1.json"), jsonLine(map[string]any{"version": 1, "id": "old-1", "kind": "question",
		"created": "2026-10-22T10:00:00Z", "title": "Left by the old chat", "session": "_chat"}))
	atomicWrite(t, filepath.Join(w.home, "chat", ".claude", "skills", "admiralbot", "SKILL.md"), "old skill")
	w.start()

	t.Run("the global chat routes are gone", func(t *testing.T) {
		for _, r := range []struct{ method, path string }{
			{http.MethodGet, "/api/chat/session"}, {http.MethodPost, "/api/chat/start"},
			{http.MethodPost, "/api/chat/messages"}, {http.MethodPost, "/api/chat/new"},
		} {
			if code, body := send(t, r.method, w.in.LocalURL+r.path, map[string]any{"text": "x"}); code != http.StatusNotFound && code != http.StatusMethodNotAllowed {
				t.Errorf("%s %s = %d, want 404: %s", r.method, r.path, code, body)
			}
		}
	})

	t.Run("a fleet's chat is mirrored under /api/v1", func(t *testing.T) {
		if code, body := send(t, http.MethodGet, w.in.LocalURL+"/api/v1/chat/alpha/session", nil); code != http.StatusOK {
			t.Errorf("GET /api/v1/chat/alpha/session = %d, want 200: %s", code, body)
		}
	})

	t.Run("an unknown fleet is refused", func(t *testing.T) {
		for _, r := range []struct{ method, tail string }{{http.MethodGet, "session"}, {http.MethodPost, "start"}, {http.MethodPost, "messages"}, {http.MethodPost, "new"}} {
			if code, body := send(t, r.method, w.in.LocalURL+"/api/chat/nowhere/"+r.tail, map[string]any{"text": "x"}); code != http.StatusNotFound {
				t.Errorf("%s /api/chat/nowhere/%s = %d, want 404: %s", r.method, r.tail, code, body)
			}
		}
	})

	t.Run("a fleet whose chat is off is refused with the reason", func(t *testing.T) {
		code, body := send(t, http.MethodPost, w.in.LocalURL+"/api/chat/nodock/start", nil)
		if code != http.StatusConflict || !strings.Contains(errMessage(body), "no Claude Code dock") {
			t.Errorf("POST /api/chat/nodock/start = %d %s, want 409 with the reason from the config", code, body)
		}
		if code, _ := send(t, http.MethodGet, w.in.LocalURL+"/api/chat/nodock/session", nil); code != http.StatusConflict {
			t.Errorf("GET /api/chat/nodock/session = %d, want 409", code)
		}
	})

	t.Run("a key that differs only in case has its chat off", func(t *testing.T) {
		code, body := send(t, http.MethodPost, w.in.LocalURL+"/api/chat/harbor/start", nil)
		if code != http.StatusConflict || !strings.Contains(strings.ToLower(errMessage(body)), "case") {
			t.Errorf("POST /api/chat/harbor/start = %d %s, want 409 saying the key differs only in case from an earlier fleet's", code, body)
		}
		if code, body := send(t, http.MethodPost, w.in.LocalURL+"/api/chat/Harbor/start", nil); code != http.StatusOK {
			t.Errorf("POST /api/chat/Harbor/start = %d, want 200 for the earlier fleet: %s", code, body)
		}
	})

	t.Run("the old layout is cleared and a fleet keyed questions starts clean", func(t *testing.T) {
		q := chats["questions"].at(t)
		for _, p := range []string{filepath.Join(w.home, "chat", ".claude"), filepath.Join(old, "old-1.json")} {
			if _, err := os.Lstat(p); err == nil {
				t.Errorf("%s survived startup", p)
			}
		}
		q.start()
		if got := fetchBotFleet(t, q).Questions; len(got) != 0 {
			t.Errorf("the fleet keyed questions serves %d questions, want none: the old chat's are withdrawn", len(got))
		}
		if _, err := os.Stat(q.dir()); err != nil {
			t.Errorf("chat/questions is not the fleet's directory: %v", err)
		}
	})
}
