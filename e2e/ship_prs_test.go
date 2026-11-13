package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// prAuthored steers the author and creation time the poll reads for a PR.
func (g *fakeGH) prAuthored(prURL, author string, at time.Time) {
	g.t.Helper()
	if err := os.MkdirAll(filepath.Join(g.dir, "pr_events"), 0o755); err != nil {
		g.t.Fatal(err)
	}
	ev, err := json.Marshal(map[string]any{"author": map[string]any{"__typename": "User", "login": author}, "createdAt": at.UTC().Format(time.RFC3339)})
	if err != nil {
		g.t.Fatal(err)
	}
	g.write("pr_events/"+strings.NewReplacer("/", "_", ":", "_").Replace(prURL), string(ev)+"\n")
}

// A running ship carries the PR its own tool result shows once the fleet's user opened it, and keeps it when it moors.
func TestShipPRs_ARunningShipCarriesThePRItOpenedThroughToMooring(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	g := w.newFakeGH()
	g.token("", "fake-token-f")
	w.checkoutOf(w.work, "widgets", "acme/widgets")
	cfgDir := filepath.Join(w.root, "cfg")
	if err := os.MkdirAll(filepath.Join(cfgDir, "projects", "zz"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cfgDir, "skills", "commodore"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "skills", "commodore", "SKILL.md"), []byte("---\nname: commodore\ndescription: a fake skill\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cue := filepath.Join(w.root, "cue-f")
	if err := os.WriteFile(cue, []byte("hook working\nwait go\nsleep 1\nexit 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := w.harnessEnv()
	env["FAKE_CUE"] = cue
	env["CLAUDE_CONFIG_DIR"] = cfgDir
	cfg := map[string]any{"version": 1, "fleets": map[string]any{"f": map[string]any{"name": "f", "color": "#336699", "search_roots": []string{w.work},
		"claude": map[string]any{"executable": w.fakeExe("claude"), "env": env}}}}
	cfg["sources"] = json.RawMessage(rawSourcesIn(firstFleet(t, cfg), srcDef{Key: "gh", Plugin: "github", Name: "GitHub", Fleet: "f", Settings: map[string]any{"views": []any{}}}))
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	writeConfig(t, w.home, string(b))
	g.prState(mooredPR, "OPEN")
	g.prAuthored(mooredPR, "fake-viewer", time.Now().Add(time.Hour))
	w.start()

	ship := w.launch(w.order("f", "claude", "widgets", commodore, "do it"))
	var id string
	argv := w.record(ship.Session).argv
	for i, a := range argv {
		if a == "--session-id" && i+1 < len(argv) {
			id = argv[i+1]
		}
	}
	if !uuidRe.MatchString(id) {
		t.Fatalf("argv = %q, want a --session-id uuid", argv)
	}
	call, _ := json.Marshal(map[string]any{"type": "assistant", "uuid": "a-1", "message": map[string]any{"content": []map[string]any{
		{"type": "tool_use", "id": "t1", "name": "Bash", "input": map[string]any{"command": "gh pr create"}}}}})
	res, _ := json.Marshal(map[string]any{"type": "user", "uuid": "u-1", "message": map[string]any{"content": []map[string]any{
		{"type": "tool_result", "tool_use_id": "t1", "content": mooredPR + "\n"}}}})
	appendLines(t, filepath.Join(cfgDir, "projects", "zz", id+".jsonl"), string(call), string(res))

	w.kickMoored()
	list := w.in.waitShipsList(t, 20*time.Second, "the running ship carrying the PR it opened", func(l []shipJSON) bool {
		s := byName(l)[ship.Name]
		return s.Status != "moored" && len(s.PRs) == 1 && s.PRs[0].URL == mooredPR
	})
	if got := byName(list)[ship.Name]; got.PRs[0].State != "OPEN" {
		t.Errorf("the running ship's PR = %+v, want it OPEN", got.PRs[0])
	}

	w.exitWith(ship, mooredReport())
	list = w.in.waitShipsList(t, 20*time.Second, "the ship moored with the PR it carried", func(l []shipJSON) bool {
		s := byName(l)[ship.Name]
		return s.Status == "moored" && len(s.PRs) == 1 && s.PRs[0].URL == mooredPR
	})
	if !exists(w.mooredFile(byName(list)[ship.Name].Name)) {
		t.Errorf("no moored record for %s, want the carried PR kept though the report named none", ship.Name)
	}
}
