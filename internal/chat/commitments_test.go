package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/actor"
	"github.com/jbuffin/admiral/internal/capabilities"
	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/inbox"
	"github.com/jbuffin/admiral/internal/ships"
)

type promiseRig struct {
	home, repo, other string
	mux               *http.ServeMux
}

var promiseT0 = time.Date(2026, 11, 12, 8, 0, 0, 0, time.UTC)

// newPromiseRig has fleet home proven for autopilot on claude and fleet work configured but unproven.
func newPromiseRig(t *testing.T) *promiseRig { return newPromiseRigWith(t, nil) }

// newPromiseRigWith is newPromiseRig with tweak applied to the Service before its routes are registered.
func newPromiseRigWith(t *testing.T, tweak func(*Service)) *promiseRig {
	t.Helper()
	home := t.TempDir()
	roots := filepath.Join(home, "code")
	repo := filepath.Join(roots, "widget")
	other := filepath.Join(home, "elsewhere", "thing")
	for _, d := range []string{repo, other} {
		if err := os.MkdirAll(filepath.Join(d, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	raw, _ := json.Marshal(map[string]any{"version": 1,
		"resources": map[string]any{"gpu": map[string]any{"capacity": 1}},
		"fleets": map[string]any{
			"home":   map[string]any{"name": "Home", "search_roots": []string{roots}, "clone_root": filepath.Join(home, "clones"), "claude": map[string]any{"executable": "/bin/sh"}},
			"broken": map[string]any{"name": "Broken", "search_roots": []string{roots}, "claude": map[string]any{"executable": "/nonexistent/claude"}},
			"cx":     map[string]any{"name": "Cx", "search_roots": []string{roots}, "codex": map[string]any{"executable": "/bin/sh", "args": []string{"--remote", "x"}}},
			"work":   map[string]any{"name": "Work", "search_roots": []string{roots}, "claude": map[string]any{"executable": "/bin/sh"}},
		},
		"sources": map[string]any{"tracker": map[string]any{"plugin": "jira", "fleet": "home"}, "theirs": map[string]any{"plugin": "jira", "fleet": "work"}},
	})
	cfg, err := config.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	store := capabilities.Open(home)
	if err := store.RecordVersion("home", "claude", "2.1.300", promiseT0); err != nil {
		t.Fatal(err)
	}
	for _, id := range []capabilities.ID{capabilities.AutopilotGuard, capabilities.JudgeNoTools, capabilities.JudgeNoNetwork} {
		if err := store.Record("home", "claude", "2.1.300", id, capabilities.Result{Passed: true, At: promiseT0}); err != nil {
			t.Fatal(err)
		}
	}
	mux := http.NewServeMux()
	svc := &Service{
		Home:   home,
		Config: func() *config.Config { return cfg },
		Ships: func() []ships.Ship {
			return []ships.Ship{{Session: "saltpaw", Fleet: "home", Launched: "2026-11-12T06:00:00Z"}, {Session: "bare", Fleet: "home"}, {Session: "rival", Fleet: "work"}, {Session: "hulk", Fleet: "broken"}, {Session: "cxship", Fleet: "cx"}, {Session: "stamped", Fleet: "home", Launched: "2026-11-12T07:00:00Z"}}
		},
		SourceFleet: func(key string) (string, bool) {
			return map[string]string{"tracker": "home", "theirs": "work"}[key], key == "tracker" || key == "theirs"
		},
		OrderExists: func(source, id string) bool { return source == "tracker" && id == "T-1" },
		OrderRepo: func(source, id string) (string, string, bool) {
			return "do T-1", "https://github.com/acme/widget", source == "tracker" && id == "T-1"
		},
	}
	if tweak != nil {
		tweak(svc)
	}
	svc.Routes(mux)
	return &promiseRig{home: home, repo: repo, other: other, mux: mux}
}

func (r *promiseRig) do(method, target, body, fleet string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if fleet != "" {
		req = req.WithContext(actor.WithFleet(context.Background(), fleet))
	}
	rec := httptest.NewRecorder()
	r.mux.ServeHTTP(rec, req)
	return rec
}

func (r *promiseRig) ledger(fleet string) string {
	b, _ := os.ReadFile(filepath.Join(r.home, "chat", fleet, "ledger.jsonl"))
	return string(b)
}

type promiseBody struct {
	ID, Text, Action, Via string
	Trigger               struct{ Kind, Subject string }
	Waiting               bool
}

func (r *promiseRig) list(t *testing.T, fleet string) []promiseBody {
	t.Helper()
	rec := r.do("GET", "/api/chat/"+fleet+"/commitments", "", "")
	var got struct{ Commitments []promiseBody }
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &got) != nil {
		t.Fatalf("GET commitments for %s = %d %s, want 200 {\"commitments\": [...]}", fleet, rec.Code, rec.Body)
	}
	return got.Commitments
}

func (r *promiseRig) launch(extra string) string {
	spec := fmt.Sprintf(`"harness":"claude","dir":%q,"text":"fix the build","skill":{"kind":"none"},"autopilot":true`, r.repo)
	if extra != "" {
		spec += "," + extra
	}
	return "{" + spec + "}"
}

func (r *promiseRig) body(trigger, action, launch string) string {
	b := fmt.Sprintf(`{"text":"  when saltpaw sails, fix the build ","trigger":%s,"action":%q`, trigger, action)
	if launch != "" {
		b += `,"launch":` + launch
	}
	return b + "}"
}

const sailed = `{"kind":"ship_sailed","subject":"saltpaw"}`

func TestACommitmentAdmiralBotRecordsIsStoredStampedAndListed(t *testing.T) {
	r := newPromiseRig(t)
	rec := r.do("POST", "/api/chat/commitments", r.body(sailed, "launch", r.launch("")), "home")
	var c promiseBody
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &c) != nil || c.ID == "" {
		t.Fatalf("POST commitment = %d %s, want 201 {id, text, trigger, action, via}", rec.Code, rec.Body)
	}
	if c.Text != "when saltpaw sails, fix the build" || c.Via != "admiralbot" || c.Action != "launch" ||
		c.Trigger.Kind != "ship_sailed" || c.Trigger.Subject != "saltpaw" {
		t.Errorf("commitment = %+v, want the trimmed text, via admiralbot, action launch and trigger ship_sailed on saltpaw", c)
	}
	if l := r.ledger("home"); !strings.Contains(l, `"event":"added"`) || !strings.Contains(l, `"kind":"commitment"`) || !strings.Contains(l, `"harness":"claude"`) || !strings.Contains(l, r.repo) {
		t.Errorf("home's ledger = %q, want one added commitment event carrying the launch spec", l)
	}
	if got := r.list(t, "home"); len(got) != 1 || got[0].ID != c.ID || got[0].Waiting {
		t.Errorf("listed = %+v, want exactly %s, not waiting", got, c.ID)
	}
	if got := r.list(t, "work"); len(got) != 0 {
		t.Errorf("work lists %+v, want nothing: the commitment is home's", got)
	}
}

func TestARemindCommitmentNeedsNoLaunchSpecAndGoesToTheTokensFleet(t *testing.T) {
	r := newPromiseRig(t)
	rec := r.do("POST", "/api/chat/commitments", `{"fleet":"work","text":"tell me about the deploy","trigger":{"kind":"none"},"action":"remind"}`, "home")
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST remind = %d %s, want 201", rec.Code, rec.Body)
	}
	if len(r.list(t, "home")) != 1 || len(r.list(t, "work")) != 0 || r.ledger("work") != "" {
		t.Error("the token's fleet home must hold the commitment and work none, whatever the body names")
	}
}

