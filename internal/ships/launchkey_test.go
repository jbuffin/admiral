package ships

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/actor"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/moor"
	"github.com/jbuffin/admiral/internal/server"
)

const (
	keyIssueA = "https://github.com/acme/widgets/issues/41"
	keyIssueB = "https://github.com/acme/widgets/issues/42"
)

func (r *wakeRig) keyReq(mod func(*launcher.Request)) launcher.Request {
	req := launcher.Request{Fleet: "work", Harness: "claude", Dir: r.checkout, Model: "opus", Prompt: "go", Skill: launcher.Skill{Kind: "none"}}
	if mod != nil {
		mod(&req)
	}
	return req
}

func (r *wakeRig) mustLaunch(t *testing.T, mod func(*launcher.Request)) Ship {
	t.Helper()
	got, lerr := r.s.Launch(context.Background(), r.keyReq(mod))
	if lerr != nil {
		t.Fatalf("Launch = %v, want a ship", lerr)
	}
	return got
}

// wantHeld asserts a 409 that names the holding ship, and that no second ship was started.
func (r *wakeRig) wantHeld(t *testing.T, mod func(*launcher.Request), holder Ship) {
	t.Helper()
	before := len(r.s.List())
	_, lerr := r.s.Launch(context.Background(), r.keyReq(mod))
	if lerr == nil || lerr.Status != http.StatusConflict || !strings.Contains(lerr.Msg, holder.Name) {
		t.Fatalf("second Launch = %v, want a 409 naming %q", lerr, holder.Name)
	}
	if got := len(r.s.List()); got != before {
		t.Errorf("%d ships after the refused launch, want %d: the first ship is untouched", got, before)
	}
}

func chatKey(id string) func(*launcher.Request) {
	return func(q *launcher.Request) { q.ChatMessage = id }
}

func TestASecondLaunchOfALiveOrderIsRefusedNamingItsShip(t *testing.T) {
	r := newWakeRig(t, 0)
	first := r.mustLaunch(t, func(q *launcher.Request) { q.Source, q.OrderID = "s", "o1" })
	r.wantHeld(t, func(q *launcher.Request) { q.Source, q.OrderID = "s", "o1" }, first)
}

func TestASecondLaunchOfALiveIssueIsRefusedWhateverItsSpelling(t *testing.T) {
	r := newWakeRig(t, 0)
	first := r.mustLaunch(t, func(q *launcher.Request) { q.Issues = []string{keyIssueA} })
	r.wantHeld(t, func(q *launcher.Request) { q.Issues = []string{"https://GitHub.com/Acme/Widgets/issues/41/"} }, first)
}

func TestASecondLaunchOfALiveSliceIsRefusedInAnyOrderOrSpelling(t *testing.T) {
	r := newWakeRig(t, 0)
	first := r.mustLaunch(t, func(q *launcher.Request) { q.Issues = []string{keyIssueA, keyIssueB} })
	r.wantHeld(t, func(q *launcher.Request) {
		q.Issues = []string{"https://GitHub.com/acme/widgets/issues/42/", keyIssueA}
	}, first)
}

func TestASecondLaunchOfALiveChatMessageIsRefusedNamingItsShip(t *testing.T) {
	r := newWakeRig(t, 0)
	first := r.mustLaunch(t, chatKey("m1"))
	r.wantHeld(t, chatKey("m1"), first)
	r.mustLaunch(t, chatKey("m2")) // another message is another key
}

func TestAnywayLaunchesAgainFromTheSameChatMessage(t *testing.T) {
	r := newWakeRig(t, 0)
	first := r.mustLaunch(t, chatKey("m1"))
	r.mustLaunch(t, func(q *launcher.Request) { q.ChatMessage, q.Anyway = "m1", true })
	r.mustLaunch(t, func(q *launcher.Request) { q.ChatMessage, q.Anyway = "m1", true }) // a fresh key each time
	r.wantHeld(t, chatKey("m1"), first)
}

// The anyway ship's own key must not be the chat key, or it would hold the message against a plain launch.
func TestAnAnywayShipDoesNotHoldItsChatMessage(t *testing.T) {
	r := newWakeRig(t, 0)
	r.mustLaunch(t, func(q *launcher.Request) { q.ChatMessage, q.Anyway = "m7", true })
	plain := r.mustLaunch(t, chatKey("m7"))
	r.wantHeld(t, chatKey("m7"), plain)
}

func TestAnywayNeverGetsPastAnOrderOrAnIssueAnotherShipHolds(t *testing.T) {
	r := newWakeRig(t, 0)
	order := r.mustLaunch(t, func(q *launcher.Request) { q.Source, q.OrderID = "s", "o1" })
	issue := r.mustLaunch(t, func(q *launcher.Request) { q.Issues = []string{keyIssueA} })
	r.wantHeld(t, func(q *launcher.Request) { q.Source, q.OrderID, q.Anyway = "s", "o1", true }, order)
	r.wantHeld(t, func(q *launcher.Request) { q.Issues, q.ChatMessage, q.Anyway = []string{keyIssueA}, "m1", true }, issue)
}

