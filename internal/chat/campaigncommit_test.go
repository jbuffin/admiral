package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/jbuffin/admiral/internal/campaigns/follow"
	"github.com/jbuffin/admiral/internal/launcher"
)

const campaignURL = "https://github.com/acme/widgets/issues/20"

// fakeFollow follows each reference once, onto the fleet the test names.
type fakeFollow struct {
	mu    sync.Mutex
	fleet string
	seen  map[string]follow.Followed
	refs  []string
}

func (f *fakeFollow) follow(_ context.Context, ref string) (follow.Followed, bool, *launcher.Error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refs = append(f.refs, ref)
	if ref == "nope" {
		return follow.Followed{}, false, &launcher.Error{Status: http.StatusBadRequest, Msg: "not a reference"}
	}
	if got, ok := f.seen[ref]; ok {
		return got, false, nil
	}
	if f.seen == nil {
		f.seen = map[string]follow.Followed{}
	}
	got := follow.Followed{ID: "camp-1", URL: campaignURL, Fleet: f.fleet}
	f.seen[ref] = got
	return got, true, nil
}

// owner answers like the follow service's route: nope is no reference, and no issue number matters.
func (f *fakeFollow) owner(ref string) (string, *launcher.Error) {
	if ref == "nope" {
		return "", &launcher.Error{Status: http.StatusBadRequest, Msg: "not a reference"}
	}
	return f.fleet, nil
}

func (f *fakeFollow) reads() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.refs...)
}

func newCampaignRig(t *testing.T, campaignFleet string) (*promiseRig, *fakeFollow) {
	t.Helper()
	ff := &fakeFollow{fleet: campaignFleet}
	return newPromiseRigWith(t, func(s *Service) { s.FollowCampaign, s.CampaignFleet = ff.follow, ff.owner }), ff
}

type workBody struct {
	ID, Text, Action, Via string
	Harness               string
	Trigger               struct{ Kind, Subject string }
}

func (r *promiseRig) workOn(t *testing.T, ref, harness, fleet string) (int, workBody, string) {
	t.Helper()
	rec := r.do("POST", "/api/chat/campaign-commitments", `{"reference":"`+ref+`","harness":"`+harness+`"}`, fleet)
	var c workBody
	_ = json.Unmarshal(rec.Body.Bytes(), &c)
	return rec.Code, c, rec.Body.String()
}

func TestWorkingOnACampaignRecordsOneCommitmentThatEndsWithIt(t *testing.T) {
	r, _ := newCampaignRig(t, "home")
	code, c, body := r.workOn(t, "acme/widgets#20", "claude", "home")
	if code != http.StatusCreated || c.ID == "" {
		t.Fatalf("POST campaign-commitments = %d %s, want 201 with the commitment", code, body)
	}
	if c.Trigger.Kind != "campaign" || c.Trigger.Subject != "camp-1" || c.Action != "work" || c.Via != "admiralbot" || c.Harness != "claude" {
		t.Errorf("commitment = %+v, want trigger campaign on camp-1, action work, via admiralbot, harness claude", c)
	}
	if !strings.Contains(c.Text, "acme/widgets#20") {
		t.Errorf("text = %q, want it to name acme/widgets#20", c.Text)
	}
	if got := r.list(t, "home"); len(got) != 1 || got[0].ID != c.ID {
		t.Errorf("listed = %+v, want the one commitment", got)
	}
}

func TestASecondWorkOnReturnsTheOpenCommitmentWithOk(t *testing.T) {
	r, _ := newCampaignRig(t, "home")
	_, first, _ := r.workOn(t, "acme/widgets#20", "claude", "home")
	code, second, body := r.workOn(t, campaignURL, "claude", "home")
	if code != http.StatusOK || second.ID != first.ID || first.ID == "" {
		t.Errorf("second work-on = %d %s, want 200 with commitment %q", code, body, first.ID)
	}
	if n := len(r.list(t, "home")); n != 1 {
		t.Errorf("%d commitments listed, want 1", n)
	}
}