func TestTheEngineersOwnRequestCannotRecordACommitment(t *testing.T) {
	r := newPromiseRig(t)
	if rec := r.do("POST", "/api/chat/commitments", `{"text":"x","trigger":{"kind":"none"},"action":"remind"}`, ""); rec.Code != http.StatusForbidden {
		t.Errorf("POST commitment with no actor = %d %s, want 403", rec.Code, rec.Body)
	}
}

func TestTheEngineerCancelsACommitmentOnceAndThenItIsGone(t *testing.T) {
	r := newPromiseRig(t)
	var c promiseBody
	rec := r.do("POST", "/api/chat/commitments", `{"text":"remind me","trigger":{"kind":"none"},"action":"remind"}`, "home")
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &c) != nil || c.ID == "" {
		t.Fatalf("POST = %d %s, want 201", rec.Code, rec.Body)
	}
	if rec := r.do("DELETE", "/api/chat/home/commitments/"+c.ID, "", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE = %d %s, want 204", rec.Code, rec.Body)
	}
	if got := r.list(t, "home"); len(got) != 0 {
		t.Errorf("listed after cancel = %+v, want none", got)
	}
	rec = r.do("DELETE", "/api/chat/home/commitments/"+c.ID, "", "")
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "commitment") {
		t.Errorf("second DELETE = %d %s, want 404 with commitment wording", rec.Code, rec.Body)
	}
}