func TestAHandLaunchWithNoSourceIssuesOrChatMessageIsNeverRefusedForAKey(t *testing.T) {
	r := newWakeRig(t, 0)
	a := r.mustLaunch(t, nil)
	b := r.mustLaunch(t, nil)
	if a.Session == b.Session {
		t.Errorf("two hand launches share the session %q", a.Session)
	}
}

func (r *wakeRig) stopShip(t *testing.T, session string) {
	t.Helper()
	mux := http.NewServeMux()
	r.s.Routes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/ships/"+session+"/stop", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("stop = %d %s", rec.Code, rec.Body)
	}
}

func TestAKeyAndItsClaimsAreFreeOnceTheShipIsStopped(t *testing.T) {
	r := newWakeRig(t, 0)
	order := func(q *launcher.Request) {
		q.Source, q.OrderID, q.ChatMessage, q.Issues = "s", "o1", "m1", []string{keyIssueA, keyIssueB}
	}
	first := r.mustLaunch(t, order)
	r.wantHeld(t, order, first)
	r.stopShip(t, first.Session)
	r.mustLaunch(t, order)
}

func TestAKeyIsFreeOnceItsShipSailsOff(t *testing.T) {
	r := newWakeRig(t, 0)
	first := r.mustLaunch(t, chatKey("m1"))
	r.wantHeld(t, chatKey("m1"), first)
	if err := os.WriteFile(filepath.Join(r.rec, "exit"), []byte("0"), 0o600); err != nil {
		t.Fatal(err)
	}
	since := map[string]time.Time{first.Session: time.Now().Add(-2 * SailOffDelay)}
	waitUntil(t, "the clean exit to settle", func() bool {
		_ = r.s.Host.Sync(context.Background())
		r.s.sailOff(context.Background(), since)
		_, listed := r.s.Get(first.Session)
		return !listed
	})
	if err := os.Remove(filepath.Join(r.rec, "exit")); err != nil {
		t.Fatal(err)
	}
	r.mustLaunch(t, chatKey("m1"))
}

func TestASourcelessLaunchOfIssuesTakesTheClaimsOfEveryChild(t *testing.T) {
	r := newWakeRig(t, 0)
	got := r.mustLaunch(t, func(q *launcher.Request) {
		q.Issues = []string{keyIssueA, keyIssueB}
		q.URL, q.Title = "https://github.com/evil/repo/issues/9", "client title" // never trusted
	})
	if got.URL != keyIssueA || len(got.Issues) != 2 || got.Issues[1] != keyIssueB {
		t.Errorf("ship url=%q issues=%v, want url the first issue and both children", got.URL, got.Issues)
	}
	if got.Title == "client title" {
		t.Errorf("ship title = %q, a client's title must not be trusted", got.Title)
	}
	claims := r.s.Claims()
	for _, u := range []string{keyIssueA, keyIssueB} {
		if claims[claimKeyOf(u)] == "" {
			t.Errorf("claims = %v, want %s held by the launch", claims, u)
		}
	}
	// A different launch that overlaps one child, with no key of its own to share, is still refused.
	r.wantHeld(t, func(q *launcher.Request) { q.Issues = []string{keyIssueB} }, got)
}

func TestASourcelessLaunchOfANonIssueURLIs400(t *testing.T) {
	r := newWakeRig(t, 0)
	_, lerr := r.s.Launch(context.Background(), r.keyReq(func(q *launcher.Request) {
		q.Issues = []string{keyIssueA, "https://github.com/acme/widgets/pull/5"}
	}))
	if lerr == nil || lerr.Status != http.StatusBadRequest || !strings.Contains(lerr.Msg, "pull/5") {
		t.Fatalf("Launch with a pull request URL as an issue = %v, want a 400 naming the URL", lerr)
	}
	if len(r.s.List()) != 0 || len(r.s.Claims()) != 0 {
		t.Errorf("the refused launch left ships %v or claims %v", r.s.List(), r.s.Claims())
	}
}

// Two launches of one issue racing each other: exactly one gets a ship, the other is refused.
func TestTwoRacingSourcelessLaunchesOfOneIssueMakeOneShip(t *testing.T) {
	r := newWakeRig(t, 0)
	var wg sync.WaitGroup
	codes := make([]int, 2)
	for i := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, lerr := r.s.Launch(context.Background(), r.keyReq(func(q *launcher.Request) { q.Issues = []string{keyIssueA} }))
			codes[i] = http.StatusCreated
			if lerr != nil {
				codes[i] = lerr.Status
			}
		}()
	}
	wg.Wait()
	if !(codes[0] == http.StatusCreated && codes[1] == http.StatusConflict || codes[0] == http.StatusConflict && codes[1] == http.StatusCreated) {
		t.Errorf("racing launch statuses = %v, want one 201 and one 409", codes)
	}
}

