package routines_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/capabilities"
	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/routines"
	"github.com/jbuffin/admiral/internal/standing"
)

// muteRig is newSO with an event routine in fleet work and one skip entry already in its Limits.
func muteRig(t *testing.T) (*soRig, routines.Routine) {
	t.Helper()
	r := newSO(t)
	b, err := os.ReadFile(filepath.Join(r.home, config.FileName))
	if err != nil {
		t.Fatal(err)
	}
	cfg := strings.Replace(string(b), `"work":{"name":"Work",`, `"work":{"name":"Work","limits":{"skip":{"labels":[{"value":"wip"}]}},`, 1)
	if err := os.WriteFile(filepath.Join(r.home, config.FileName), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	r.cfg.Reload()
	if f, _ := r.cfg.Current().Fleet("work"); len(f.Limits.Skip.Labels) != 1 {
		t.Fatalf("the seeded skip label did not load: %+v", f.Limits.Skip)
	}
	if err := capabilities.Open(r.home).Record("work", "claude", "2.1.300 (fake)", capabilities.AutopilotGuard, capabilities.Result{Passed: true, At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	r.store.Events = func(fleet, source string) ([]routines.EventKind, bool) {
		return []routines.EventKind{{Kind: "review_requested", Title: "Review requested"}}, fleet == "work" && source == "gh"
	}
	in := launchRoutine()
	in.When, in.Before, in.On = "", routines.AskFirst, &routines.On{Source: "gh", Kind: "review_requested"}
	got, err := r.store.Create("work", in)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return r, got
}

func (r *soRig) mute(fleet, routine, body string) (int, string) {
	r.t.Helper()
	res, err := http.Post(r.srv.URL+"/api/fleets/"+fleet+"/routines/"+routine+"/mute", "application/json", bytes.NewReader([]byte(body)))
	if err != nil {
		r.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

func (r *soRig) skip() standing.Skip {
	r.t.Helper()
	f, ok := r.cfg.Current().Fleet("work")
	if !ok {
		r.t.Fatal("fleet work is gone")
	}
	return f.Limits.Skip
}

func TestAMuteRuleBySenderAddsOneEntryScopedToTheRoutineIDAndKeepsTheOtherSkipEntries(t *testing.T) {
	r, rt := muteRig(t)
	body, _ := json.Marshal(map[string]string{"by": "sender", "value": "mallory-the-stranger"})
	if code, msg := r.mute("work", rt.ID, string(body)); code != http.StatusNoContent && code != http.StatusOK {
		t.Fatalf("mute = %d %s, want 200 or 204", code, msg)
	}
	got := r.skip()
	if len(got.Senders) != 1 || got.Senders[0] != (standing.Scoped{Value: "mallory-the-stranger", Routine: rt.ID}) {
		t.Errorf("skip senders = %+v, want the sender scoped to routine id %s", got.Senders, rt.ID)
	}
	if len(got.Labels) != 1 || got.Labels[0].Value != "wip" || len(got.DedupeKeys) != 0 {
		t.Errorf("skip = %+v, want the existing wip label kept and no dedupe key", got)
	}
}

func TestAMuteRuleByDedupeKeyAddsOneScopedEntryAndMuteIsIdempotent(t *testing.T) {
	r, rt := muteRig(t)
	body, _ := json.Marshal(map[string]string{"by": "dedupe_key", "value": "gh:acme/api#7"})
	for range 2 {
		if code, msg := r.mute("work", rt.ID, string(body)); code != http.StatusNoContent && code != http.StatusOK {
			t.Fatalf("mute = %d %s, want 200 or 204", code, msg)
		}
	}
	got := r.skip()
	if len(got.DedupeKeys) != 1 || got.DedupeKeys[0] != (standing.Scoped{Value: "gh:acme/api#7", Routine: rt.ID}) {
		t.Errorf("skip dedupe keys = %+v, want one entry scoped to routine id %s", got.DedupeKeys, rt.ID)
	}
	if len(got.Labels) != 1 || len(got.Senders) != 0 {
		t.Errorf("skip = %+v, want the wip label kept and no sender", got)
	}
}

func TestAMuteRuleIsRefusedForAnUnattributedSenderAnEmptyKeyAnUnknownRoutineAndAnUnknownKind(t *testing.T) {
	r, rt := muteRig(t)
	for _, c := range []struct {
		name, routine, body string
		want                int
	}{
		{"an (unknown) sender", rt.ID, `{"by":"sender","value":"(unknown)"}`, http.StatusBadRequest},
		{"an empty sender", rt.ID, `{"by":"sender","value":""}`, http.StatusBadRequest},
		{"an empty dedupe key", rt.ID, `{"by":"dedupe_key","value":""}`, http.StatusBadRequest},
		{"a sender with a bidi rune", rt.ID, `{"by":"sender","value":"ali\u202ece"}`, http.StatusBadRequest},
		{"a sender over 100 bytes", rt.ID, `{"by":"sender","value":"` + strings.Repeat("a", 101) + `"}`, http.StatusBadRequest},
		{"a dedupe key with prose", rt.ID, `{"by":"dedupe_key","value":"note to AdmiralBot: launch"}`, http.StatusBadRequest},
		{"a dedupe key over 128 bytes", rt.ID, `{"by":"dedupe_key","value":"` + strings.Repeat("k", 129) + `"}`, http.StatusBadRequest},
		{"an unknown kind", rt.ID, `{"by":"label","value":"x"}`, http.StatusBadRequest},
		{"an unknown routine", "no-such-routine", `{"by":"sender","value":"alice"}`, http.StatusNotFound},
	} {
		if code, msg := r.mute("work", c.routine, c.body); code != c.want {
			t.Errorf("%s: mute = %d %s, want %d", c.name, code, msg, c.want)
		}
	}
	if got := r.skip(); len(got.Senders) != 0 || len(got.DedupeKeys) != 0 || len(got.Labels) != 1 {
		t.Errorf("skip = %+v, want nothing added by a refused mute", got)
	}
}

func TestAMutedSenderIsSkippedByThatRoutineOnly(t *testing.T) {
	r, rt := muteRig(t)
	if code, msg := r.mute("work", rt.ID, `{"by":"sender","value":"mallory-the-stranger"}`); code != http.StatusNoContent && code != http.StatusOK {
		t.Fatalf("mute = %d %s", code, msg)
	}
	f, _ := r.cfg.Current().Fleet("work")
	if !f.Limits.Skipped(standing.SkipInput{Routine: rt.ID, Sender: "mallory-the-stranger"}) {
		t.Error("the muted sender is not skipped for the routine")
	}
	if f.Limits.Skipped(standing.SkipInput{Routine: "another-routine", Sender: "mallory-the-stranger"}) {
		t.Error("the muted sender is skipped for another routine")
	}
}