func TestACommitmentIdCannotBeCancelledThroughAnotherFleetOrAsANote(t *testing.T) {
	r := newPromiseRig(t)
	var c promiseBody
	rec := r.do("POST", "/api/chat/commitments", `{"text":"remind me","trigger":{"kind":"none"},"action":"remind"}`, "home")
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &c) != nil {
		t.Fatalf("POST = %d %s, want 201", rec.Code, rec.Body)
	}
	if rec := r.do("DELETE", "/api/chat/work/commitments/"+c.ID, "", ""); rec.Code != http.StatusNotFound {
		t.Errorf("DELETE through work = %d, want 404", rec.Code)
	}
	if rec := r.do("DELETE", "/api/chat/home/notes/"+c.ID, "", ""); rec.Code != http.StatusNotFound {
		t.Errorf("DELETE as a note = %d, want 404", rec.Code)
	}
	if len(r.list(t, "home")) != 1 {
		t.Error("the commitment must still be open")
	}
}

func cloneSpec(extra string) string {
	spec := `{"harness":"claude","clone":true,"skill":{"kind":"none"},"autopilot":true`
	if !strings.Contains(extra, `"text"`) {
		spec += `,"text":"fix the build"`
	}
	return spec + "," + extra + "}"
}

func TestARecordTimeRefusalIs4xxStoresNothingAndPostsNoInboxNotice(t *testing.T) {
	past := promiseT0.Add(-time.Hour).Format(time.RFC3339)
	cases := []struct {
		name, fleet string
		body        func(*promiseRig) string
	}{
		{"an unknown trigger", "home", func(r *promiseRig) string { return r.body(`{"kind":"full_moon"}`, "remind", "") }},
		{"an unknown action", "home", func(r *promiseRig) string { return r.body(sailed, "explode", "") }},
		{"a ship of another fleet", "home", func(r *promiseRig) string { return r.body(`{"kind":"ship_sailed","subject":"rival"}`, "remind", "") }},
		{"a ship with no launch stamp", "home", func(r *promiseRig) string { return r.body(`{"kind":"ship_sailed","subject":"bare"}`, "remind", "") }},
		{"a ship that does not exist", "home", func(r *promiseRig) string { return r.body(`{"kind":"ship_sailed","subject":"ghost"}`, "remind", "") }},
		{"a flag as the PR subject", "home", func(r *promiseRig) string { return r.body(`{"kind":"pr_merged","subject":"-R x/y"}`, "remind", "") }},
		{"a bare owner/repo#N as the PR subject", "home", func(r *promiseRig) string {
			return r.body(`{"kind":"pr_merged","subject":"acme/widget#12"}`, "remind", "")
		}},
		{"a PR URL on an unknown host", "home", func(r *promiseRig) string {
			return r.body(`{"kind":"pr_merged","subject":"https://evil.example/acme/widget/pull/7"}`, "remind", "")
		}},
		{"an issue URL for pr_merged", "home", func(r *promiseRig) string {
			return r.body(`{"kind":"pr_merged","subject":"https://github.com/acme/widget/issues/7"}`, "remind", "")
		}},
		{"a PR URL for issue_closed", "home", func(r *promiseRig) string {
			return r.body(`{"kind":"issue_closed","subject":"https://github.com/acme/widget/pull/7"}`, "remind", "")
		}},
		{"a time in the past", "home", func(r *promiseRig) string { return r.body(`{"kind":"time","subject":"`+past+`"}`, "remind", "") }},
		{"a time that is not a time", "home", func(r *promiseRig) string { return r.body(`{"kind":"time","subject":"tomorrow"}`, "remind", "") }},
		{"a launch with no spec", "home", func(r *promiseRig) string { return r.body(sailed, "launch", "") }},
		{"a spec naming another fleet", "home", func(r *promiseRig) string { return r.body(sailed, "launch", r.launch(`"fleet":"work"`)) }},
		{"another fleet's source", "home", func(r *promiseRig) string {
			return r.body(sailed, "launch", r.launch(`"source":"theirs","order_id":"T-1"`))
		}},
		{"an order that does not exist", "home", func(r *promiseRig) string {
			return r.body(sailed, "launch", r.launch(`"source":"tracker","order_id":"T-404"`))
		}},
		{"issues beside a source", "home", func(r *promiseRig) string {
			return r.body(sailed, "launch", r.launch(`"source":"tracker","order_id":"T-1","issues":["https://github.com/acme/widget/issues/1"]`))
		}},
		{"an unknown harness", "home", func(r *promiseRig) string {
			return r.body(sailed, "launch", strings.Replace(r.launch(""), `"claude"`, `"emacs"`, 1))
		}},
		{"a dir outside the fleet's roots", "home", func(r *promiseRig) string {
			return r.body(sailed, "launch", strings.Replace(r.launch(""), r.repo, r.other, 1))
		}},
		{"an unknown resource", "home", func(r *promiseRig) string { return r.body(sailed, "launch", r.launch(`"resources":["nope"]`)) }},
		{"control characters in the prompt", "home", func(r *promiseRig) string {
			return r.body(sailed, "launch", strings.Replace(r.launch(""), "fix the build", `fix\u001b[31m`, 1))
		}},
		{"a dock whose executable does not resolve", "broken", func(r *promiseRig) string {
			return r.body(`{"kind":"ship_sailed","subject":"hulk"}`, "launch", r.launch(""))
		}},
		{"a codex dock with a refused arg", "cx", func(r *promiseRig) string {
			return r.body(`{"kind":"ship_sailed","subject":"cxship"}`, "launch", strings.Replace(r.launch(""), `"claude"`, `"codex"`, 1))
		}},
		{"a Commodore that does not exist", "home", func(r *promiseRig) string { return r.body(sailed, "launch", r.launch(`"member":"nobody"`)) }},
		{"a clone beside a dir", "home", func(r *promiseRig) string { return r.body(sailed, "launch", r.launch(`"clone":true`)) }},
		{"a clone whose order is missing", "home", func(r *promiseRig) string {
			return r.body(sailed, "launch", cloneSpec(`"source":"tracker","order_id":"T-404"`))
		}},
		{"a clone on a host the fleet does not know", "home", func(r *promiseRig) string {
			return r.body(sailed, "launch", cloneSpec(`"text":"https://evil.example/acme/widget"`))
		}},
		{"a clone naming no repository", "home", func(r *promiseRig) string { return r.body(sailed, "launch", cloneSpec(`"text":"fix it"`)) }},
		{"a clone of a repo the form did not show", "home", func(r *promiseRig) string {
			return r.body(sailed, "launch", cloneSpec(`"source":"tracker","order_id":"T-1","clone_repo":"github.com/acme/other"`))
		}},
		{"a clone into a fleet with no clone_root", "broken", func(r *promiseRig) string {
			return r.body(`{"kind":"ship_sailed","subject":"hulk"}`, "launch", cloneSpec(`"text":"github.com/acme/widget"`))
		}},
		{"an unproven autopilot gate", "work", func(r *promiseRig) string {
			return r.body(`{"kind":"ship_sailed","subject":"rival"}`, "launch", r.launch(""))
		}},
		{"a body over 16 KiB", "home", func(r *promiseRig) string {
			return r.body(sailed, "launch", fmt.Sprintf(`{"harness":"claude","dir":%q,"text":%q,"autopilot":true}`, r.repo, strings.Repeat("x", 17<<10)))
		}},
		{"two JSON values in one body", "home", func(r *promiseRig) string {
			return `{"text":"a","trigger":{"kind":"none"},"action":"remind"}{"text":"b"}`
		}},
		{"empty text", "home", func(r *promiseRig) string { return `{"text":" ","trigger":{"kind":"none"},"action":"remind"}` }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newPromiseRig(t)
			rec := r.do("POST", "/api/chat/commitments", c.body(r), c.fleet)
			if rec.Code < 400 || rec.Code > 499 || rec.Body.Len() == 0 {
				t.Errorf("POST = %d %s, want a 4xx with a reason", rec.Code, rec.Body)
			}
			if l := r.ledger(c.fleet); l != "" {
				t.Errorf("ledger = %q, want nothing stored", l)
			}
			if n := inbox.OpenNotices(r.home); len(n) != 0 {
				t.Errorf("Inbox notices = %+v, want none: a record-time refusal never posts one", n)
			}
		})
	}
}