func TestAClientCanNameIssuesOnTheLaunchRoute(t *testing.T) {
	r := newWakeRig(t, 0)
	mux := http.NewServeMux()
	r.s.Routes(mux)
	body := `{"fleet":"work","harness":"claude","dir":"` + r.checkout + `","model":"opus","skill":{"kind":"none"},"text":"go","issues":["` + keyIssueA + `"],"chat_message":"m9"}`
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/ships", strings.NewReader(body)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("launch with issues = %d %s, want 201", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/ships", strings.NewReader(body)))
	if rec.Code != http.StatusConflict {
		t.Fatalf("the same launch again = %d %s, want 409", rec.Code, rec.Body)
	}
}

// restarted is a second Service over the same tmux and home: what a restarted Admiral reads back.
func (r *wakeRig) restarted() *Service {
	return &Service{Host: r.s.Host, State: r.s.State, Hub: server.NewHub(), Outcomes: r.s.Outcomes,
		Launcher: r.s.Launcher, Orders: r.s.Orders, Reserve: r.s.Reserve}
}

func TestAKeyAndItsClaimsSurviveAnAdmiralRestart(t *testing.T) {
	r := newWakeRig(t, 0)
	first := r.mustLaunch(t, func(q *launcher.Request) { q.ChatMessage, q.Issues = "m1", []string{keyIssueA, keyIssueB} })
	s2 := r.restarted()
	for _, mod := range []func(*launcher.Request){chatKey("m1"), func(q *launcher.Request) { q.Issues = []string{keyIssueB, keyIssueA} }} {
		_, lerr := s2.Launch(context.Background(), r.keyReq(mod))
		if lerr == nil || lerr.Status != http.StatusConflict || !strings.Contains(lerr.Msg, first.Name) {
			t.Errorf("Launch on the restarted service = %v, want a 409 naming %q", lerr, first.Name)
		}
	}
}

// moorKeyed launches a ship named Saltpaw with a chat key and issues, then moors it and takes its session away.
func (r *wakeRig) moorKeyed(t *testing.T) Ship {
	t.Helper()
	got := r.mustLaunch(t, func(q *launcher.Request) {
		q.Name, q.Session, q.ChatMessage, q.Issues = "Saltpaw", "saltpaw", "m1", []string{keyIssueA, keyIssueB}
	})
	r.exitAndMoor(t)
	r.kill(t, "saltpaw")
	return got
}

func TestAKeyAndItsClaimsSurviveMooring(t *testing.T) {
	r := newWakeRig(t, 0)
	first := r.moorKeyed(t)
	for _, mod := range []func(*launcher.Request){chatKey("m1"), func(q *launcher.Request) { q.Issues = []string{keyIssueB} }} {
		_, lerr := r.s.Launch(context.Background(), r.keyReq(mod))
		if lerr == nil || lerr.Status != http.StatusConflict || !strings.Contains(lerr.Msg, first.Name) {
			t.Errorf("Launch beside a moored ship = %v, want a 409 naming %q", lerr, first.Name)
		}
	}
}

func TestAMooredShipWakesWithoutItsOwnKeyOrClaimsRefusingIt(t *testing.T) {
	r := newWakeRig(t, 0)
	first := r.moorKeyed(t)
	recs, err := moor.List(r.home)
	if err != nil || len(recs) != 1 {
		t.Fatalf("moored records = %+v, %v", recs, err)
	}
	recs[0].Resume = "/commodore resume run-7"
	if err := moor.Write(r.home, recs[0]); err != nil {
		t.Fatal(err)
	}
	r.s.mooredMu.Lock()
	r.s.mooredLoaded = false
	r.s.mooredMu.Unlock()
	if err := os.Remove(filepath.Join(r.rec, "exit")); err != nil {
		t.Fatal(err)
	}
	if _, lerr := r.s.Wake(context.Background(), "saltpaw", ""); lerr != nil {
		t.Fatalf("Wake = %v, want the ship's own key and claims not to refuse it", lerr)
	}
	if got := r.s.Claims()[claimKeyOf(keyIssueA)]; got == "" {
		t.Errorf("the woken ship lost its claim on %s", keyIssueA)
	}
	r.wantHeld(t, chatKey("m1"), first)
}

