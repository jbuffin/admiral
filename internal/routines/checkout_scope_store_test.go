package routines_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/routines"
)

func (r *rig) sourcePlugin(plugin string) {
	r.store.SourcePlugin = func(fleet, source string) (string, bool) { return plugin, fleet == "work" && source == "gh" }
}

func (r *rig) saveOn(dir string, mod func(*routines.Routine)) error {
	in := validOn()
	in.Do.Launch.Dir = dir
	if mod != nil {
		mod(&in)
	}
	_, err := r.store.Create("work", in)
	return err
}

func TestAGitHubEventRoutineOnACheckoutWithNoRemoteIsRefusedNamingCheckout(t *testing.T) {
	r := newRig(t)
	r.catalog()
	r.sourcePlugin("github")
	err := r.saveOn(checkoutWith(t), nil)
	if !errors.Is(err, routines.ErrInvalid) || err == nil || !strings.Contains(err.Error(), "Checkout has no remote, so a GitHub event routine on it never fires") {
		t.Fatalf("Create = %v, want an invalid error: Checkout has no remote, so a GitHub event routine on it never fires", err)
	}
	if r.count("work") != 0 {
		t.Error("the refused routine was saved")
	}
}

func TestAnUnscopedRoutineSavesOnACheckoutWithNoRemote(t *testing.T) {
	bare := checkoutWith(t)
	t.Run("a schedule routine", func(t *testing.T) {
		r := newRig(t)
		r.catalog()
		r.sourcePlugin("github")
		if err := r.saveOn(bare, func(in *routines.Routine) { in.On, in.When = nil, "0 9 * * *" }); err != nil {
			t.Fatalf("Create = %v", err)
		}
	})
	t.Run("a plugin-source event routine", func(t *testing.T) {
		r := newRig(t)
		r.catalog()
		r.sourcePlugin("tracker")
		if err := r.saveOn(bare, nil); err != nil {
			t.Fatalf("Create = %v", err)
		}
	})
	t.Run("no source hook", func(t *testing.T) {
		r := newRig(t)
		r.catalog()
		if err := r.saveOn(bare, nil); err != nil {
			t.Fatalf("Create = %v", err)
		}
	})
	t.Run("a GitHub routine on a checkout with a remote", func(t *testing.T) {
		r := newRig(t)
		r.catalog()
		r.sourcePlugin("github")
		if err := r.saveOn(sharedCheckout, nil); err != nil {
			t.Fatalf("Create = %v", err)
		}
	})
}

func TestEditingASavedGitHubRoutineOnARemotelessCheckoutIsRefusedUntilTheCheckoutChanges(t *testing.T) {
	r := newRig(t)
	r.catalog()
	saved := r.create("work", func() routines.Routine { in := validOn(); in.Do.Launch.Dir = checkoutWith(t); return in }())
	r.sourcePlugin("github")
	edit := saved
	edit.Name = "Renamed"
	if _, err := r.store.Update("work", saved.ID, edit); !errors.Is(err, routines.ErrInvalid) {
		t.Fatalf("Update = %v, want ErrInvalid", err)
	}
	if _, err := r.store.SetEnabled("work", saved.ID, false); err != nil {
		t.Errorf("SetEnabled = %v, want it unvalidated", err)
	}
	edit.Do.Launch.Dir = sharedCheckout
	if _, err := r.store.Update("work", saved.ID, edit); err != nil {
		t.Fatalf("Update with a checkout that has a remote = %v", err)
	}
}
