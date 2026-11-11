package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/coder/websocket"
)

type crewConvWorld struct {
	*world
	cfgDir, other, sess string
}

// crewConvSetup starts a Claude ship whose fleet's Claude and Codex docks share one config dir.
func crewConvSetup(t *testing.T) *crewConvWorld {
	t.Helper()
	w := newWorld(t)
	cfgDir := filepath.Join(w.root, "cfg")
	if err := os.MkdirAll(filepath.Join(cfgDir, "projects", "zz"), 0o755); err != nil {
		t.Fatal(err)
	}
	env := w.harnessEnv()
	env["CLAUDE_CONFIG_DIR"] = cfgDir
	cfg, err := json.Marshal(map[string]any{"version": 1, "fleets": map[string]any{
		"work": map[string]any{"name": "Work", "search_roots": []string{w.work},
			"claude": map[string]any{"executable": w.fakeExe("claude"), "env": env},
			"codex":  map[string]any{"executable": w.fakeExe("codex"), "env": env}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	writeConfig(t, w.home, string(cfg))
	w.writeCue("wait go", "exit 0")
	w.start()
	ship := w.launch(w.order("work", "claude", "alpha", noSkill, "go"))
	other := filepath.Join(w.root, "anywhere")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	return &crewConvWorld{world: w, cfgDir: cfgDir, other: other, sess: ship.Session}
}

func (c *crewConvWorld) boat(name, harness string, extra ...string) string {
	c.t.Helper()
	args := append([]string{"launch", "--name", name, "--harness", harness, "--dir", c.other}, extra...)
	c.expectExit(crewCall(c.world, c.sess, "", append(args, "--", "go")...), 0, "crew launch "+name)
	c.waitFile(c.sess+"/crew/"+name, "recorded")
	return c.sess + "/crew/" + name
}

func (c *crewConvWorld) setTranscript(name, path string) {
	c.t.Helper()
	c.mustTmux("set", "-p", "-t", c.paneOf(c.sess, name).pane, crewTranscriptOption, path)
	holdFor(c.t, "crew list to show the transcript of "+name, func() bool {
		got, set := transcriptField(c.world, c.sess, name)
		return set && got == path
	})
}

func (c *crewConvWorld) convURL(name string) string {
	return c.in.LocalURL + "/api/ships/" + url.PathEscape(c.sess) + "/crew/" + url.PathEscape(name) + "/conversation"
}

func (c *crewConvWorld) page(name string) (int, convPage) {
	c.t.Helper()
	code, body := send(c.t, http.MethodGet, c.convURL(name), nil)
	var p convPage
	_ = json.Unmarshal([]byte(body), &p)
	return code, p
}

func (c *crewConvWorld) shipRow() map[string]json.RawMessage {
	c.t.Helper()
	return fetchShipRaw(c.t, c.in, c.sess)
}

func fetchShipRaw(t *testing.T, in *fleetInst, session string) map[string]json.RawMessage {
	t.Helper()
	for _, s := range fetchShips(t, in) {
		if s.Session == session {
			return s.raw
		}
	}
	t.Fatalf("GET /api/ships lists no %s", session)
	return nil
}

type apiCrewRow struct {
	Name         string          `json:"name"`
	Role         *string         `json:"role"`
	Conversation bool            `json:"conversation"`
	Transcript   json.RawMessage `json:"transcript"`
}

func (c *crewConvWorld) crewRows() map[string]apiCrewRow {
	c.t.Helper()
	var rows []apiCrewRow
	if err := json.Unmarshal(c.shipRow()["crew"], &rows); err != nil {
		c.t.Fatalf("ship crew is not a list of boats: %v", err)
	}
	out := map[string]apiCrewRow{}
	for _, r := range rows {
		out[r.Name] = r
	}
	return out
}

// A boat's role and whether it has a conversation come with the ship; the recorded
// transcript path itself stays off the API.
func TestCrewConversation_RoleAndFlagOnShipsAPI(t *testing.T) {
	t.Parallel()
	c := crewConvSetup(t)
	c.boat("rev", "claude", "--role", "reviewer")
	c.boat("plain", "claude")
	c.boat("cx", "codex")
	c.boat("fresh", "claude", "--role", "tester")
	claudePath := filepath.Join(c.cfgDir, "projects", "zz", "11111111-1111-4111-8111-111111111111.jsonl")
	appendLines(t, claudePath, userLine("u-1", "hello", nil))
	c.setTranscript("rev", claudePath)
	c.setTranscript("plain", claudePath)
	c.setTranscript("cx", "/Users/me/.codex/sessions/2026/10/25/rollout-1.jsonl")

	c.waitShip(c.sess, "crew rows with role", func(s apiShip) bool {
		var rows []apiCrewRow
		return json.Unmarshal(s.raw["crew"], &rows) == nil && len(rows) == 4
	})
	rows := c.crewRows()
	if r := rows["rev"]; r.Role == nil || *r.Role != "reviewer" {
		t.Errorf("rev role = %v, want reviewer", r.Role)
	}
	if r := rows["plain"]; r.Role != nil && *r.Role != "" {
		t.Errorf("plain role = %q, want none (null or absent)", *r.Role)
	}
	for name, want := range map[string]bool{"rev": true, "plain": true, "cx": false, "fresh": false} {
		if got := rows[name].Conversation; got != want {
			t.Errorf("%s conversation flag = %v, want %v (true only for a Claude boat with a recorded transcript)", name, got, want)
		}
	}
	for name, r := range rows {
		if len(r.Transcript) > 0 && string(r.Transcript) != "null" {
			t.Errorf("%s: the ships API exposes the transcript path %s, want it left off", name, r.Transcript)
		}
	}
	if strings.Contains(c.shipRowText(), ".jsonl") {
		t.Errorf("the ships API row holds a transcript path: %s", c.shipRowText())
	}
}

func (c *crewConvWorld) shipRowText() string {
	b, _ := json.Marshal(c.shipRow())
	return string(b)
}

type crewWSMsg struct {
	Type  string     `json:"type"`
	Ship  string     `json:"ship"`
	Crew  string     `json:"crew"`
	Items []convItem `json:"items"`
}

// A boat's conversation reads over HTTP, streams a later append to the client that
// opened the boat, reads after the boat exited, and ends with the boat's window.
func TestCrewConversation_ReadStreamExitAndClose(t *testing.T) {
	t.Parallel()
	c := crewConvSetup(t)
	boat := c.boat("rev", "claude", "--role", "reviewer")
	path := filepath.Join(c.cfgDir, "projects", "zz", "22222222-2222-4222-8222-222222222222.jsonl")
	appendLines(t, path,
		userLine("u-1", "please review", nil),
		assistantLine("a-1", "m-1", map[string]any{"type": "text", "text": "looking now"}))
	c.setTranscript("rev", path)

	var p convPage
	holdFor(t, "the boat's conversation to read ok", func() bool {
		var code int
		code, p = c.page("rev")
		return code == http.StatusOK && p.State == "ok" && len(p.Items) == 2
	})
	if p.Items[0].Text != "please review" || p.Items[1].Text != "looking now" {
		t.Errorf("items = %+v, want the user line then the assistant line", p.Items)
	}
	if code, _ := c.page("nobody"); code != http.StatusNotFound {
		t.Errorf("unknown boat = %d, want 404", code)
	}

	conn := dialWS(t, c.in.instance)
	msgs := make(chan crewWSMsg, 16)
	go func() {
		for {
			_, data, err := conn.Read(context.Background())
			if err != nil {
				return
			}
			var m crewWSMsg
			if json.Unmarshal(data, &m) == nil && (m.Type == "conversation" || m.Type == "conversation_opened" || m.Type == "conversation_close") {
				msgs <- m
			}
		}
	}()
	open, _ := json.Marshal(map[string]string{"type": "conversation_open", "ship": c.sess, "crew": "rev"})
	if err := conn.Write(context.Background(), websocket.MessageText, open); err != nil {
		t.Fatal(err)
	}
	select {
	case m := <-msgs:
		if m.Type != "conversation_opened" || m.Ship != c.sess || m.Crew != "rev" {
			t.Fatalf("first message = %+v, want conversation_opened for the ship and crew rev", m)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("within 10s the crew open was never answered with conversation_opened")
	}
	appendLines(t, path, userLine("u-2", "and the tests?", nil))
	select {
	case m := <-msgs:
		if m.Type != "conversation" || m.Crew != "rev" || len(m.Items) != 1 || m.Items[0].Text != "and the tests?" {
			t.Fatalf("pushed = %+v, want one conversation message for crew rev holding the new line", m)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("within 10s the opener never got the appended line")
	}

	// Anchored: the boat's process is gone and its conversation still reads.
	pid, err := strconv.Atoi(c.paneOf(c.sess, "rev").pid)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	waitCrewState(c.world, c.sess, "rev", "anchored")
	if code, p := c.page("rev"); code != http.StatusOK || p.State != "ok" || len(p.Items) != 3 {
		t.Errorf("anchored boat: %d state %q with %d items, want 200 ok with 3", code, p.State, len(p.Items))
	}

	// Closing the boat ends the subscription: a later line is not pushed.
	c.expectExit(crewCall(c.world, c.sess, "", "close", "rev"), 0, "crew close rev")
	holdFor(t, "the closed boat's conversation to be 404", func() bool {
		code, _ := c.page("rev")
		return code == http.StatusNotFound
	})
	for len(msgs) > 0 {
		<-msgs
	}
	appendLines(t, path, userLine("u-3", "after close", nil))
	holdThat(t, "a closed boat's subscription still pushed a line", func() bool {
		for len(msgs) > 0 {
			if m := <-msgs; m.Type == "conversation" && m.Crew == "rev" {
				return false
			}
		}
		return true
	})
	_ = boat
}

// A Codex boat has no adapter: its conversation reads none.
func TestCrewConversation_NonClaudeBoatReadsNone(t *testing.T) {
	t.Parallel()
	c := crewConvSetup(t)
	c.boat("cx", "codex")
	path := filepath.Join(c.cfgDir, "projects", "zz", "33333333-3333-4333-8333-333333333333.jsonl")
	appendLines(t, path, userLine("u-1", "codex text", nil))
	c.setTranscript("cx", path)
	holdThat(t, "a Codex boat's conversation read something other than none", func() bool {
		code, p := c.page("cx")
		return code == http.StatusOK && p.State == "none" && len(p.Items) == 0
	})
}

// The path a boat records comes from a payload the boat controls: whatever it
// names outside the fleet's Claude projects dir is never read.
func TestCrewConversation_PathOutsideProjectsIsNeverRead(t *testing.T) {
	t.Parallel()
	c := crewConvSetup(t)
	c.boat("evil", "claude")
	good := filepath.Join(c.cfgDir, "projects", "zz", "55555555-5555-4555-8555-555555555555.jsonl")
	appendLines(t, good, userLine("u-1", "inside is fine", nil))
	c.setTranscript("evil", good)
	holdFor(t, "a path inside projects to read ok", func() bool {
		code, p := c.page("evil")
		return code == http.StatusOK && p.State == "ok" && len(p.Items) == 1
	})
	outside := filepath.Join(c.root, "outside", "secret.jsonl")
	appendLines(t, outside, userLine("u-1", "TOP-SECRET-OUTSIDE", nil))
	link := filepath.Join(c.cfgDir, "projects", "zz", "44444444-4444-4444-8444-444444444444.jsonl")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	for name, p := range map[string]string{
		"a file outside projects":      outside,
		"a symlink inside projects":    link,
		"a path that climbs out":       filepath.Join(c.cfgDir, "projects", "..", "..", "outside", "secret.jsonl"),
		"a path the hook would refuse": filepath.Join(c.root, "outside", "a;b.jsonl"),
	} {
		c.mustTmux("set", "-p", "-t", c.paneOf(c.sess, "evil").pane, crewTranscriptOption, p)
		holdThat(t, "reading "+name+" returned the outside file's text", func() bool {
			code, body := send(t, http.MethodGet, c.convURL("evil"), nil)
			return code != http.StatusOK || (!strings.Contains(body, "TOP-SECRET-OUTSIDE") && !strings.Contains(body, `"state":"ok"`))
		})
	}
}
