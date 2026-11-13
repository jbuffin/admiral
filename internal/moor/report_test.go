package moor

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestParseReportReadsVersionsOneAndTwo(t *testing.T) {
	const pr = "https://github.com/acme/widgets/pull/12"
	for name, c := range map[string]struct {
		raw        string
		wantPRs    []string
		wantResume string
	}{
		"v1 lists PR URL strings": {
			`{"version":1,"result":"done","why":"x","prs":["` + pr + `"],"resume":"/commodore resume run-1"}`,
			[]string{pr}, "/commodore resume run-1"},
		"v2 lists PR objects": {
			`{"version":2,"result":"done","why":"x","prs":[{"url":"` + pr + `","closes":["acme/widgets#3"]}],"resume":"/commodore resume run-2"}`,
			[]string{pr}, "/commodore resume run-2"},
		"another version": {
			`{"version":3,"result":"done","why":"x","prs":[{"url":"` + pr + `","closes":[]}],"resume":"/commodore resume run-3"}`,
			nil, ""},
		"no version":   {`{"result":"done","why":"x","prs":["` + pr + `"]}`, nil, ""},
		"invalid JSON": {`{"version":1,"prs":["` + pr + `"`, nil, ""},
		"a prs entry that is not a PR URL voids the list": {
			`{"version":1,"result":"done","why":"x","prs":["` + pr + `","https://github.com/acme/widgets/issues/12"],"resume":"/commodore resume run-1"}`,
			nil, ""},
		"a v2 entry with no url voids the list": {
			`{"version":2,"result":"done","why":"x","prs":[{"url":"` + pr + `","closes":[]},{"closes":[]}]}`,
			nil, ""},
		"no prs":         {`{"version":1,"result":"done","why":"x"}`, nil, ""},
		"missing report": {``, nil, ""},
	} {
		t.Run(name, func(t *testing.T) {
			got := ParseReport([]byte(c.raw))
			if !reflect.DeepEqual(got.PRs, c.wantPRs) && !(len(got.PRs) == 0 && len(c.wantPRs) == 0) {
				t.Errorf("PRs = %v, want %v", got.PRs, c.wantPRs)
			}
			if got.Resume != c.wantResume {
				t.Errorf("Resume = %q, want %q", got.Resume, c.wantResume)
			}
		})
	}
}

// The reader caps a report at 1 MiB; a parser handed more still reads no PRs.
func TestParseReportIgnoresAnOversizedReport(t *testing.T) {
	raw := `{"version":1,"result":"done","why":"` + strings.Repeat("x", 1<<20) + `","prs":["https://github.com/acme/widgets/pull/1"]}`
	if got := ParseReport([]byte(raw)); len(got.PRs) != 0 {
		t.Errorf("an oversized report gave PRs %v, want none", got.PRs)
	}
}

func TestOnlyASkillsResumeInvocationIsKeptAsResumeText(t *testing.T) {
	for resume, want := range map[string]bool{
		"/commodore resume run-7":          true,
		"$commodore resume run-7":          true,
		"/skills:commodore resume r1.2_x":  true,
		"/commodore resume run-7 and push": false,
		"/commodore resume run-7\nmore":    false,
		"/commodore resume run-7\n":        false,
		"/commodore resume ../x":           false,
		"/commodore resume a..b":           false,
		"/commodore resume -x":             false,
		"/commodore resume":                false,
		"commodore resume run-7":           false,
		"/commodore  resume run-7":         false,
		"/commodore resume run-7\x00":      false,
		"":                                 false,
	} {
		if got := ValidResume(resume); got != want {
			t.Errorf("ValidResume(%q) = %v, want %v", resume, got, want)
		}
		raw := `{"version":2,"prs":[],"resume":` + jsonString(resume) + `}`
		if got := ParseReport([]byte(raw)).Resume; (got != "") != want {
			t.Errorf("ParseReport kept resume %q as %q, want kept = %v", resume, got, want)
		}
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestParseReportReadsTheResultOfAVersionTwoReport(t *testing.T) {
	const pr = `{"url":"https://github.com/acme/widgets/pull/12","closes":[]}`
	for name, c := range map[string]struct{ raw, want string }{
		"done":               {`{"version":2,"result":"done","why":"x","prs":[` + pr + `]}`, "done"},
		"partial":            {`{"version":2,"result":"partial","why":"x","prs":[` + pr + `]}`, "partial"},
		"failed":             {`{"version":2,"result":"failed","why":"x","prs":[` + pr + `]}`, "failed"},
		"missing":            {`{"version":2,"why":"x","prs":[` + pr + `]}`, ""},
		"unknown":            {`{"version":2,"result":"finished","why":"x","prs":[` + pr + `]}`, ""},
		"not a string":       {`{"version":2,"result":true,"why":"x","prs":[` + pr + `]}`, ""},
		"version 1 has none": {`{"version":1,"result":"done","why":"x","prs":["https://github.com/acme/widgets/pull/12"]}`, ""},
		"a report that fails the PR check gives nothing": {`{"version":2,"result":"done","why":"x","prs":[{"url":"https://example.com/nope"}]}`, ""},
	} {
		t.Run(name, func(t *testing.T) {
			if got := ParseReport([]byte(c.raw)).Result; got != c.want {
				t.Errorf("Result = %q, want %q", got, c.want)
			}
		})
	}
}
