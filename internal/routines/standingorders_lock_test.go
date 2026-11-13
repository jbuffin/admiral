package routines_test

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/standing"
)

func scopedSkip(t *testing.T, r *soRig, routine string) {
	t.Helper()
	skip := `{"senders":[{"value":"mallory","routine":"` + routine + `"}]}`
	r.setLimits("work", config.FleetFields{Limits: map[string]json.RawMessage{"skip": json.RawMessage(skip)}})
}

func TestAConfigViewAndARoutineSaveThatChecksResourcesDoNotWaitOnEachOther(t *testing.T) {
	r, rt := muteRig(t)
	scopedSkip(t, r, rt.ID)
	r.cfg.RoutineNamer = func(fleet string) standing.RoutineNamer { return r.store.Namer(fleet) }
	r.store.ResourceKnown = func(string) bool { r.cfg.Current(); return true }
	up := rt
	up.Do.Launch.Resources = []string{"phone"}

	done := make(chan struct{})
	go func() {
		defer close(done)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			for i := 0; i < 300; i++ {
				r.cfg.View()
			}
		}()
		go func() {
			defer wg.Done()
			for i := 0; i < 300; i++ {
				if _, err := r.store.Update("work", rt.ID, up); err != nil {
					t.Errorf("Update: %v", err)
					return
				}
			}
		}()
		wg.Wait()
	}()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("View and Update deadlocked")
	}
}

func skipSentence(t *testing.T, r *soRig) string {
	t.Helper()
	_, body := r.do("GET", "/api/fleets/work/standing-orders", nil)
	for _, s := range body["sentences"].([]any) {
		if m := s.(map[string]any); m["field"] == "skip.senders" {
			return m["sentence"].(string)
		}
	}
	t.Fatal("no skip.senders sentence")
	return ""
}

func TestAScopedSkipKeepsItsRoutineIDWhenTheRoutineListCannotBeRead(t *testing.T) {
	r, rt := muteRig(t)
	scopedSkip(t, r, rt.ID)
	_ = filepath.WalkDir(r.home, func(p string, d fs.DirEntry, _ error) error {
		if d != nil && d.Name() == "routines.json" {
			if err := os.Chmod(p, 0); err != nil {
				t.Fatal(err)
			}
		}
		return nil
	})
	got := skipSentence(t, r)
	if !strings.Contains(got, "mallory ("+rt.ID+" only)") || strings.Contains(got, "deleted") {
		t.Errorf("sentence = %q, want it to keep the routine ID and not call it deleted", got)
	}
}

func TestARoutineNameInALimitSentenceHasHiddenRunesStrippedAndIsBounded(t *testing.T) {
	r, rt := muteRig(t)
	scopedSkip(t, r, rt.ID)
	// the name is edited into the file because a save refuses such a name
	_ = filepath.WalkDir(r.home, func(p string, d fs.DirEntry, _ error) error {
		if d != nil && d.Name() == "routines.json" {
			b, _ := os.ReadFile(p)
			var f struct {
				Routines []map[string]any `json:"routines"`
			}
			if err := json.Unmarshal(b, &f); err != nil {
				t.Fatal(err)
			}
			f.Routines[0]["name"] = "a\u202eb" + strings.Repeat("é", 100)
			b, _ = json.Marshal(f)
			if err := os.WriteFile(p, b, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return nil
	})
	got := skipSentence(t, r)
	if strings.Contains(got, "\u202e") || !strings.Contains(got, "…") {
		t.Errorf("sentence = %q, want no bidi rune and a name bounded at 80 runes", got)
	}
}

