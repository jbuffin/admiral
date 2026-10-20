package e2e

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

// Launching a ship from a dock without an order (#102): the launch request
// names no source and no order_id, and its skill kind is none. The prompt is
// the trimmed text; empty text means no prompt argument at all.

// dockLaunch builds a request that names no order.
func (w *world) dockLaunch(harness, repo, text string) map[string]any {
	return map[string]any{"fleet": "work", "harness": harness, "dir": w.checkout(repo), "skill": noSkill, "text": text}
}

// withTitle is a ship as the API lists it, with the fields this file reads.
type withTitle struct {
	Session string  `json:"session"`
	Title   string  `json:"title"`
	Skill   *string `json:"skill"`
	Source  string  `json:"source"`
	OrderID string  `json:"order_id"`
}

func (w *world) shipWithTitle(session string) withTitle {
	w.t.Helper()
	code, body := send(w.t, http.MethodGet, w.in.LocalURL+"/api/ships", nil)
	if code != http.StatusOK {
		w.t.Fatalf("GET /api/ships = %d: %s", code, body)
	}
	var wrap struct {
		Ships []withTitle `json:"ships"`
	}
	if err := json.Unmarshal([]byte(body), &wrap); err != nil {
		w.t.Fatalf("GET /api/ships is not JSON: %v\n%s", err, body)
	}
	for _, s := range wrap.Ships {
		if s.Session == session {
			return s
		}
	}
	w.t.Fatalf("GET /api/ships lists no ship %s: %s", session, body)
	return withTitle{}
}

// A3: the text is the prompt as one argument, trimmed, unguarded; the title is its first line.
func TestDockLaunchPrompt(t *testing.T) {
	w := newWorld(t)
	w.writeCue("exit 3")
	w.start()
	cases := []struct{ name, text, prompt, title string }{
		{"trimmed, newlines kept", "\n  fix the flaky test\nsecond line \n", "fix the flaky test\nsecond line", "fix the flaky test"},
		{"a slash is not guarded", "/review it", "/review it", "/review it"},
		{"a dollar sign is not guarded", "$review it", "$review it", "$review it"},
		{"a dash is one argument", "--help -x", "--help -x", "--help -x"},
	}
	n := 0
	for _, c := range cases {
		for _, harness := range []string{"claude", "codex", "copilot"} {
			n++
			repo := "dl" + string(rune('a'+n%26)) + string(rune('a'+n/26))
			t.Run(c.name+"/"+harness, func(t *testing.T) {
				w := w.at(t)
				ship := w.launch(w.dockLaunch(harness, repo, c.text))
				if got := promptOf(t, w, w.record(ship.Session)); got != c.prompt {
					t.Errorf("prompt = %q, want %q", got, c.prompt)
				}
				got := w.shipWithTitle(ship.Session)
				if got.Title != c.title {
					t.Errorf("title = %q, want %q", got.Title, c.title)
				}
				if got.Skill != nil && *got.Skill != "" || got.Source != "" || got.OrderID != "" {
					t.Errorf("a dock launch has no order and no skill, got skill %v source %q order_id %q", got.Skill, got.Source, got.OrderID)
				}
			})
		}
	}
}

// A4: empty or whitespace-only text starts the harness with no prompt argument.
func TestDockLaunchWithoutText(t *testing.T) {
	w := newWorld(t)
	w.writeCue("exit 3")
	w.start()
	for label, text := range map[string]string{"empty": "", "whitespace": " \n\t "} {
		for _, harness := range []string{"claude", "codex", "copilot"} {
			repo := "dn-" + label + "-" + harness
			t.Run(harness+"/"+label, func(t *testing.T) {
				w := w.at(t)
				ship := w.launch(w.dockLaunch(harness, repo, text))
				r := w.record(ship.Session)
				switch harness {
				case "claude":
					want := []string{"--fa-claude", "--settings", filepath.Join(w.home, "harness", "claude-settings.json")}
					if !reflect.DeepEqual(r.argv, want) {
						t.Errorf("claude argv = %q, want exactly %q", r.argv, want)
					}
				case "copilot":
					want := []string{"--fa-copilot", "--plugin-dir", filepath.Join(w.home, "harness", "copilot")}
					if !reflect.DeepEqual(r.argv, want) {
						t.Errorf("copilot argv = %q, want exactly %q", r.argv, want)
					}
				default:
					if slices.Contains(r.argv, "--") {
						t.Errorf("codex argv has a -- with no prompt: %q", r.argv)
					}
					if len(r.argv) < 2 || r.argv[len(r.argv)-2] != "-c" {
						t.Errorf("codex argv must end after Admiral's last -c flag: %q", r.argv)
					}
				}
				if got := w.shipWithTitle(ship.Session); got.Title != "" {
					t.Errorf("title = %q, want empty", got.Title)
				}
			})
		}
	}
}

// A5: a launch with no order that names a skill is refused, and nothing starts.
func TestDockLaunchRefusesASkill(t *testing.T) {
	w := newWorld(t)
	w.writeCue("exit 3")
	w.start()
	w.launch(w.dockLaunch("claude", "dr-ok", "control")) // the endpoint works, so a refusal isn't a missing route
	for name, skill := range map[string]map[string]any{"commodore": commodore, "named skill": customSkill("foo")} {
		t.Run(name, func(t *testing.T) {
			w := w.at(t)
			req := w.dockLaunch("claude", "dr-"+name, "hello")
			req["skill"] = skill
			before := w.sessions()
			code, body := w.post(req)
			requireRefusal(t, code, body)
			if after := w.sessions(); !reflect.DeepEqual(before, after) {
				t.Errorf("a refused launch changed the sessions: before %v, after %v", before, after)
			}
		})
	}
}

// A6: a dock launch makes its directory the dock's last one, with or without text.
func TestDockLaunchRemembersTheDirectory(t *testing.T) {
	d := newDataWorld(t)
	d.writeCue("exit 3")
	a := d.checkout("la")
	b := d.checkout("lb")
	d.start()
	if r := d.dirs("work", "claude", "", ""); r.selected() != "" {
		t.Fatalf("a fresh dock pre-selected %q", r.selected())
	}
	for i, text := range []string{"fix the flaky test", ""} {
		dir := []string{a, b}[i]
		d.launch(map[string]any{"fleet": "work", "harness": "claude", "dir": dir, "skill": noSkill, "text": text})
		if r := d.dirs("work", "claude", "", ""); r.selected() != dir || r.reason() != "last_used" {
			t.Errorf("after a dock launch with text %q: selected %q (%s), want %q (last_used)", text, r.selected(), r.reason(), dir)
		}
	}
}
