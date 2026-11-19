package routines_test

import (
	"encoding/json"
	"strings"
	"testing"
)

const digestProse = "Every morning at 8, post a digest of this fleet, leading with PRs."

func digestDraftFields() map[string]any {
	return map[string]any{"name": "Morning digest", "enabled": true, "when": "0 8 * * *", "before_acting": "act", "notify": false,
		"do": map[string]any{"digest": map[string]any{"extra": "lead with PRs"}}}
}

func draftOf(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	d, _ := body["draft"].(map[string]any)
	if d == nil {
		t.Fatalf("response %v has no draft", body)
	}
	return d
}

func problemFields(body map[string]any) []string {
	var out []string
	ps, _ := body["problems"].([]any)
	for _, p := range ps {
		out = append(out, p.(map[string]any)["field"].(string))
	}
	return out
}

func TestRoutineDraftSchemaLetsTheJudgeAnswerADigestWithoutAPrompt(t *testing.T) {
	r := newSO(t)
	r.answers(routineDraftFields())
	r.routineDraft(routineProse)
	calls := r.called()
	if len(calls) != 1 {
		t.Fatalf("judge calls = %d, want 1", len(calls))
	}
	schema := string(calls[0].Schema)
	if !strings.Contains(schema, `"digest"`) || !strings.Contains(schema, `"extra"`) {
		t.Errorf("schema %s does not offer do.digest with extra words", schema)
	}
	var s struct {
		Properties struct {
			Do struct {
				Required []string `json:"required"`
			} `json:"do"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(calls[0].Schema, &s); err != nil {
		t.Fatalf("schema is not JSON: %v", err)
	}
	for _, k := range s.Properties.Do.Required {
		if k == "prompt" {
			t.Errorf("schema requires do.prompt, so a digest answer would fail it: %s", schema)
		}
	}
}

func TestRoutineWriterPromptTellsTheJudgeAboutTheDigestAction(t *testing.T) {
	r := newSO(t)
	r.answers(routineDraftFields())
	// the prose must not say "digest", so the word can only come from the writer's own instructions
	r.routineDraft("Every morning at 8, sum up what changed in this fleet.")
	calls := r.called()
	if len(calls) != 1 {
		t.Fatalf("judge calls = %d, want 1", len(calls))
	}
	p := strings.ToLower(calls[0].Prompt)
	for _, want := range []string{"digest", "extra"} {
		if !strings.Contains(p, want) {
			t.Errorf("the writer prompt never says %q, so the judge can't draft a fleet digest:\n%s", want, calls[0].Prompt)
		}
	}
}

func TestRoutineDraftAnswersADigestRoutineWithItsExtraWordsAndNoProblems(t *testing.T) {
	r := newSO(t)
	r.answers(digestDraftFields())
	code, body := r.routineDraft(digestProse)
	if code != 200 {
		t.Fatalf("draft = %d %v, want 200", code, body)
	}
	d := draftOf(t, body)
	do, _ := d["do"].(map[string]any)
	digest, _ := do["digest"].(map[string]any)
	if digest["extra"] != "lead with PRs" || do["prompt"] != nil || do["launch"] != nil {
		t.Errorf("draft.do = %v, want a digest with its extra words and nothing else", d["do"])
	}
	if p, ok := body["problems"].([]any); !ok || len(p) != 0 {
		t.Errorf("problems = %v, want an empty list", body["problems"])
	}
	if code, list := r.do("GET", "/api/fleets/work/routines", nil); code != 200 || len(list["routines"].([]any)) != 0 {
		t.Errorf("after a draft the fleet's routines are %v: nothing may be saved", list)
	}
}

func TestRoutineDraftKeepsADigestWithNoExtraWordsAsAnEmptyDigest(t *testing.T) {
	r := newSO(t)
	fields := digestDraftFields()
	fields["do"] = map[string]any{"digest": map[string]any{}}
	r.answers(fields)
	id := r.madeDraft(digestProse)
	code, got := r.keptDraft("work", id)
	if code != 200 {
		t.Fatalf("GET kept draft = %d %v, want 200", code, got)
	}
	do, _ := draftOf(t, got)["do"].(map[string]any)
	if d, ok := do["digest"].(map[string]any); !ok || len(d) != 0 {
		t.Errorf("kept draft.do = %v, want an empty digest, which is still a digest", do)
	}
}

func TestRoutineDraftNamesADigestOverTheCapAsAProblemOnDo(t *testing.T) {
	cases := map[string]func(map[string]any){
		"extra words over the cap": func(f map[string]any) {
			f["do"] = map[string]any{"digest": map[string]any{"extra": strings.Repeat("x", 1001)}}
		},
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			r := newSO(t)
			fields := digestDraftFields()
			edit(fields)
			r.answers(fields)
			code, body := r.routineDraft(digestProse)
			if got := problemFields(body); code != 200 || len(got) != 1 || got[0] != "do" {
				t.Errorf("draft = %d %v, want 200 with one problem on do", code, body)
			}
		})
	}
}

func TestRoutineDraftNamesADigestOnABadScheduleAsAProblemOnWhen(t *testing.T) {
	r := newSO(t)
	fields := digestDraftFields()
	fields["when"] = "every morning"
	r.answers(fields)
	code, body := r.routineDraft(digestProse)
	if got := problemFields(body); code != 200 || len(got) == 0 || got[0] != "when" {
		t.Errorf("draft = %d %v, want 200 with a problem on when", code, body)
	}
}

func TestRoutineWriterPromptSaysADigestDefaultsToAct(t *testing.T) {
	r := newSO(t)
	r.answers(routineDraftFields())
	r.routineDraft("Every morning at 8, sum up what changed in this fleet.")
	calls := r.called()
	if len(calls) != 1 {
		t.Fatalf("judge calls = %d, want 1", len(calls))
	}
	if p := calls[0].Prompt; !strings.Contains(p, "Use `act`") || !strings.Contains(p, "digest") {
		t.Errorf("the writer prompt never says a digest defaults to act:\n%s", p)
	}
}

func TestRoutineDraftTakesADrafterAnswerWithBothAPromptAndADigestAsMalformed(t *testing.T) {
	r := newSO(t)
	fields := digestDraftFields()
	fields["do"] = map[string]any{"prompt": "sweep", "digest": map[string]any{}}
	r.answers(fields)
	code, body := r.routineDraft(digestProse)
	if code != 502 {
		t.Errorf("draft = %d %v, want 502, the malformed-answer refusal", code, body)
	}
}
