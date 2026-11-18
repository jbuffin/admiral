package ships

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/jbuffin/admiral/internal/launcher"
)

const (
	campaignIssue  = "https://github.com/acme/widgets/issues/20"
	otherCampaign  = "https://github.com/acme/widgets/issues/21"
	endedWithoutIt = "ended without filing a campaign"
)

// draftRig is a hand rig whose ship saltpaw is a drafting ship, with the follow func replaced by a recorder.
type draftRig struct {
	*handRig
	mu    sync.Mutex
	calls [][2]string // fleet, ref
	err   error       // what the follow func answers
}

func newDraftRig(t *testing.T) *draftRig {
	t.Helper()
	r := &draftRig{handRig: newHandRig(t)}
	r.s.FollowDrafted = func(fleet, ref string) error {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.calls = append(r.calls, [2]string{fleet, ref})
		return r.err
	}
	r.draft(t, "saltpaw")
	return r
}

func (r *draftRig) draft(t *testing.T, session string) {
	t.Helper()
	r.s.markDrafting(session, firstLaunched(r.options(t, session)))
}

func (r *draftRig) followed() [][2]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([][2]string(nil), r.calls...)
}

func (r *draftRig) refOf(session string) string {
	return filepath.Join(r.home, "ships", session, launcher.CampaignRefFile)
}

// turnOf sets what a ship's hooks would, for any session, then runs the poller's passes that read it.
func (r *draftRig) turnOf(t *testing.T, session, state, reason string) {
	t.Helper()
	set := func(args ...string) {
		if out, err := exec.Command(r.s.Launcher.Tmux, append([]string{"-S", r.s.Launcher.Socket, "set-option", "-p", "-t", session}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("set-option %v: %v: %s", args, err, out)
		}
	}
	set("@admiral-state", state)
	if reason == "" {
		set("-u", "@admiral-reason")
	} else {
		set("@admiral-reason", reason)
	}
	_ = r.s.Host.Sync(context.Background())
	r.s.observe(context.Background())
	r.s.advanceDrafting()
	r.s.settleWG.Wait()
}

// endTurn is a turn that works, then stops: one turn end for the dedupe, whatever the poller does in between.
func (r *draftRig) endTurn(t *testing.T, session string) {
	t.Helper()
	r.turnOf(t, session, "working", "")
	r.turnOf(t, session, "needs_you", "stop")
	r.s.advanceDrafting()
	r.s.advanceDrafting()
	r.s.settleWG.Wait()
}

func (r *draftRig) draftingJSON(t *testing.T, session string) map[string]any {
	t.Helper()
	for _, sh := range r.s.List() {
		if sh.Session == session {
			return shipJSON(t, sh)
		}
	}
	t.Fatalf("%s is not listed", session)
	return nil
}

// endedItems lists the drafting-ended items in the Admiral inbox, read from disk so a missing writer shows as none.
func (r *draftRig) endedItems(t *testing.T) []string {
	t.Helper()
	dir := filepath.Join(r.home, "inbox", "_admiral")
	entries, _ := os.ReadDir(dir)
	var out []string
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err == nil && strings.Contains(string(b), endedWithoutIt) {
			out = append(out, string(b))
		}
	}
	return out
}

func (r *draftRig) waitEndedItem(t *testing.T) string {
	t.Helper()
	var items []string
	waitUntil(t, "the ended-without-filing item", func() bool { items = r.endedItems(t); return len(items) > 0 })
	r.s.settleWG.Wait()
	if items = r.endedItems(t); len(items) != 1 {
		t.Fatalf("%d ended-without-filing items, want 1: %v", len(items), items)
	}
	return items[0]
}

func TestADraftingShipsRefIsFollowedOncePerTurnEndAndTheFileIsDeleted(t *testing.T) {
	r := newDraftRig(t)
	writeRef(t, r.refOf("saltpaw"), campaignIssue+"\n")
	r.endTurn(t, "saltpaw")

	if got := r.followed(); len(got) != 1 || got[0] != [2]string{"work", campaignIssue} {
		t.Fatalf("follow calls = %v, want one for work and %s", got, campaignIssue)
	}
	if _, err := os.Stat(r.refOf("saltpaw")); err == nil {
		t.Error("the ref file was left after it was read")
	}

	// A turn that ends with no new ref asks nothing more.
	r.endTurn(t, "saltpaw")
	if got := r.followed(); len(got) != 1 {
		t.Errorf("follow calls after a turn with no ref = %v, want still one", got)
	}
}

func TestARefWrittenBeforeATurnEndsIsNotReadUntilItDoes(t *testing.T) {
	r := newDraftRig(t)
	r.turnOf(t, "saltpaw", "working", "")
	writeRef(t, r.refOf("saltpaw"), campaignIssue+"\n")
	r.s.advanceDrafting()
	r.s.settleWG.Wait()
	if got := r.followed(); len(got) != 0 {
		t.Fatalf("follow calls mid-turn = %v, want none", got)
	}
	r.turnOf(t, "saltpaw", "needs_you", "stop")
	if got := r.followed(); len(got) != 1 {
		t.Errorf("follow calls at the turn end = %v, want one", got)
	}
}

