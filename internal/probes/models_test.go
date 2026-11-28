package probes_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/probes"
)

// The runner lists each dock's models whenever it looks at the dock, whatever its version check says.

type listed struct {
	mu    sync.Mutex
	calls []string
	enter chan struct{}
	hold  chan struct{}
}

func (l *listed) List(ctx context.Context, fleet config.Fleet, harness string) error {
	l.mu.Lock()
	l.calls = append(l.calls, fleet.Key+"/"+harness)
	l.mu.Unlock()
	if l.enter != nil {
		l.enter <- struct{}{}
		select {
		case <-l.hold:
		case <-ctx.Done():
		}
	}
	return nil
}

func (l *listed) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.calls)
}

func TestRunListsTheDocksModelsEvenWhenItsVersionCannotBeDetected(t *testing.T) {
	t.Parallel()
	l := &listed{}
	r := probes.New(home(t), pass("x"))
	r.SetModelLister(l)
	_, _ = r.Run(context.Background(), fleetOf(versionScript(t), "FAIL"), "claude")
	r.Wait()
	if l.count() != 1 {
		t.Errorf("the lister ran %d times, want once: a broken --version must not hide the models", l.count())
	}
}

func TestRefreshListsTheDocksModelsEvenWhenItsVersionIsUnchangedOrCannotBeDetected(t *testing.T) {
	t.Parallel()
	l := &listed{}
	r := probes.New(home(t), pass("x"))
	r.SetModelLister(l)
	fleet := fleetOf(versionScript(t), "9.1.0")
	r.Refresh(context.Background(), []config.Fleet{fleet})
	r.Wait()
	r.Refresh(context.Background(), []config.Fleet{fleet})
	r.Wait()
	r.Refresh(context.Background(), []config.Fleet{fleetOf(versionScript(t), "FAIL")})
	r.Wait()
	if l.count() != 3 {
		t.Errorf("the lister ran %d times over three refreshes, want 3: new models can appear without a version change", l.count())
	}
}

func TestRunReturnsWhileAModelListingIsStillRunning(t *testing.T) {
	t.Parallel()
	l := &listed{enter: make(chan struct{}, 1), hold: make(chan struct{})}
	r := probes.New(home(t), pass("x"))
	r.SetModelLister(l)
	returned := make(chan struct{})
	go func() {
		_, _ = r.Run(context.Background(), fleetOf(versionScript(t), "9.1.0"), "claude")
		close(returned)
	}()
	select {
	case <-l.enter:
	case <-time.After(10 * time.Second):
		t.Fatal("the lister never started")
	}
	select {
	case <-returned:
	case <-time.After(10 * time.Second):
		t.Fatal("Check again waited for a harness listing")
	}
	close(l.hold)
	r.Wait()
}