func TestWorkingOnAnotherFleetsCampaignIsRefusedAndRecordsNothing(t *testing.T) {
	r, _ := newCampaignRig(t, "work")
	code, _, body := r.workOn(t, "acme/widgets#20", "claude", "home")
	if code != http.StatusForbidden {
		t.Errorf("work-on of fleet work's campaign by fleet home = %d %s, want 403", code, body)
	}
	if got := r.ledger("home") + r.ledger("work"); strings.Contains(got, "camp-1") {
		t.Errorf("a refused work-on was recorded: %s", got)
	}
}

func TestWorkingOnAnotherFleetsCampaignReadsAndFollowsNothingAndNeverSaysWhetherItExists(t *testing.T) {
	r, ff := newCampaignRig(t, "work")
	code1, _, body1 := r.workOn(t, "acme/widgets#20", "claude", "home")
	code2, _, body2 := r.workOn(t, "acme/widgets#99", "claude", "home")
	if code1 != http.StatusForbidden || code1 != code2 || body1 != body2 {
		t.Errorf("answers differ: %d %s vs %d %s, want one fixed 403", code1, body1, code2, body2)
	}
	if got := ff.reads(); len(got) != 0 {
		t.Errorf("a refused work-on followed %v, want no read and no follow", got)
	}
}

func TestWorkingOnACampaignNeedsAnActor(t *testing.T) {
	r, _ := newCampaignRig(t, "home")
	if code, _, body := r.workOn(t, "acme/widgets#20", "claude", ""); code != http.StatusForbidden {
		t.Errorf("work-on with no actor = %d %s, want 403", code, body)
	}
}

func TestWorkingOnACampaignWithAnUnprovenHarnessIsRefused(t *testing.T) {
	r, ff := newCampaignRig(t, "work")
	code, _, body := r.workOn(t, "acme/widgets#20", "claude", "work")
	if code != http.StatusUnprocessableEntity || !strings.Contains(strings.ToLower(body), "autopilot") {
		t.Errorf("work-on with claude unproven on fleet work = %d %s, want 422 naming autopilot", code, body)
	}
	if strings.Contains(r.ledger("work"), "camp-1") {
		t.Error("an unproven work-on was recorded")
	}
	if got := ff.reads(); len(got) != 0 {
		t.Errorf("an unproven work-on followed %v, want no read and no follow", got)
	}
}

func TestWorkingOnAReferenceThatIsNoCampaignPassesTheFollowRefusalOn(t *testing.T) {
	r, _ := newCampaignRig(t, "home")
	if code, _, body := r.workOn(t, "nope", "claude", "home"); code != http.StatusBadRequest {
		t.Errorf("work-on of a bad reference = %d %s, want the follow refusal's 400", code, body)
	}
}

func TestTheGenericCommitmentRouteRefusesACampaignCommitment(t *testing.T) {
	r, _ := newCampaignRig(t, "home")
	rec := r.do("POST", "/api/chat/commitments", `{"text":"work","trigger":{"kind":"campaign","subject":"camp-1"},"action":"work"}`, "home")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("generic route with a campaign trigger = %d %s, want 422", rec.Code, rec.Body)
	}
	if strings.Contains(r.ledger("home"), "camp-1") {
		t.Error("the generic route recorded a campaign commitment")
	}
}

func TestMakeCommitmentStillRefusesALaunchFromTheCampaignSource(t *testing.T) {
	r, _ := newCampaignRig(t, "home")
	launch := r.launch(`"source":"campaign","order_id":"camp-1/` + campaignURL + `"`)
	rec := r.do("POST", "/api/chat/commitments", r.body(sailed, "launch", launch), "home")
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "campaign") {
		t.Errorf("launch commitment from source campaign = %d %s, want 422 naming the campaign", rec.Code, rec.Body)
	}
}
