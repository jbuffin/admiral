package crewapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/capabilities"
	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/crew"
	"github.com/jbuffin/admiral/internal/crewapi"
	"github.com/jbuffin/admiral/internal/judge"
)

const crewWriterFirstLine = "You write one Admiral crew member."

type draftFields = map[string]any

type problem struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

type draftResp struct {
	Draft    map[string]any `json:"draft"`
	Problems []problem      `json:"problems"`
	Error    string         `json:"error"`
	Kind     string         `json:"kind"`
	Reason   string         `json:"reason"`
	Field    string         `json:"field"`
}

// judgeRig is a fixture whose fleet has a claude dock proven for judge calls and whose Service.Judge is a recorder.
type judgeRig struct {
	*fixture
	mu    sync.Mutex
	calls []judge.Call
	reply func(ctx context.Context, call judge.Call) (judge.Reply, error)
}

func validDraft() draftFields {
	return draftFields{"name": "quartermaster", "role": "implementer", "based-on": "", "skills": []any{}, "model": "", "pick-me-when": "Pick for provisioning chores.", "instructions": "Check the stores before each voyage."}
}

func with(base draftFields, over draftFields) draftFields {
	out := draftFields{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range over {
		out[k] = v
	}
	return out
}

func fakeClaude(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "..", "e2e", "fakeharness", "claude"))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// proveJudge records the version and the judge proofs of the fleet work's claude, as a Check again does.
func proveJudge(t *testing.T, home string) {
	t.Helper()
	store := capabilities.Open(home)
	if err := store.RecordVersion("work", "claude", "2.1.300 (fake)", time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, id := range []capabilities.ID{capabilities.JudgeNoTools, capabilities.JudgeNoNetwork, capabilities.JudgeJSONSchema, capabilities.JudgeCallCap} {
		if err := store.Record("work", "claude", "2.1.300 (fake)", id, capabilities.Result{Passed: true, At: time.Now(), Detail: "seeded"}); err != nil {
			t.Fatal(err)
		}
	}
}

// newJudgeRig serves the crew routes with a Service.Judge that answers fields (or the rig's own reply func) and records each call.
func newJudgeRig(t *testing.T, proven bool, extraDocks string) *judgeRig {
	t.Helper()
	cfg := `{"version":1,"fleets":{"work":{"name":"Work","claude":{"executable":` + quote(fakeClaude(t)) + `}` + extraDocks + `}}}`
	root, _ := filepath.EvalSymlinks(t.TempDir())
	f := &fixture{t: t, home: filepath.Join(root, "home")}
	if err := os.MkdirAll(f.home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.home, config.FileName), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := config.Open(f.home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	if proven {
		proveJudge(t, f.home)
	}
	r := &judgeRig{fixture: f}
	mux := http.NewServeMux()
	(&crewapi.Service{Home: f.home, Config: m, Judge: r.judge}).Routes(mux)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return r
}

func quote(s string) string { b, _ := json.Marshal(s); return string(b) }

func (r *judgeRig) judge(ctx context.Context, c judge.Choice, call judge.Call) (judge.Reply, error) {
	r.mu.Lock()
	r.calls = append(r.calls, call)
	reply := r.reply
	r.mu.Unlock()
	if c.Name != "claude" {
		r.t.Errorf("judge harness = %q, want the fleet's proven claude", c.Name)
	}
	if reply == nil {
		return judge.Reply{Fields: validDraft()}, nil
	}
	return reply(ctx, call)
}

func (r *judgeRig) answers(fields draftFields) {
	r.reply = func(context.Context, judge.Call) (judge.Reply, error) { return judge.Reply{Fields: fields}, nil }
}

func (r *judgeRig) called() []judge.Call {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]judge.Call(nil), r.calls...)
}

func (r *judgeRig) draft(description string) (int, draftResp) {
	r.t.Helper()
	status, body := r.do("POST", "/api/fleets/work/crew/draft", map[string]any{"description": description})
	var out draftResp
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		r.t.Fatalf("draft answered %d %q, which is not JSON", status, body)
	}
	return status, out
}

