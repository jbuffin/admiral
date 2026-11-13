package routines_test

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestStandingOrderDraftDropsGuidanceTheProseDoesNotHoldAndNamesIt(t *testing.T) {
	r := newSO(t)
	r.answers(map[string]any{"limits": map[string]any{}, "checks": []any{}, "guidance": "Always rewrite it in Rust."})
	_, got, raw := r.draft(draftProse)
	if d := raw["draft"].(map[string]any); d["guidance"] != nil {
		t.Errorf("draft.guidance = %v, want null for text the prose does not hold", d["guidance"])
	}
	if !hasProblem(got, "guidance") || !strings.Contains(got.Problems[0].Message, "not in the text") {
		t.Errorf("problems = %+v, want one on guidance", got.Problems)
	}
}

func TestStandingOrderDraftKeepsGuidanceAcrossWhitespace(t *testing.T) {
	r := newSO(t)
	r.answers(map[string]any{"limits": map[string]any{}, "checks": []any{}, "guidance": "Prefer small PRs."})
	_, got, _ := r.draft("Prefer   small\nPRs.")
	if got.Draft.Guidance == nil || *got.Draft.Guidance != "Prefer small PRs." || len(got.Problems) != 0 {
		t.Errorf("guidance %v, problems %+v; want it kept with no problem", got.Draft.Guidance, got.Problems)
	}
}

func TestCopyThatTheConfigRefusesPutsTheTargetsOrdersFileBackOrRemovesIt(t *testing.T) {
	for name, seed := range map[string]bool{"a target with orders": true, "a target with none": false} {
		t.Run(name, func(t *testing.T) {
			r := newSO(t)
			r.put("work", map[string]any{"checks": []string{"new check"}, "guidance": "new guidance"})
			var before []byte
			if seed {
				r.put("play", map[string]any{"checks": []string{"old check"}, "guidance": "old guidance"})
				before, _ = os.ReadFile(r.file("play"))
			}
			if err := os.WriteFile(filepath.Join(r.home, "config.json"), []byte("{not json"), 0o600); err != nil {
				t.Fatal(err)
			}
			code, body := r.do("POST", "/api/fleets/work/standing-orders/copy", `{"to":"play"}`)
			if code != 409 || body["error"] == nil {
				t.Fatalf("copy = %d %v, want 409 for a config that cannot be edited", code, body)
			}
			after, err := os.ReadFile(r.file("play"))
			switch {
			case seed && !bytes.Equal(before, after):
				t.Errorf("play's file = %s, was %s", after, before)
			case !seed && err == nil:
				t.Errorf("play has a standing-orders.json after a refused copy: %s", after)
			}
			if got, _ := r.store.StandingOrders("play"); seed && !reflect.DeepEqual(got.Checks, []string{"old check"}) {
				t.Errorf("play's checks = %v, want the old ones", got.Checks)
			}
		})
	}
}