func TestAPRAndAnIssueOnAFleetGitHubHostAreAcceptedAsSubjects(t *testing.T) {
	r := newPromiseRig(t)
	for _, c := range []struct{ kind, subject string }{
		{"pr_merged", "https://github.com/acme/widget/pull/7"},
		{"pr_closed", "https://github.com/acme/widget/pull/8"},
		{"issue_closed", "https://github.com/acme/widget/issues/9"},
		{"time", time.Now().Add(72 * time.Hour).UTC().Format(time.RFC3339)},
	} {
		body := fmt.Sprintf(`{"text":"watch %s","trigger":{"kind":%q,"subject":%q},"action":"remind"}`, c.kind, c.kind, c.subject)
		if rec := r.do("POST", "/api/chat/commitments", body, "home"); rec.Code != http.StatusCreated {
			t.Errorf("%s %s: POST = %d %s, want 201", c.kind, c.subject, rec.Code, rec.Body)
		}
	}
}

func TestAValidLaunchSpecWithAnOrderAndAResourceIsAccepted(t *testing.T) {
	r := newPromiseRig(t)
	spec := fmt.Sprintf(`{"harness":"claude","dir":%q,"text":"do T-1","skill":{"kind":"none"},"source":"tracker","order_id":"T-1","resources":["gpu"],"autopilot":true}`, r.repo)
	if rec := r.do("POST", "/api/chat/commitments", r.body(sailed, "launch", spec), "home"); rec.Code != http.StatusCreated {
		t.Errorf("POST = %d %s, want 201", rec.Code, rec.Body)
	}
}