func TestAKeyIsFreeOnceItsMooredShipIsDismissed(t *testing.T) {
	r := newWakeRig(t, 0)
	r.moorKeyed(t)
	mux := http.NewServeMux()
	r.s.Routes(mux)
	rec := httptest.NewRecorder()
	_, lerr := r.s.Launch(context.Background(), r.keyReq(chatKey("m1")))
	if lerr == nil || lerr.Status != http.StatusConflict {
		t.Fatalf("Launch beside the moored ship = %v, want a 409", lerr)
	}
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/ships/saltpaw/dismiss", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("dismiss = %d %s", rec.Code, rec.Body)
	}
	r.mustLaunch(t, chatKey("m1"))
	r.mustLaunch(t, func(q *launcher.Request) { q.Issues = []string{keyIssueA, keyIssueB} })
}

func TestASourcedLaunchRefusesClientIssues(t *testing.T) {
	r := newWakeRig(t, 0)
	mux := http.NewServeMux()
	r.s.Routes(mux)
	body := `{"fleet":"work","harness":"claude","dir":"` + r.checkout + `","model":"opus","skill":{"kind":"none"},"source":"s","order_id":"o1","issues":["` + keyIssueB + `"]}`
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/ships", strings.NewReader(body)))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "no source") {
		t.Fatalf("sourced launch with issues = %d %s, want 400 saying issues go on a launch with no source", rec.Code, rec.Body)
	}
	if len(r.s.List()) != 0 {
		t.Errorf("the refused launch left ships %v", r.s.List())
	}
}

func (r *wakeRig) launchActing(fleet string, mod func(*launcher.Request)) (Ship, *launcher.Error) {
	req := r.keyReq(func(q *launcher.Request) { q.Fleet = fleet; mod(q) })
	return r.s.Launch(actor.WithFleet(context.Background(), fleet), req)
}

func TestAnActorRefusedByAnotherFleetsShipLearnsTheFleetNotTheShip(t *testing.T) {
	r := newWakeRig(t, 0)
	first := r.mustLaunch(t, func(q *launcher.Request) { q.Issues = []string{keyIssueA} })
	_, lerr := r.launchActing("home", func(q *launcher.Request) { q.Issues = []string{keyIssueA} })
	if lerr == nil || lerr.Status != http.StatusConflict || strings.Contains(lerr.Msg, first.Name) || !strings.Contains(lerr.Msg, "fleet Work") {
		t.Fatalf("actor launch of another fleet's issue = %v, want a 409 naming fleet Work and not %q", lerr, first.Name)
	}
	r.wantHeld(t, func(q *launcher.Request) { q.Issues = []string{keyIssueA} }, first) // the engineer still gets the ship
	_, lerr = r.launchActing("work", func(q *launcher.Request) { q.Issues = []string{keyIssueA} })
	if lerr == nil || !strings.Contains(lerr.Msg, first.Name) {
		t.Errorf("actor launch in the holder's own fleet = %v, want the ship named", lerr)
	}
}

func TestAChatMessageIdIsScopedToItsFleet(t *testing.T) {
	r := newWakeRig(t, 0)
	r.mustLaunch(t, chatKey("m1"))
	if _, lerr := r.launchActing("home", chatKey("m1")); lerr != nil {
		t.Fatalf("the same chat id from fleet home = %v, want a ship", lerr)
	}
}

func TestASourcelessIssueURLMustBeHttpsOnAHostTheFleetUses(t *testing.T) {
	r := newWakeRig(t, 0)
	for _, u := range []string{"http://github.com/acme/widgets/issues/41", "https://evil.example/acme/widgets/issues/41"} {
		_, lerr := r.s.Launch(context.Background(), r.keyReq(func(q *launcher.Request) { q.Issues = []string{u} }))
		if lerr == nil || lerr.Status != http.StatusBadRequest || !strings.Contains(lerr.Msg, u) {
			t.Errorf("Launch with %s = %v, want a 400 naming it", u, lerr)
		}
	}
	got := r.mustLaunch(t, func(q *launcher.Request) { q.Issues = []string{"https://GitHub.com/Acme/Widgets/issues/41/"} })
	if got.URL != keyIssueA {
		t.Errorf("ship url = %q, want the normalised %q", got.URL, keyIssueA)
	}
}

func TestASourcelessPendingEntryIsDroppedOnceListed(t *testing.T) {
	r := newWakeRig(t, 0)
	r.s.holdUnlisted(OrderKey{OrderID: "gone"}, "no-such-session", "", nil)
	r.s.mu.Lock()
	r.s.listing[OrderKey{OrderID: "gone"}] = unlisted{session: "no-such-session", at: time.Now().Add(-2 * listedWithin)}
	r.s.mu.Unlock()
	r.s.Pending()
	r.s.mu.Lock()
	n := len(r.s.listing)
	r.s.mu.Unlock()
	if n != 0 {
		t.Errorf("%d listing entries after Pending, want the expired sourceless one dropped", n)
	}
}
