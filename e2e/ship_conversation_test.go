package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type convItem struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

type convPage struct {
	State string     `json:"state"`
	Items []convItem `json:"items"`
	More  bool       `json:"more"`
}

func msg(i int) string { return fmt.Sprintf("message %03d", i) }

func userLines(from, to int) []string {
	var out []string
	for i := from; i <= to; i++ {
		out = append(out, userLine(fmt.Sprintf("u-%03d", i), msg(i), nil))
	}
	return out
}

// A Claude ship launches with its session id and a recorded transcript path; its
// conversation is paged over HTTP, pushed over the WebSocket to whoever opened
// it, and stays readable after the harness exits until the ship is dismissed.
func TestShipConversation_FromLaunchToDismiss(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	cfgDir := filepath.Join(w.root, "cfg")
	// A project directory Admiral does not compute: reads find it by session id.
	if err := os.MkdirAll(filepath.Join(cfgDir, "projects", "zz"), 0o755); err != nil {
		t.Fatal(err)
	}
	w.writeCue("exit 3")
	env := w.harnessEnv()
	env["CLAUDE_CONFIG_DIR"] = cfgDir
	cfg, err := json.Marshal(map[string]any{"version": 1, "fleets": map[string]any{
		"work": map[string]any{"name": "Work", "search_roots": []string{w.work},
			"claude": map[string]any{"executable": w.fakeExe("claude"), "env": env, "args": []string{"--fa-claude"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	writeConfig(t, w.home, string(cfg))
	w.start()
	base := w.in.LocalURL

	s := w.launch(w.order("work", "claude", "a", noSkill, "go"))
	argv := w.record(s.Session).argv
	var id string
	for i, a := range argv {
		if a == "--session-id" && i+1 < len(argv) {
			id = argv[i+1]
		}
	}
	if !uuidRe.MatchString(id) || strings.Count(strings.Join(argv, "\x00"), "--session-id") != 1 {
		t.Fatalf("argv = %q, want one --session-id holding a uuid", argv)
	}
	var opt string
	holdFor(t, "@admiral-transcript to be set", func() bool {
		out, err := w.tmux("show-options", "-pv", "-t", s.Session, "@admiral-transcript")
		opt = strings.TrimSpace(out)
		return err == nil && opt != ""
	})
	if f := strings.Fields(opt); len(f) != 3 || f[1] != "launch" || !strings.Contains(f[2], id) {
		t.Errorf("@admiral-transcript = %q, want \"<unix ns> launch <path with %s>\"", opt, id)
	}

	var caps struct {
		Capabilities []string `json:"capabilities"`
	}
	_, body := send(t, http.MethodGet, base+"/api/version", nil)
	if json.Unmarshal([]byte(body), &caps) != nil || !strings.Contains(strings.Join(caps.Capabilities, ","), "conversation") {
		t.Errorf("GET /api/version = %s, want the conversation capability", body)
	}

	convURL := base + "/api/v1/ships/" + url.PathEscape(s.Session) + "/conversation"
	page := func(query string) convPage {
		_, body := send(t, http.MethodGet, convURL+query, nil)
		var p convPage
		_ = json.Unmarshal([]byte(body), &p)
		return p
	}
	transcript := filepath.Join(cfgDir, "projects", "zz", id+".jsonl")
	appendLines(t, transcript, userLines(1, 250)...)
	var last convPage
	holdFor(t, "the newest page of the conversation", func() bool {
		last = page("")
		return last.State == "ok" && len(last.Items) == 200
	})
	if last.Items[0].Text != msg(51) || last.Items[199].Text != msg(250) || !last.More {
		t.Errorf("newest page = %q..%q more=%v, want %q..%q more=true", last.Items[0].Text, last.Items[199].Text, last.More, msg(51), msg(250))
	}
	if older := page("?before=" + url.QueryEscape(last.Items[0].ID)); len(older.Items) != 50 || older.Items[0].Text != msg(1) || older.More {
		t.Errorf("page before the first item = %d items from %q more=%v, want 50 from %q and more=false", len(older.Items), firstText(older.Items), older.More, msg(1))
	}

	// A pushed append reaches the client that opened the ship, and no other.
	opener, other := dialWS(t, w.in.instance), dialWS(t, w.in.instance)
	got, strays := make(chan []convItem, 8), make(chan struct{}, 8)
	read := func(c *websocket.Conn, into func([]convItem)) {
		for {
			_, data, err := c.Read(context.Background())
			if err != nil {
				return
			}
			var m struct {
				Type  string     `json:"type"`
				Ship  string     `json:"ship"`
				Items []convItem `json:"items"`
			}
			if json.Unmarshal(data, &m) == nil && m.Type == "conversation" && m.Ship == s.Session {
				into(m.Items)
			}
		}
	}
	go read(opener, func(items []convItem) { got <- items })
	go read(other, func([]convItem) { strays <- struct{}{} })
	open, _ := json.Marshal(map[string]string{"type": "conversation_open", "ship": s.Session})
	if err := opener.Write(context.Background(), websocket.MessageText, open); err != nil {
		t.Fatal(err)
	}
	holdThat(t, "opening a ship sent a conversation message by itself", func() bool { return len(got) == 0 })
	appendLines(t, transcript, userLines(251, 251)...)
	select {
	case items := <-got:
		if len(items) != 1 || items[0].Text != msg(251) {
			t.Errorf("pushed items = %+v, want one holding %q", items, msg(251))
		}
	case <-time.After(10 * time.Second):
		t.Fatal("within 10s the opener never got the appended item")
	}
	holdThat(t, "a client that never opened the ship received its conversation", func() bool { return len(strays) == 0 })

	// The fake exits at once, so the ship is anchored: still readable until dismissed.
	w.in.waitShipsList(t, 15*time.Second, "the ship to be anchored", func(l []shipJSON) bool { return statusOf(l, s.Session) == "anchored" })
	if p := page(""); p.State != "ok" || len(p.Items) != 200 || p.Items[199].Text != msg(251) {
		t.Errorf("after the harness exited: state %q with %d items, want ok and the newest 200", p.State, len(p.Items))
	}
	if code, body := send(t, http.MethodPost, base+"/api/ships/"+s.Session+"/dismiss", nil); code >= 300 {
		t.Fatalf("dismiss = %d: %s", code, body)
	}
	holdFor(t, "the dismissed ship's conversation to be 404", func() bool {
		code, _ := send(t, http.MethodGet, convURL, nil)
		return code == http.StatusNotFound
	})
}

func firstText(items []convItem) string {
	if len(items) == 0 {
		return ""
	}
	return items[0].Text
}

// holdFor polls cond for up to 15 s.
func holdFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for end := time.Now().Add(15 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if cond() {
			return
		}
		if time.Now().After(end) {
			t.Fatalf("within 15s: %s never happened", what)
		}
	}
}
