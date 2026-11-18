package routines_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/routines"
)

// A digest routine's save rules and its JSON shape; the run is in digest_sched_test.go.

func digestRoutine() routines.Routine {
	return routines.Routine{
		Name: "Morning digest", Enabled: true, When: "0 9 * * *", Before: routines.Act,
		Do: routines.Do{Digest: &routines.Digest{Extra: "lead with PRs"}},
	}
}

func TestADigestRoutineSavesAndKeepsItsExtraWordsAcrossAReopen(t *testing.T) {
	r := newRig(t)
	saved := r.create("work", digestRoutine())
	reopened := &routines.Store{Home: r.home, Fleets: r.store.Fleets, Now: r.store.Now}
	list, err := reopened.List("work")
	if err != nil || len(list) != 1 {
		t.Fatalf("List after reopen = %v, %v", list, err)
	}
	got := list[0]
	if got.ID != saved.ID || got.Do.Digest == nil || got.Do.Digest.Extra != "lead with PRs" || got.Do.Launch != nil || got.Do.Prompt != "" {
		t.Fatalf("reopened = %+v, want a digest routine with its extra words and nothing else in do", got.Do)
	}
	raw, err := os.ReadFile(filepath.Join(r.home, "routines", "work", "routines.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"digest"`) || !strings.Contains(string(raw), `"extra"`) {
		t.Errorf("routines.json = %s, want do.digest.extra", raw)
	}
}

func TestADigestRoutineWithNoExtraWordsSaves(t *testing.T) {
	r := newRig(t)
	in := digestRoutine()
	in.Do.Digest = &routines.Digest{}
	saved := r.create("work", in)
	if saved.Do.Digest == nil || saved.Do.Digest.Extra != "" {
		t.Fatalf("saved do = %+v, want an empty digest action kept", saved.Do)
	}
	list, err := r.store.List("work")
	if err != nil || len(list) != 1 || list[0].Do.Digest == nil {
		t.Fatalf("List = %+v, %v, want the digest action to survive a reload", list, err)
	}
}

func TestADigestRoutineIsRefusedWithAnOnBecauseItRunsOnASchedule(t *testing.T) {
	r := newRig(t)
	r.catalog()
	in := digestRoutine()
	in.When, in.On = "", &routines.On{Source: "gh", Kind: "review_requested"}
	if _, err := r.store.Create("work", in); !errors.Is(err, routines.ErrInvalid) || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("Create(digest with on) = %v, want ErrInvalid saying a digest runs on a schedule", err)
	}
	if n := r.count("work"); n != 0 {
		t.Errorf("%d routines saved, want none", n)
	}
}

func TestDoTakesExactlyOneOfLaunchPromptOrDigest(t *testing.T) {
	r := newRig(t)
	launch := launchRoutine().Do.Launch
	cases := map[string]routines.Do{
		"digest and prompt":         {Digest: &routines.Digest{}, Prompt: "hello"},
		"digest and launch":         {Digest: &routines.Digest{}, Launch: launch},
		"digest, launch and prompt": {Digest: &routines.Digest{}, Launch: launch, Prompt: "hello"},
	}
	for name, do := range cases {
		in := digestRoutine()
		in.Do = do
		if _, err := r.store.Create("work", in); !errors.Is(err, routines.ErrInvalid) {
			t.Errorf("%s: Create = %v, want ErrInvalid", name, err)
		}
	}
	if n := r.count("work"); n != 0 {
		t.Errorf("%d routines saved, want none", n)
	}
}

func TestADigestsExtraWordsAreCappedAtAThousandCharactersSoTheTurnFitsThePaste(t *testing.T) {
	r := newRig(t)
	in := digestRoutine()
	in.Do.Digest.Extra = strings.Repeat("a", 1000)
	r.create("work", in)
	in.Name = "Too long"
	in.Do.Digest.Extra = strings.Repeat("a", 1001)
	if _, err := r.store.Create("work", in); !errors.Is(err, routines.ErrInvalid) {
		t.Fatalf("Create(1001 characters of extra words) = %v, want ErrInvalid", err)
	}
}

func TestTheRoutinesAPIAcceptsAndReturnsADigestRoutineAndRefusesItWithAnOn(t *testing.T) {
	a := newAPI(t)
	a.rig.catalog()
	const body = `{"name":"Morning digest","enabled":true,"when":"0 9 * * *","before_acting":"act","notify":false,
 "do":{"digest":{"extra":"lead with PRs"}}}`
	code, got := a.do("POST", "/api/fleets/work/routines", body)
	if code != http.StatusCreated {
		t.Fatalf("POST = %d %v, want 201", code, got)
	}
	do, _ := got["do"].(map[string]any)
	digest, _ := do["digest"].(map[string]any)
	if digest["extra"] != "lead with PRs" || do["launch"] != nil || do["prompt"] != nil {
		t.Errorf("do = %v, want {digest:{extra:\"lead with PRs\"}}", do)
	}
	_, list := a.do("GET", "/api/fleets/work/routines", "")
	raw, _ := json.Marshal(list)
	if !strings.Contains(string(raw), `"digest":{"extra":"lead with PRs"}`) {
		t.Errorf("list = %s, want the digest action with its extra words", raw)
	}
	withOn := `{"name":"Event digest","enabled":true,"on":{"source":"gh","kind":"review_requested"},"before_acting":"act","do":{"digest":{}}}`
	code, bad := a.do("POST", "/api/fleets/work/routines", withOn)
	wantError(t, code, bad, http.StatusBadRequest)
	if msg, _ := bad["error"].(string); !strings.Contains(msg, "digest") {
		t.Errorf("error = %q, want it to say a digest runs on a schedule", msg)
	}
}