func TestTheListHoldsOpenAndWaitingCommitmentsAndMarksWhichAndOmitsClosedOnes(t *testing.T) {
	r := newPromiseRig(t)
	dir := filepath.Join(r.home, "chat", "home")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	added := func(id string) string {
		return fmt.Sprintf(`{"v":1,"ts":"2026-11-12T09:00:00Z","event":"added","id":%q,"kind":"commitment","text":"text of %s","trigger":{"kind":"none"},"action":"launch","launch":{"harness":"claude"}}`, id, id)
	}
	lines := []string{
		added("c-open01"), added("c-wait01"), added("c-done01"), added("c-drop01"), added("c-canc01"),
		`{"v":1,"ts":"2026-11-12T09:01:00Z","event":"fired","id":"c-wait01"}`,
		`{"v":1,"ts":"2026-11-12T09:01:00Z","event":"fired","id":"c-done01"}`,
		`{"v":1,"ts":"2026-11-12T09:02:00Z","event":"done","id":"c-done01","reason":"launched"}`,
		`{"v":1,"ts":"2026-11-12T09:02:00Z","event":"dropped","id":"c-drop01","reason":"gone"}`,
		`{"v":1,"ts":"2026-11-12T09:02:00Z","event":"cancelled","id":"c-canc01"}`,
	}
	if err := os.WriteFile(filepath.Join(dir, "ledger.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, c := range r.list(t, "home") {
		got[c.ID] = c.Waiting
	}
	if len(got) != 2 || got["c-open01"] || !got["c-wait01"] {
		t.Errorf("listed = %v, want only c-open01 (not waiting) and c-wait01 (waiting)", got)
	}
}

func TestACloneLaunchIsAcceptedAndStoredWithShapeChecksOnly(t *testing.T) {
	r := newPromiseRig(t)
	for name, extra := range map[string]string{
		"an order's repo":    `"source":"tracker","order_id":"T-1","clone_repo":"GitHub.com/ACME/widget"`,
		"a repo in the text": `"text":"https://github.com/acme/widget"`,
	} {
		rec := r.do("POST", "/api/chat/commitments", r.body(sailed, "launch", cloneSpec(extra)), "home")
		if rec.Code != http.StatusCreated {
			t.Errorf("%s: POST = %d %s, want 201", name, rec.Code, rec.Body)
		}
	}
	if got := strings.Count(r.ledger("home"), `"clone":true`); got != 2 {
		t.Errorf("ledger holds %d clone specs, want 2:\n%s", got, r.ledger("home"))
	}
}

func TestAShipTriggerIsStoredWithTheShipsLaunchStamp(t *testing.T) {
	r := newPromiseRig(t)
	body := r.body(`{"kind":"ship_anchored","subject":"stamped"}`, "remind", "")
	if rec := r.do("POST", "/api/chat/commitments", body, "home"); rec.Code != http.StatusCreated {
		t.Fatalf("POST = %d %s, want 201", rec.Code, rec.Body)
	}
	if l := r.ledger("home"); !strings.Contains(l, `"launched":"2026-11-12T07:00:00Z"`) {
		t.Errorf("ledger = %s, want the trigger to carry the ship's launch stamp", l)
	}
}

func TestSubjectsAreStoredCanonically(t *testing.T) {
	r := newPromiseRig(t)
	at := time.Now().Add(72 * time.Hour).In(time.FixedZone("x", 5*3600)).Truncate(time.Second)
	for _, c := range []struct{ kind, sent, want string }{
		{"pr_merged", "  https://GitHub.com/acme/widget/pull/7 ", "https://github.com/acme/widget/pull/7"},
		{"time", " " + at.Format(time.RFC3339) + " ", at.UTC().Format(time.RFC3339)},
	} {
		body := fmt.Sprintf(`{"text":"w","trigger":{"kind":%q,"subject":%q},"action":"remind"}`, c.kind, c.sent)
		if rec := r.do("POST", "/api/chat/commitments", body, "home"); rec.Code != http.StatusCreated {
			t.Fatalf("%s: POST = %d %s, want 201", c.kind, rec.Code, rec.Body)
		}
		if l := r.ledger("home"); !strings.Contains(l, `"subject":"`+c.want+`"`) {
			t.Errorf("%s: ledger = %s, want subject %q", c.kind, l, c.want)
		}
	}
}

func TestANilOrderSeamRefusesInsteadOfPanicking(t *testing.T) {
	mux := http.NewServeMux()
	home := t.TempDir()
	cfg, _ := config.Parse([]byte(`{"version":1,"fleets":{"home":{"name":"Home","search_roots":["` + home + `"],"claude":{"executable":"/bin/sh"}}}}`))
	(&Service{Home: home, Config: func() *config.Config { return cfg }, Ships: func() []ships.Ship { return nil }}).Routes(mux)
	spec := fmt.Sprintf(`{"harness":"claude","dir":%q,"text":"x","skill":{"kind":"none"},"source":"tracker","order_id":"T-1","autopilot":true}`, home)
	body := `{"text":"w","trigger":{"kind":"none"},"action":"launch","launch":` + spec + `}`
	req := httptest.NewRequest("POST", "/api/chat/commitments", strings.NewReader(body))
	req = req.WithContext(actor.WithFleet(context.Background(), "home"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code < 400 || rec.Code > 499 {
		t.Errorf("POST = %d %s, want a 4xx", rec.Code, rec.Body)
	}
}
