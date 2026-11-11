package e2e

// A Copilot ship's conversation from launch to anchored.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func copilotEventsLine(typ, id, ts, data string) string {
	return fmt.Sprintf(`{"type":%q,"id":%q,"parentId":null,"timestamp":%q,"data":%s}`, typ, id, ts, data)
}

func copilotUserLine(i int) string {
	return copilotEventsLine("user.message", fmt.Sprintf("u-%03d", i), "2026-01-02T03:04:05.000Z", fmt.Sprintf(`{"content":%q}`, msg(i)))
}

// A Copilot ship launches with its session id and a recorded events.jsonl path; its conversation is paged over HTTP and pushed to whoever opened it.
func TestShipConversation_CopilotFromLaunchToAnchored(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	home := filepath.Join(w.root, "copilot-home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	w.writeCue("exit 3")
	env := w.harnessEnv()
	env["COPILOT_HOME"] = home
	cfg, err := json.Marshal(map[string]any{"version": 1, "fleets": map[string]any{
		"work": map[string]any{"name": "Work", "search_roots": []string{w.work},
			"copilot": map[string]any{"executable": w.fakeExe("copilot"), "env": env}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	writeConfig(t, w.home, string(cfg))
	w.start()
	base := w.in.LocalURL

	s := w.launch(w.order("work", "copilot", "a", noSkill, "go"))
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
	transcript := filepath.Join(home, "session-state", id, "events.jsonl")
	var opt string
	holdFor(t, "@admiral-transcript to be set", func() bool {
		out, err := w.tmux("show-options", "-pv", "-t", s.Session, "@admiral-transcript")
		opt = strings.TrimSpace(out)
		return err == nil && opt != ""
	})
	if f := strings.Fields(opt); len(f) != 3 || f[1] != "launch" || f[2] != transcript {
		t.Errorf("@admiral-transcript = %q, want \"<unix ns> launch %s\"", opt, transcript)
	}

	convURL := base + "/api/v1/ships/" + url.PathEscape(s.Session) + "/conversation"
	page := func(query string) convPage {
		_, body := send(t, http.MethodGet, convURL+query, nil)
		var p convPage
		_ = json.Unmarshal([]byte(body), &p)
		return p
	}
	var lines []string
	for i := 1; i <= 250; i++ {
		lines = append(lines, copilotUserLine(i))
	}
	appendLines(t, transcript, lines...)
	var last convPage
	holdFor(t, "the newest page of the conversation", func() bool {
		last = page("")
		return last.State == "ok" && len(last.Items) == 200
	})
	if last.Items[0].Text != msg(51) || last.Items[199].Text != msg(250) || !last.More {
		t.Errorf("newest page = %q..%q more=%v, want %q..%q more=true", last.Items[0].Text, last.Items[199].Text, last.More, msg(51), msg(250))
	}

	opener := dialWS(t, w.in.instance)
	got := make(chan []convItem, 8)
	go func() {
		for {
			_, data, err := opener.Read(context.Background())
			if err != nil {
				return
			}
			var m struct {
				Type  string     `json:"type"`
				Ship  string     `json:"ship"`
				Items []convItem `json:"items"`
			}
			if json.Unmarshal(data, &m) == nil && m.Type == "conversation" && m.Ship == s.Session {
				got <- m.Items
			}
		}
	}()
	open, _ := json.Marshal(map[string]string{"type": "conversation_open", "ship": s.Session})
	if err := opener.Write(context.Background(), websocket.MessageText, open); err != nil {
		t.Fatal(err)
	}
	holdThat(t, "opening a ship sent a conversation message by itself", func() bool { return len(got) == 0 })
	appendLines(t, transcript, copilotUserLine(251))
	select {
	case items := <-got:
		if len(items) != 1 || items[0].Text != msg(251) {
			t.Errorf("pushed items = %+v, want one holding %q", items, msg(251))
		}
	case <-time.After(10 * time.Second):
		t.Fatal("within 10s the opener never got the appended item")
	}

	// The fake exits at once, so the ship is anchored: still readable until dismissed.
	w.in.waitShipsList(t, 15*time.Second, "the ship to be anchored", func(l []shipJSON) bool { return statusOf(l, s.Session) == "anchored" })
	if p := page(""); p.State != "ok" || len(p.Items) != 200 || p.Items[199].Text != msg(251) {
		t.Errorf("after the harness exited: state %q with %d items, want ok and the newest 200", p.State, len(p.Items))
	}
}
