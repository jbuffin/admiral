package ships

// Re-review wakes a moored reviewing ship under its name with its Commodore skill on a URL the record carries.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/moor"
)

const (
	layer1 = "https://github.com/acme/widgets/pull/21"
	layer2 = "https://github.com/acme/widgets/pull/22"
	ownPR  = "https://github.com/acme/widgets/pull/9"
)

// rereviewSpec is the moored ship a Re-review test starts from.
type rereviewSpec struct {
	orderURL  string
	prs       []string
	reviewing []string
	skill     moor.Skill
	resume    string
	maxShips  int
}

func commodoreSkill() moor.Skill { return moor.Skill{Kind: "commodore", Name: "commodore"} }

type rereviewRig struct {
	*wakeRig
	mux *http.ServeMux
}

// newRereviewRig moors saltpaw as spec says, with a Commodore the wake can resolve in the checkout and none anywhere else.
func newRereviewRig(t *testing.T, spec rereviewSpec) *rereviewRig {
	t.Helper()
	r := newWakeRig(t, spec.maxShips)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	dir := filepath.Join(r.checkout, ".claude", "skills", "commodore")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: commodore\ndescription: a fake skill\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := moor.Write(r.home, moor.Record{Name: "Saltpaw", Session: "saltpaw", Fleet: "work", Harness: "claude", Model: "opus",
		Skill: spec.skill, Checkout: r.checkout, Source: "s", OrderID: "o1", Panel: "p", URL: spec.orderURL, Title: "Review it",
		PRs: spec.prs, Reviewing: spec.reviewing, Resume: spec.resume, MooredAt: time.Now().Add(-30 * time.Minute).UTC().Truncate(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	r.s.Routes(mux)
	return &rereviewRig{wakeRig: r, mux: mux}
}

func (r *rereviewRig) rereview(session, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/ships/"+session+"/rereview", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.mux.ServeHTTP(rec, req)
	return rec
}

func (r *rereviewRig) nothingRelaunched(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(r.rec, "argv-1")); err == nil {
		t.Error("a ship was started, want the Re-review refused before any side effect")
	}
	if _, err := os.Stat(filepath.Join(r.home, "moored", "saltpaw.json")); err != nil {
		t.Errorf("the moored record is gone: %v", err)
	}
	if got := r.logged(t, "woken"); len(got) != 0 {
		t.Errorf("woken events = %v, want none", got)
	}
}

func singlePR() rereviewSpec {
	return rereviewSpec{orderURL: wakePR, prs: []string{wakePR}, reviewing: []string{wakePR}, skill: commodoreSkill(), resume: resumeText}
}

func TestReReviewOfAReviewedPRRelaunchesTheSameShipWithCommodoreOnThatURLAndNoResume(t *testing.T) {
	spec := singlePR()
	spec.prs, spec.reviewing = []string{wakePR, ownPR}, []string{wakePR}
	r := newRereviewRig(t, spec)

	rec := r.rereview("saltpaw", `{"url":"`+wakePR+`"}`)
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"saltpaw"`) {
		t.Fatalf("rereview = %d %s, want 201 with the ship", rec.Code, rec.Body)
	}
	argv, _ := r.started(t, 1)
	if got := prompt(argv); got != "/commodore "+wakePR {
		t.Errorf("prompt = %q, want %q: Commodore on the PR, not the resume text", got, "/commodore "+wakePR)
	}
	o := r.options(t, "saltpaw")
	if o["round"] != "1" || o["reviewing"] != wakePR || o["source"] != "s" || o["order"] != "o1" {
		t.Errorf("options = %v, want round 1, the order and the record's reviewing role", o)
	}
	if got := r.logged(t, "woken"); len(got) != 1 || got[0]["by"] != "hand" || got[0]["pr"] != wakePR || got[0]["round"] != float64(1) {
		t.Errorf("woken events = %v, want one by hand for round 1 with pr %s", got, wakePR)
	}
}

func TestReReviewOfTheStackOrderURLRelaunchesWithCommodoreOnTheStack(t *testing.T) {
	r := newRereviewRig(t, rereviewSpec{orderURL: reviewStack, prs: []string{layer1, layer2}, reviewing: []string{layer1, layer2},
		skill: commodoreSkill(), resume: resumeText})

	if rec := r.rereview("saltpaw", `{"url":"`+reviewStack+`"}`); rec.Code != http.StatusCreated {
		t.Fatalf("rereview = %d %s, want 201", rec.Code, rec.Body)
	}
	argv, _ := r.started(t, 1)
	if got := prompt(argv); got != "/commodore "+reviewStack {
		t.Errorf("prompt = %q, want %q", got, "/commodore "+reviewStack)
	}
	if o := r.options(t, "saltpaw"); o["reviewing"] != layer1+","+layer2 {
		t.Errorf("reviewing option = %q, want both layers kept", o["reviewing"])
	}
}

func TestReReviewOfOneLayerOfAReviewedStackIsAllowed(t *testing.T) {
	r := newRereviewRig(t, rereviewSpec{orderURL: reviewStack, prs: []string{layer1, layer2}, reviewing: []string{layer1, layer2},
		skill: commodoreSkill(), resume: resumeText})
	if rec := r.rereview("saltpaw", `{"url":"`+layer2+`"}`); rec.Code != http.StatusCreated {
		t.Fatalf("rereview = %d %s, want 201", rec.Code, rec.Body)
	}
	if argv, _ := r.started(t, 1); prompt(argv) != "/commodore "+layer2 {
		t.Errorf("prompt = %q, want Commodore on the layer", prompt(argv))
	}
}

func TestReReviewNamesTheCommodoreTheWakeResolvesNotTheOneTheRecordSaved(t *testing.T) {
	spec := singlePR()
	spec.skill = moor.Skill{Kind: "commodore", Name: "jasonbuffin:commodore"}
	r := newRereviewRig(t, spec)
	if rec := r.rereview("saltpaw", `{"url":"`+wakePR+`"}`); rec.Code != http.StatusCreated {
		t.Fatalf("rereview = %d %s, want 201", rec.Code, rec.Body)
	}
	if argv, _ := r.started(t, 1); prompt(argv) != "/commodore "+wakePR {
		t.Errorf("prompt = %q, want the name the wake resolves in the checkout, not the record's jasonbuffin:commodore", prompt(argv))
	}
}

func TestReReviewRefusesAnyURLTheRecordDoesNotCarryAsReviewedBeforeAnySideEffect(t *testing.T) {
	spec := singlePR()
	spec.prs, spec.reviewing = []string{wakePR, ownPR}, []string{wakePR}
	for _, c := range []struct{ name, url string }{
		{"the ship's own PR", ownPR},
		{"a PR it does not carry", "https://github.com/acme/widgets/pull/99"},
		{"a trailing slash", wakePR + "/"},
		{"another case", "https://github.com/Acme/widgets/pull/7"},
		{"a query string", wakePR + "?x=1"},
		{"a stack the order is not", reviewStack},
		{"empty", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRereviewRig(t, spec)
			rec := r.rereview("saltpaw", `{"url":"`+c.url+`"}`)
			if rec.Code != http.StatusBadRequest || (c.url != "" && !strings.Contains(rec.Body.String(), "not reviewing")) {
				t.Errorf("rereview %q = %d %s, want 400 saying the ship is not reviewing it", c.url, rec.Code, rec.Body)
			}
			r.nothingRelaunched(t)
		})
	}
}

func TestReReviewRefusesAStackURLNearMissesAndAStackWithNoReviewedLayer(t *testing.T) {
	for _, c := range []struct {
		name      string
		reviewing []string
		url       string
	}{
		{"a trailing slash", []string{layer1}, reviewStack + "/"},
		{"another stack", []string{layer1}, "https://github.com/acme/widgets/stack/13"},
		{"the viewer's own stack, no reviewed layer", nil, reviewStack},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRereviewRig(t, rereviewSpec{orderURL: reviewStack, prs: []string{layer1}, reviewing: c.reviewing,
				skill: commodoreSkill(), resume: resumeText})
			if rec := r.rereview("saltpaw", `{"url":"`+c.url+`"}`); rec.Code != http.StatusBadRequest {
				t.Errorf("rereview %q = %d %s, want 400", c.url, rec.Code, rec.Body)
			}
			r.nothingRelaunched(t)
		})
	}
}

func TestReReviewOfAShipNotLaunchedAsCommodoreAnswers409AndRelaunchesNothing(t *testing.T) {
	for _, skill := range []moor.Skill{{Kind: "skill", Name: "commodore"}, {Kind: "none"}} {
		spec := singlePR()
		spec.skill = skill
		r := newRereviewRig(t, spec)
		rec := r.rereview("saltpaw", `{"url":"`+wakePR+`"}`)
		if rec.Code != http.StatusConflict || !strings.Contains(strings.ToLower(rec.Body.String()), "commodore") {
			t.Errorf("rereview of a %+v ship = %d %s, want 409 saying it is not a Commodore ship", skill, rec.Code, rec.Body)
		}
		r.nothingRelaunched(t)
	}
}

func TestAReviewingShipWithNoResumeTextCanStillBeReReviewed(t *testing.T) {
	spec := singlePR()
	spec.resume = ""
	r := newRereviewRig(t, spec)
	if rec := r.rereview("saltpaw", `{"url":"`+wakePR+`"}`); rec.Code != http.StatusCreated {
		t.Fatalf("rereview = %d %s, want 201: the prompt does not use the resume text", rec.Code, rec.Body)
	}
	if argv, _ := r.started(t, 1); prompt(argv) != "/commodore "+wakePR {
		t.Errorf("prompt = %q", prompt(argv))
	}
}

func TestReReviewIsRefusedForAShipThatIsUnknownOrAlreadyLive(t *testing.T) {
	r := newRereviewRig(t, singlePR())
	if rec := r.rereview("no-such-ship", `{"url":"`+wakePR+`"}`); rec.Code != http.StatusNotFound {
		t.Errorf("rereview of an unknown ship = %d %s, want 404", rec.Code, rec.Body)
	}
	if rec := r.rereview("saltpaw", `{"url":"`+wakePR+`"}`); rec.Code != http.StatusCreated {
		t.Fatalf("first rereview = %d %s", rec.Code, rec.Body)
	}
	r.started(t, 1)
	if rec := r.rereview("saltpaw", `{"url":"`+wakePR+`"}`); rec.Code != http.StatusConflict {
		t.Errorf("rereview of a live ship = %d %s, want 409", rec.Code, rec.Body)
	}
}

func TestReReviewWithABodyThatIsNotJSONAnswers400(t *testing.T) {
	r := newRereviewRig(t, singlePR())
	if rec := r.rereview("saltpaw", `not json`); rec.Code != http.StatusBadRequest {
		t.Errorf("rereview = %d %s, want 400", rec.Code, rec.Body)
	}
	r.nothingRelaunched(t)
}

// A hand wake counts toward the cap but the cap never stops it.
func TestReReviewStartsAtTheShipCap(t *testing.T) {
	spec := singlePR()
	spec.maxShips = 1
	r := newRereviewRig(t, spec)
	r.s.holdUnlisted(OrderKey{"s", "other"}, "ship-1", "p", nil)
	if rec := r.rereview("saltpaw", `{"url":"`+wakePR+`"}`); rec.Code != http.StatusCreated {
		t.Errorf("rereview at the cap = %d %s, want 201 like a hand wake", rec.Code, rec.Body)
	}
}