func (r *judgeRig) rawDraft(description string) (int, string) {
	return r.do("POST", "/api/fleets/work/crew/draft", map[string]any{"description": description})
}

func (r *judgeRig) wroteNothing() {
	r.t.Helper()
	if exists(filepath.Join(r.home, "fleets")) {
		r.t.Error("the draft call created files under fleets/")
	}
}

func TestDraftAnswersTheJudgesDraftWithEmptyFieldsKeptAndNoProblems(t *testing.T) {
	commodoreAt(t)
	r := newJudgeRig(t, true, "")
	status, got := r.draft("A quartermaster who checks the stores.")
	if status != 200 {
		t.Fatalf("status = %d, want 200", status)
	}
	want := validDraft()
	want["harness"] = ""
	for k, v := range want {
		if gv, ok := got.Draft[k]; !ok || !jsonEqual(gv, v) {
			t.Errorf("draft[%q] = %v (present %v), want %v", k, gv, ok, v)
		}
	}
	if len(got.Draft) != len(want) {
		t.Errorf("draft keys = %v, want exactly %d", got.Draft, len(want))
	}
	if got.Problems == nil || len(got.Problems) != 0 {
		t.Errorf("problems = %v, want an empty list", got.Problems)
	}
	if _, body := r.rawDraft("again"); !strings.Contains(body, `"problems":[]`) {
		t.Errorf("body = %s, want problems as [] and not null", body)
	}
	r.wroteNothing()
}

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

func TestDraftReportsOneProblemPerFailedCheckAndStillAnswers200(t *testing.T) {
	builtIn := commodoreAt(t)
	builtInMember(t, builtIn, "scout", "scribe", "Built-in scout.")
	twentyOne := make([]any, 21)
	for i := range twentyOne {
		twentyOne[i] = "skill-" + string(rune('a'+i))
	}
	cases := []struct {
		name  string
		over  draftFields
		field string
		text  string
	}{
		{"a custom role with no based-on", draftFields{"role": "cartographer"}, "based-on", ""},
		{"a name that is no member name", draftFields{"name": "../Evil Name"}, "name", ""},
		{"a name a fleet member has", draftFields{"name": "bosun"}, "name", ""},
		{"a name a built-in member has", draftFields{"name": "scout"}, "name", ""},
		{"a skill the resolver would drop", draftFields{"skills": []any{"not a skill"}}, "skills", ""},
		{"more than 20 skills", draftFields{"skills": twentyOne}, "skills", ""},
		{"a zero-width space in pick-me-when", draftFields{"pick-me-when": "Pick for​ stores."}, "pick-me-when", "U+200B"},
		{"a tag character in instructions", draftFields{"instructions": "Check the stores.\U000E0041"}, "instructions", "U+E0041"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newJudgeRig(t, true, "")
			r.fleetMember("bosun", "triager", "Fleet bosun.", "")
			before := listDir(t, r.crewDir())
			r.answers(with(validDraft(), tc.over))
			status, got := r.draft("Anything.")
			if status != 200 {
				t.Fatalf("status = %d, want 200 with the problem listed", status)
			}
			var found *problem
			for i, p := range got.Problems {
				if p.Field == tc.field {
					found = &got.Problems[i]
				}
			}
			if found == nil || found.Message == "" {
				t.Fatalf("problems = %v, want one on %q with a message", got.Problems, tc.field)
			}
			if !strings.Contains(found.Message, tc.text) {
				t.Errorf("message = %q, want it to name %q", found.Message, tc.text)
			}
			if got.Draft["name"] != tc.over["name"] && tc.over["name"] != nil {
				t.Errorf("draft name = %v, want the reply's name kept as sent for the editor", got.Draft["name"])
			}
			if after := listDir(t, r.crewDir()); strings.Join(after, ",") != strings.Join(before, ",") {
				t.Errorf("crew dir = %v, was %v: a draft wrote to it", after, before)
			}
		})
	}
}