func TestARefTheFollowRefusesShowsItsReasonOnTheShip(t *testing.T) {
	r := newDraftRig(t)
	refusal := "That campaign belongs to fleet home, not work"
	r.err = errString(refusal)
	writeRef(t, r.refOf("saltpaw"), campaignIssue+"\n")
	r.endTurn(t, "saltpaw")

	sh := r.draftingJSON(t, "saltpaw")
	if sh["drafting"] != true || sh["drafting_notice"] != refusal {
		t.Errorf("ship drafting=%v drafting_notice=%q, want drafting with the refusal %q", sh["drafting"], sh["drafting_notice"], refusal)
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func TestARefToTheAlreadyFollowedIssueLeavesNoNoticeAndALaterOtherRefIsIgnoredWithOne(t *testing.T) {
	r := newDraftRig(t)
	writeRef(t, r.refOf("saltpaw"), campaignIssue+"\n")
	r.endTurn(t, "saltpaw")
	if sh := r.draftingJSON(t, "saltpaw"); sh["drafting"] != false && sh["drafting"] != nil {
		t.Errorf("drafting = %v once its campaign is followed, want it no longer shown as waiting", sh["drafting"])
	}

	writeRef(t, r.refOf("saltpaw"), campaignIssue+"\n")
	r.endTurn(t, "saltpaw")
	if n := r.draftingJSON(t, "saltpaw")["drafting_notice"]; n != nil && n != "" {
		t.Errorf("notice after the same issue again = %q, want none", n)
	}

	writeRef(t, r.refOf("saltpaw"), otherCampaign+"\n")
	r.endTurn(t, "saltpaw")
	if n, _ := r.draftingJSON(t, "saltpaw")["drafting_notice"].(string); !strings.Contains(n, "21") {
		t.Errorf("notice after a different issue = %q, want a line naming it", n)
	}
	for _, c := range r.followed() {
		if c[1] == otherCampaign {
			t.Errorf("follow was asked for the later issue: %v", r.followed())
		}
	}
}

func TestAnUnsafeRefIsNotReadAndTheNextValidOneIs(t *testing.T) {
	for _, c := range []struct {
		name  string
		write func(t *testing.T, path string)
	}{
		{"a symlink", func(t *testing.T, path string) {
			target := filepath.Join(t.TempDir(), "elsewhere")
			writeRef(t, target, campaignIssue+"\n")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
		}},
		{"a FIFO", func(t *testing.T, path string) {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := syscall.Mkfifo(path, 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"an oversized file", func(t *testing.T, path string) {
			writeRef(t, path, campaignIssue+strings.Repeat(" ", 1<<20)+"\n")
		}},
		{"a line that is no issue URL", func(t *testing.T, path string) {
			writeRef(t, path, "https://example.com/acme/widgets/issues/20\n")
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newDraftRig(t)
			c.write(t, r.refOf("saltpaw"))
			r.endTurn(t, "saltpaw")
			if got := r.followed(); len(got) != 0 {
				t.Fatalf("follow calls for %s = %v, want none", c.name, got)
			}
			_ = os.Remove(r.refOf("saltpaw"))
			writeRef(t, r.refOf("saltpaw"), campaignIssue+"\n")
			r.endTurn(t, "saltpaw")
			if got := r.followed(); len(got) != 1 || got[0][1] != campaignIssue {
				t.Errorf("follow calls after a valid ref = %v, want one for %s", got, campaignIssue)
			}
		})
	}
}

func TestAWokenDraftingShipStaysDraftingAndItsRefIsFollowed(t *testing.T) {
	r := newWakeRig(t, 0)
	first, _ := shipJSON(t, r.moorKeyed(t))["first_launched"].(string)
	r.s.markDrafting("saltpaw", first)
	var mu sync.Mutex
	var calls []string
	r.s.FollowDrafted = func(fleet, ref string) error {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, ref)
		return nil
	}
	if err := os.Remove(filepath.Join(r.rec, "exit")); err != nil {
		t.Fatal(err)
	}
	if _, lerr := r.s.Wake(context.Background(), "saltpaw", ""); lerr != nil {
		t.Fatalf("Wake = %v", lerr)
	}
	r.started(t, 2)

	// The wake gives the harness a new launch stamp; the ship is still the drafting ship it was.
	var sh map[string]any
	for _, s := range r.s.List() {
		if s.Session == "saltpaw" {
			sh = shipJSON(t, s)
		}
	}
	if sh["drafting"] != true {
		t.Fatalf("a woken drafting ship shows drafting = %v, want true", sh["drafting"])
	}
	n := &nextRig{wakeRig: r}
	writeRef(t, filepath.Join(r.home, "ships", "saltpaw", launcher.CampaignRefFile), campaignIssue+"\n")
	n.turn(t, "working", "")
	n.turn(t, "needs_you", "stop")
	r.s.observe(context.Background())
	r.s.advanceDrafting()
	r.s.settleWG.Wait()
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 1 || calls[0] != campaignIssue {
		t.Errorf("follow calls for the woken ship = %v, want one for %s", calls, campaignIssue)
	}
}

func TestStoppingADraftingShipThatFiledNothingPostsOneItemNamingIt(t *testing.T) {
	r := newDraftRig(t)
	r.stopShip(t, "saltpaw")
	item := r.waitEndedItem(t)
	if !strings.Contains(item, "Saltpaw") {
		t.Errorf("item %s does not name the ship", item)
	}
}

func TestADraftingShipWithALateRefIsFollowedAtTheStopNotReportedEnded(t *testing.T) {
	r := newDraftRig(t)
	writeRef(t, r.refOf("saltpaw"), campaignIssue+"\n")
	r.stopShip(t, "saltpaw")
	r.s.settleWG.Wait()
	if got := r.followed(); len(got) != 1 || got[0][1] != campaignIssue {
		t.Fatalf("follow calls at the stop = %v, want one for %s", got, campaignIssue)
	}
	if items := r.endedItems(t); len(items) != 0 {
		t.Errorf("items = %v, want none for a ship that filed", items)
	}
}

func TestAFollowedDraftingShipThatStopsPostsNoItem(t *testing.T) {
	r := newDraftRig(t)
	writeRef(t, r.refOf("saltpaw"), campaignIssue+"\n")
	r.endTurn(t, "saltpaw")
	r.stopShip(t, "saltpaw")
	r.s.settleWG.Wait()
	if got := r.followed(); len(got) != 1 {
		t.Fatalf("follow calls = %v, want the one at its turn end", got)
	}
	if items := r.endedItems(t); len(items) != 0 {
		t.Errorf("items = %v, want none", items)
	}
}

// bramble is a second drafting ship, the one a test stops through the stop route to prove an item posts at all.
func (r *draftRig) bramble(t *testing.T) {
	t.Helper()
	_, lerr := r.s.LaunchOrder(launcher.Request{Fleet: "work", Harness: "claude", Dir: r.checkout, Model: "opus", Name: "Bramble",
		Session: "bramble", Prompt: "go", Skill: launcher.Skill{Kind: "none"}, Resources: []string{}})
	if lerr != nil {
		t.Fatalf("LaunchOrder bramble = %v", lerr)
	}
	r.started(t, 2)
	r.draft(t, "bramble")
}

func TestHandingAndReturningADraftingShipPostNoItemOnlyAStoppedOneDoes(t *testing.T) {
	r := newDraftRig(t)
	r.bramble(t)

	ref := r.queue(t, `{"kind":"issue"}`)
	writeRef(t, ref, handIssue+"\n")
	r.handRig.endTurn(t)
	r.started(t, 3)
	if sh := r.draftingJSON(t, "saltpaw"); sh["drafting"] == true {
		t.Error("a ship handed to Commodore still shows as a drafting ship")
	}

	r.stopShip(t, "bramble")
	item := r.waitEndedItem(t)
	if !strings.Contains(item, "Bramble") || strings.Contains(item, "Saltpaw") {
		t.Errorf("the one item must name only the stopped ship Bramble: %s", item)
	}
}

func TestReturningADraftingShipToPortPostsNoItem(t *testing.T) {
	r := newDraftRig(t)
	r.bramble(t)
	r.turnOf(t, "saltpaw", "needs_you", "stop")
	if rec := r.returnToPort("saltpaw"); rec.Code != 200 {
		t.Fatalf("return = %d %s", rec.Code, rec.Body)
	}
	r.s.advanceReturns()
	waitUntil(t, "the clean-up prompt at the ship's prompt", func() bool { return strings.Contains(r.pane(t), "Return to port") })
	r.s.settleWG.Wait()
	r.turnOf(t, "saltpaw", "needs_you", "stop")
	r.s.advanceReturns()
	waitUntil(t, "saltpaw to be stopped", func() bool { return !r.afloat() })

	r.stopShip(t, "bramble")
	item := r.waitEndedItem(t)
	if !strings.Contains(item, "Bramble") || strings.Contains(item, "Saltpaw") {
		t.Errorf("the one item must name only the stopped ship Bramble: %s", item)
	}
}

func TestAMooredDraftingShipKeepsDraftingAndPostsNoItem(t *testing.T) {
	r := newWakeRig(t, 0)
	first, _ := shipJSON(t, r.moorKeyed(t))["first_launched"].(string)
	r.s.markDrafting("saltpaw", first)
	r.s.settleWG.Wait()
	if items := (&draftRig{handRig: &handRig{&nextRig{wakeRig: r}}}).endedItems(t); len(items) != 0 {
		t.Errorf("a ship that moored posted %v, want no item", items)
	}
}

func TestTheShipsListCarriesTheDraftingFieldsOverHTTP(t *testing.T) {
	r := newDraftRig(t)
	rec := httptest.NewRecorder()
	r.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/ships", nil))
	var list []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list) == 0 {
		t.Fatalf("GET /api/ships = %d %s (%v)", rec.Code, rec.Body, err)
	}
	for _, sh := range list {
		if sh["session"] == "saltpaw" && sh["drafting"] != true {
			t.Errorf("saltpaw over HTTP: drafting = %v, want true", sh["drafting"])
		}
	}
}
