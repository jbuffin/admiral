package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A ship launched blank gains its title once the engineer sends the first prompt: a pull request's own title when the
// prompt links one, else Claude Code's name for the session. The link the prompt is about becomes the ship's link.
func TestShipTitleFromItsFirstPrompt(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	gh := w.newFakeGH()
	gh.token("", "fake-token-work-2")
	gh.write("search/title.match", "resource(url:")
	gh.write("search/title.json", `{"data":{"r":{"title":"Fix the login flake"}}}`)
	cfgDir := filepath.Join(w.root, "cfg")
	if err := os.MkdirAll(filepath.Join(cfgDir, "projects", "zz"), 0o755); err != nil {
		t.Fatal(err)
	}
	w.writeCue("wait never") // the ships stay underway; the test writes their transcripts itself
	env := w.harnessEnv()
	env["CLAUDE_CONFIG_DIR"] = cfgDir
	cfg, err := json.Marshal(map[string]any{"version": 1, "fleets": map[string]any{
		"work": map[string]any{"name": "Work", "search_roots": []string{w.work},
			"claude": map[string]any{"executable": w.fakeExe("claude"), "env": env}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	writeConfig(t, w.home, string(cfg))
	w.start()

	blank := func(repo string) (shipJSON, string) {
		s := w.launch(map[string]any{"fleet": "work", "harness": "claude", "dir": w.checkout(repo), "skill": noSkill, "text": ""})
		var id string
		argv := w.record(s.Session).argv
		for i, a := range argv {
			if a == "--session-id" && i+1 < len(argv) {
				id = argv[i+1]
			}
		}
		if id == "" {
			t.Fatalf("ship %s launched with no --session-id", s.Session)
		}
		return s, filepath.Join(cfgDir, "projects", "zz", id+".jsonl")
	}
	pr, prTranscript := blank("pr")
	docs, docsTranscript := blank("docs")

	for _, s := range []shipJSON{pr, docs} {
		if s.Title != "" || s.Link != "" {
			t.Errorf("blank ship %s launched with title %q and link %q, want neither", s.Session, s.Title, s.Link)
		}
	}

	const prURL = "https://github.com/acme/widgets/pull/42"
	appendLines(t, prTranscript, userLine("u-1", "Review "+prURL+" and leave comments", nil))
	appendLines(t, docsTranscript, userLine("u-1", "Read https://docs.example.com/setup and tidy the setup page", nil),
		jsonLine(map[string]any{"type": "ai-title", "aiTitle": "Tidy the setup page", "sessionId": "x"}))

	list := w.in.waitShipsList(t, 30*time.Second, "both ships titled", func(l []shipJSON) bool {
		a, _ := shipIn(l, pr.Session)
		b, _ := shipIn(l, docs.Session)
		return a.Title != "" && !a.Titling && b.Title != "" && !b.Titling
	})
	got, _ := shipIn(list, pr.Session)
	if got.Title != "Fix the login flake" || got.Link != prURL || got.URL != "" {
		t.Errorf("ship with a PR in its prompt: title %q, link %q, url %q; want the PR's title, the PR as its link and no url",
			got.Title, got.Link, got.URL)
	}
	got, _ = shipIn(list, docs.Session)
	if got.Title != "Tidy the setup page" || got.Link != "https://docs.example.com/setup" {
		t.Errorf("ship with a docs link: title %q, link %q; want Claude's title and the docs link", got.Title, got.Link)
	}
}