func listDir(t *testing.T, dir string) []string {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func TestDraftMakesOneStrongCallWithTheBriefsCapTimeoutSchemaAndPrompt(t *testing.T) {
	commodoreAt(t)
	r := newJudgeRig(t, true, "")
	description := "Keep the ship's stores.\nNever order rum."
	if status, _ := r.draft(description); status != 200 {
		t.Fatalf("status = %d", status)
	}
	calls := r.called()
	if len(calls) != 1 {
		t.Fatalf("%d judge calls, want 1", len(calls))
	}
	c := calls[0]
	if c.Kind != judge.Strong || c.BudgetUSD != 0.30 || c.Timeout != 2*time.Minute {
		t.Errorf("call = kind %v, budget %v, timeout %v, want Strong, 0.30 and 2m", c.Kind, c.BudgetUSD, c.Timeout)
	}
	var schema struct {
		Type                 string                    `json:"type"`
		Properties           map[string]map[string]any `json:"properties"`
		Required             []string                  `json:"required"`
		AdditionalProperties *bool                     `json:"additionalProperties"`
	}
	if err := json.Unmarshal(c.Schema, &schema); err != nil {
		t.Fatalf("schema is not JSON: %v", err)
	}
	keys := []string{"name", "role", "based-on", "skills", "model", "pick-me-when", "instructions"}
	if schema.Type != "object" || schema.AdditionalProperties == nil || *schema.AdditionalProperties || len(schema.Properties) != len(keys) || len(schema.Required) != len(keys) {
		t.Errorf("schema = %s, want a closed object of the %d keys, all required", c.Schema, len(keys))
	}
	bounds := map[string]float64{"name": 64, "role": 64, "based-on": 64, "model": 100, "pick-me-when": 300, "instructions": 12000}
	for _, k := range keys {
		p, ok := schema.Properties[k]
		if !ok || !contains(schema.Required, k) {
			t.Errorf("schema lacks %q or does not require it", k)
			continue
		}
		if k == "skills" {
			items, _ := p["items"].(map[string]any)
			if p["type"] != "array" || items["type"] != "string" || items["maxLength"] != float64(100) {
				t.Errorf("skills = %v, want an array of strings of at most 100", p)
			}
			continue
		}
		if p["type"] != "string" || p["maxLength"] != bounds[k] {
			t.Errorf("%s = %v, want a string of at most %v", k, p, bounds[k])
		}
	}
	if _, ok := schema.Properties["harness"]; ok {
		t.Error("schema has a harness key: a draft never locks a harness")
	}
	lines := strings.SplitN(c.Prompt, "\n", 2)
	if lines[0] != crewWriterFirstLine {
		t.Errorf("prompt's first line = %q, want %q", lines[0], crewWriterFirstLine)
	}
	if strings.Contains(c.Prompt, "stakes") || strings.Contains(c.Prompt, "You are the advisor") {
		t.Error("prompt holds a phrase the fake harness reads as the advise call")
	}
	fenced := regexp.MustCompile("(?s)\n`{3,}[a-z]*\n" + regexp.QuoteMeta(description) + "\n`{3,}\\s*$")
	if !fenced.MatchString(c.Prompt) {
		t.Errorf("prompt does not end with the description in a fenced block:\n%s", tail(c.Prompt, 300))
	}
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func TestCrewWriterPromptNamesExactlyTheResolversRolesOnOneLine(t *testing.T) {
	commodoreAt(t)
	r := newJudgeRig(t, true, "")
	r.draft("Anything.")
	var roles string
	for _, line := range strings.Split(r.called()[0].Prompt, "\n") {
		if rest, ok := strings.CutPrefix(line, "Roles: "); ok {
			roles = rest
		}
	}
	if want := strings.Join(crew.Roles(), ", "); roles != want {
		t.Errorf("the prompt's \"Roles: \" line = %q, want %q", roles, want)
	}
}

func TestDraftPassesTheRequestsContextToTheCallSoStopCancelsIt(t *testing.T) {
	commodoreAt(t)
	r := newJudgeRig(t, true, "")
	started := make(chan struct{})
	cancelled := make(chan struct{})
	r.reply = func(ctx context.Context, _ judge.Call) (judge.Reply, error) {
		close(started)
		select {
		case <-ctx.Done():
			close(cancelled)
			return judge.Reply{}, &judge.Failure{Kind: judge.KindError, Detail: "the call was cancelled"}
		case <-time.After(10 * time.Second):
			return judge.Reply{Fields: validDraft()}, nil
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "POST", r.srv.URL+"/api/fleets/work/crew/draft", strings.NewReader(`{"description":"slow"}`))
	done := make(chan struct{})
	go func() {
		defer close(done)
		if resp, err := http.DefaultClient.Do(req); err == nil {
			resp.Body.Close()
		}
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("the draft route never called the judge")
	}
	cancel()
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("the judge call's context was not cancelled when the client went away")
	}
	<-done
}

func TestDraftFailureIs502NamingTheKind(t *testing.T) {
	commodoreAt(t)
	cases := []struct {
		err  error
		kind string
	}{
		{&judge.Failure{Kind: judge.KindTimeout, Detail: "no answer in 2m"}, "timeout"},
		{&judge.Failure{Kind: judge.KindBudgetSpent, Detail: "over the cap"}, "budget spent"},
		{&judge.Failure{Kind: judge.KindBadReply, Detail: "not JSON"}, "bad reply"},
		{&judge.Failure{Kind: judge.KindError, Detail: "boom"}, "error"},
		{errors.New("the seam broke"), "error"},
	}
	for _, tc := range cases {
		t.Run(tc.kind+" "+tc.err.Error(), func(t *testing.T) {
			r := newJudgeRig(t, true, "")
			r.reply = func(context.Context, judge.Call) (judge.Reply, error) { return judge.Reply{}, tc.err }
			status, got := r.draft("Anything.")
			if status != 502 || got.Kind != tc.kind || got.Error == "" {
				t.Errorf("%d %+v, want 502 with kind %q and an error", status, got, tc.kind)
			}
			r.wroteNothing()
		})
	}
}

func TestDraftWithNoProvenJudgeHarnessIs409WithTheReasonAndMakesNoCall(t *testing.T) {
	commodoreAt(t)
	r := newJudgeRig(t, false, "")
	status, got := r.draft("Anything.")
	if status != 409 || got.Error == "" || !strings.Contains(got.Reason, "claude") {
		t.Errorf("%d %+v, want 409 with an error and a reason naming claude", status, got)
	}
	if n := len(r.called()); n != 0 {
		t.Errorf("%d judge calls, want none", n)
	}
	r.wroteNothing()
}

func TestDraftRefusesAnEmptyBlankOrOverlongDescriptionBeforeAnyCall(t *testing.T) {
	commodoreAt(t)
	r := newJudgeRig(t, true, "")
	for name, description := range map[string]string{"empty": "", "blank": " \n\t ", "4001 characters": strings.Repeat("é", 4001)} {
		status, body := r.rawDraft(description)
		if status != 400 || !strings.Contains(body, `"error"`) {
			t.Errorf("%s: %d %s, want 400 with an error", name, status, body)
		}
	}
	if status, body := r.do("POST", "/api/fleets/work/crew/draft", map[string]any{}); status != 400 {
		t.Errorf("no description key: %d %s, want 400", status, body)
	}
	if n := len(r.called()); n != 0 {
		t.Errorf("%d judge calls, want none", n)
	}
	if status, _ := r.rawDraft(strings.Repeat("é", 4000)); status != 200 {
		t.Errorf("4000 two-byte characters: %d, want 200: the limit counts characters, not bytes", status)
	}
}

func TestDraftOfAnUnknownFleetIs404(t *testing.T) {
	commodoreAt(t)
	r := newJudgeRig(t, true, "")
	if status, body := r.do("POST", "/api/fleets/ghost/crew/draft", map[string]any{"description": "x"}); status != 404 || !strings.Contains(body, "fleet ghost not found") {
		t.Errorf("%d %s, want 404 naming the fleet", status, body)
	}
	if n := len(r.called()); n != 0 {
		t.Errorf("%d judge calls, want none", n)
	}
}
