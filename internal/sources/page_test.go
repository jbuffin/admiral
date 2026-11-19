package sources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func jsonOf(t *testing.T, v any) map[string]any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("%s is not an object: %v", b, err)
	}
	return m
}

func list(t *testing.T, v any, key string) []any {
	t.Helper()
	l, ok := v.(map[string]any)[key].([]any)
	if !ok {
		t.Fatalf("%s has no list %q", dump(v), key)
	}
	return l
}

func dump(v any) string { b, _ := json.Marshal(v); return string(b) }

func pageOf(groups string, waiting int) string {
	return fmt.Sprintf(`{"groups":%s,"waiting":%d}`, groups, waiting)
}

func rowsJSON(n int) string {
	rows := make([]string, n)
	for i := range rows {
		rows[i] = fmt.Sprintf(`{"id":"r%d","title":"Row %d"}`, i, i)
	}
	return "[" + strings.Join(rows, ",") + "]"
}

func groupsJSON(n int) string {
	gs := make([]string, n)
	for i := range gs {
		gs[i] = fmt.Sprintf(`{"id":"g%d","title":"Group %d","rows":[]}`, i, i)
	}
	return "[" + strings.Join(gs, ",") + "]"
}

func repeat(item string, n int) string {
	items := make([]string, n)
	for i := range items {
		items[i] = item
	}
	return "[" + strings.Join(items, ",") + "]"
}

func oneRow(row string) string {
	return pageOf(`[{"id":"g","title":"G","rows":[`+row+`]}]`, 0)
}

func firstRow(t *testing.T, raw string) map[string]any {
	t.Helper()
	p, err := parsePage(json.RawMessage(raw))
	if err != nil {
		t.Fatalf("parsePage: %v", err)
	}
	rows := list(t, list(t, jsonOf(t, p), "groups")[0], "rows")
	return rows[0].(map[string]any)
}

func TestAGoodPageKeepsItsGroupsRowsChipsAndWaitingCount(t *testing.T) {
	raw := pageOf(`[{"id":"review","title":"Needs review","rows":[
		{"id":"T-1","title":"Fix login","subtitle":"Acme","chips":[{"label":"P1","tone":"warn"}],
		 "reason":"Review is open","action":{"id":"pass","label":"Pass review","confirm":"form","summary":"Pass it"}}]},
		{"id":"later","title":"Later","rows":[]}]`, 7)
	p, err := parsePage(json.RawMessage(raw))
	if err != nil {
		t.Fatalf("parsePage: %v", err)
	}
	got := jsonOf(t, p)
	if got["waiting"] != float64(7) {
		t.Errorf("waiting = %v, want 7", got["waiting"])
	}
	groups := list(t, got, "groups")
	if len(groups) != 2 || groups[0].(map[string]any)["id"] != "review" || groups[1].(map[string]any)["id"] != "later" {
		t.Fatalf("groups = %s, want review then later in order", dump(groups))
	}
	if rows := list(t, groups[1], "rows"); len(rows) != 0 {
		t.Errorf("an empty group = %s, want a list with no rows", dump(rows))
	}
	row := list(t, groups[0], "rows")[0].(map[string]any)
	for k, want := range map[string]string{"id": "T-1", "title": "Fix login", "subtitle": "Acme", "reason": "Review is open"} {
		if row[k] != want {
			t.Errorf("row %s = %v, want %q", k, row[k], want)
		}
	}
	chip := list(t, row, "chips")[0].(map[string]any)
	if chip["label"] != "P1" || chip["tone"] != "warn" {
		t.Errorf("chip = %s, want label P1 tone warn", dump(chip))
	}
	act := row["action"].(map[string]any)
	if act["id"] != "pass" || act["label"] != "Pass review" || act["confirm"] != "form" || act["summary"] != "Pass it" {
		t.Errorf("action = %s, want the declared main action as data", dump(act))
	}
}

func TestAPageAtEveryLimitIsKept(t *testing.T) {
	chips := repeat(`{"label":"c","tone":"good"}`, maxChips)
	acts := repeat(`{"id":"a","label":"A","confirm":"click"}`, maxActions)
	for name, raw := range map[string]string{
		"5000 rows":                  pageOf(`[{"id":"g","title":"G","rows":`+rowsJSON(maxPageRows)+`}]`, 0),
		"100 groups":                 pageOf(groupsJSON(maxPageGroups), 0),
		"10 chips":                   oneRow(`{"id":"r","title":"R","chips":` + chips + `}`),
		"10 actions on a row's list": oneRow(`{"id":"r","title":"R","actions":` + acts + `}`),
		"a main action and 9 more":   oneRow(`{"id":"r","title":"R","action":{"id":"m","label":"M"},"actions":` + repeat(`{"id":"a","label":"A"}`, maxActions-1) + `}`),
		"an id at the id cap":        oneRow(`{"id":"` + strings.Repeat("i", maxIDRunes) + `","title":"R"}`),
	} {
		if _, err := parsePage(json.RawMessage(raw)); err != nil {
			t.Errorf("%s: parsePage = %v, want the page kept", name, err)
		}
	}
}

func TestAPageOverAnyNumericLimitOrMalformedIsDroppedWhole(t *testing.T) {
	chips := repeat(`{"label":"c"}`, maxChips+1)
	acts := repeat(`{"id":"a","label":"A","confirm":"click"}`, maxActions+1)
	for name, raw := range map[string]string{
		"5001 rows":             pageOf(`[{"id":"g","title":"G","rows":`+rowsJSON(maxPageRows+1)+`}]`, 0),
		"5001 rows over groups": pageOf(`[{"id":"a","title":"A","rows":`+rowsJSON(3000)+`},{"id":"b","title":"B","rows":`+rowsJSON(2001)+`}]`, 0),
		"101 groups":            pageOf(groupsJSON(maxPageGroups+1), 0),
		"11 chips":              oneRow(`{"id":"r","title":"R","chips":` + chips + `}`),
		"11 actions":            oneRow(`{"id":"r","title":"R","actions":` + acts + `}`),
		"a main action and 10":  oneRow(`{"id":"r","title":"R","action":{"id":"m","label":"M"},"actions":` + repeat(`{"id":"a","label":"A"}`, maxActions) + `}`),
		"an id over the cap":    oneRow(`{"id":"` + strings.Repeat("i", maxIDRunes+1) + `","title":"R"}`),
		"a control id":          oneRow(`{"id":"a\nb","title":"R"}`),
		"a repeated group id":   pageOf(`[{"id":"g","title":"A","rows":[]},{"id":"g","title":"B","rows":[]}]`, 0),
		"a repeated row id":     pageOf(`[{"id":"a","title":"A","rows":[{"id":"r"}]},{"id":"b","title":"B","rows":[{"id":"r"}]}]`, 0),
		"a row without an id":   oneRow(`{"title":"R"}`),
		"a group without an id": pageOf(`[{"title":"G","rows":[]}]`, 0),
		"groups not a list":     `{"groups":{},"waiting":0}`,
		"not an object":         `[]`,
	} {
		if p, err := parsePage(json.RawMessage(raw)); err == nil {
			t.Errorf("%s: parsePage = %s, want an error that drops the whole answer", name, dump(p))
		}
	}
}

func TestATitleLosesInvisibleAndBidiCharacters(t *testing.T) {
	row := firstRow(t, oneRow(`{"id":"r","title":"pay\u202eevil\u200b\u0085ok"}`))
	if row["title"] != "payevilok" {
		t.Errorf("title = %q, want the invisible and bidi characters stripped", row["title"])
	}
}

func TestAChipToneOutsideTheFixedSetReadsAsNeutral(t *testing.T) {
	row := firstRow(t, oneRow(`{"id":"r","title":"R","chips":[
		{"label":"a","tone":"neutral"},{"label":"b","tone":"good"},{"label":"c","tone":"warn"},{"label":"d","tone":"muted"},
		{"label":"e","tone":"red"},{"label":"f","tone":"#ff0000"},{"label":"g"},{"label":"h","tone":7}]}`))
	var tones []string
	for _, c := range list(t, row, "chips") {
		tones = append(tones, c.(map[string]any)["tone"].(string))
	}
	want := []string{"neutral", "good", "warn", "muted", "neutral", "neutral", "neutral", "neutral"}
	if strings.Join(tones, ",") != strings.Join(want, ",") {
		t.Errorf("tones = %v, want %v", tones, want)
	}
}

func TestARowTitleIsCutToOneLineAndTheTitleCap(t *testing.T) {
	row := firstRow(t, oneRow(`{"id":"r","title":"First line\nSecond line","subtitle":"`+strings.Repeat("x", maxTitleRunes+50)+`"}`))
	if row["title"] != "First line" {
		t.Errorf("title = %q, want the first line only", row["title"])
	}
	if n := len([]rune(row["subtitle"].(string))); n != maxTitleRunes {
		t.Errorf("a %d-rune subtitle came out %d runes, want it cut to %d", maxTitleRunes+50, n, maxTitleRunes)
	}
	long := firstRow(t, oneRow(`{"id":"r","title":"`+strings.Repeat("é", maxTitleRunes+10)+`"}`))
	if n := len([]rune(long["title"].(string))); n != maxTitleRunes {
		t.Errorf("a long title came out %d runes, want %d", n, maxTitleRunes)
	}
}

func TestAnActionWhoseURLIsNotHTTPIsDroppedAndTheRestOfThePageStays(t *testing.T) {
	for _, bad := range []string{"javascript:alert(1)", "file:///etc/passwd", "ftp://example.com/x", "//example.com/x", "https://exa mple.com", "data:text/html,hi", "not a url"} {
		row := firstRow(t, oneRow(`{"id":"r","title":"Kept","action":{"id":"open","label":"Open","confirm":"click","url":`+quote(bad)+`}}`))
		if row["title"] != "Kept" {
			t.Errorf("url %q: the row = %s, want it kept", bad, dump(row))
		}
		if _, has := row["action"]; has && row["action"] != nil {
			t.Errorf("url %q: action = %s, want it dropped", bad, dump(row["action"]))
		}
	}
	row := firstRow(t, oneRow(`{"id":"r","title":"R","action":{"id":"open","label":"Open","confirm":"click","url":"https://example.com/x"}}`))
	if a, _ := row["action"].(map[string]any); a == nil || a["url"] != "https://example.com/x" {
		t.Errorf("action = %s, want an https url kept", dump(row["action"]))
	}
	listed := firstRow(t, oneRow(`{"id":"r","title":"R","actions":[{"id":"a","label":"A","confirm":"click","url":"javascript:x"},{"id":"b","label":"B","confirm":"click","url":"http://example.com"},{"id":"c","label":"C","confirm":"yesno"}]}`))
	var ids []string
	for _, a := range list(t, listed, "actions") {
		ids = append(ids, a.(map[string]any)["id"].(string))
	}
	if strings.Join(ids, ",") != "b,c" {
		t.Errorf("actions kept = %v, want b and c (a url-less action stays, the javascript one goes)", ids)
	}
}

func TestARowKeepsAnHTTPLinkAndDropsAnyOtherWithTheRowKept(t *testing.T) {
	row := firstRow(t, oneRow(`{"id":"r","title":"R","url":"https://example.com/item/1"}`))
	if row["url"] != "https://example.com/item/1" {
		t.Errorf("url = %v, want the https link kept", row["url"])
	}
	for _, bad := range []string{"javascript:alert(1)", "file:///etc/passwd", "https://exa mple.com"} {
		row := firstRow(t, oneRow(`{"id":"r","title":"Kept","url":`+quote(bad)+`}`))
		if row["title"] != "Kept" || row["url"] != nil {
			t.Errorf("url %q: row = %s, want the row kept without its link", bad, dump(row))
		}
	}
}

func TestARowTargetKeepsItsLabelChipsAndStepAndDropsWhatIsUnusable(t *testing.T) {
	row := firstRow(t, oneRow(`{"id":"r","title":"R","target":{"label":"Code review\nmore","chips":[{"label":"Paused","tone":"warn"}],"step":"s1"}}`))
	tg, _ := row["target"].(map[string]any)
	if tg == nil || tg["label"] != "Code review" || tg["step"] != "s1" || list(t, tg, "chips")[0].(map[string]any)["label"] != "Paused" {
		t.Fatalf("target = %s, want its one-line label, chip and step", dump(row["target"]))
	}
	noStep := firstRow(t, oneRow(`{"id":"r","title":"R","target":{"label":"Review","step":"a\nb"}}`))
	if tg, _ := noStep["target"].(map[string]any); tg == nil || tg["label"] != "Review" || tg["step"] != nil {
		t.Errorf("a target with a bad step id = %s, want the label kept and the step dropped", dump(noStep["target"]))
	}
	for _, bad := range []string{`{"chips":[]}`, `{"label":""}`, `"Review"`, `[1]`} {
		if row := firstRow(t, oneRow(`{"id":"r","title":"Kept","target":`+bad+`}`)); row["title"] != "Kept" || row["target"] != nil {
			t.Errorf("target %s: row = %s, want the row kept without a target", bad, dump(row))
		}
	}
	if p, err := parsePage(json.RawMessage(oneRow(`{"id":"r","title":"R","target":{"label":"L","chips":` + repeat(`{"label":"c"}`, maxChips+1) + `}}`))); err == nil {
		t.Errorf("a target over the chip limit: parsePage = %s, want the page refused like a row over it", dump(p))
	}
}

func TestAModuleActionKeepsTheStepItNamesAndDropsABadStep(t *testing.T) {
	row := firstRow(t, oneRow(`{"id":"r","title":"R",
		"action":{"id":"a","label":"A","confirm":"click","step":"s1"},
		"actions":[{"id":"b","label":"B","confirm":"click","step":"a\nb"},{"id":"c","label":"C","confirm":"click","step":"gone"},{"id":"d","label":"D","confirm":"click"}]}`))
	if got := row["action"].(map[string]any)["step"]; got != "s1" {
		t.Errorf("row action step = %v, want s1 kept", got)
	}
	acts := list(t, row, "actions")
	if len(acts) != 3 {
		t.Fatalf("actions = %s, want all three kept, a bad step dropping only the step", dump(acts))
	}
	for i, want := range []any{nil, "gone", nil} {
		if got := acts[i].(map[string]any)["step"]; got != want {
			t.Errorf("action %d step = %v, want %v (a step the detail lacks is no error; a bad id is dropped)", i, got, want)
		}
	}
	d, err := parseRowDetail(json.RawMessage(`{"sections":[{"kind":"steps","title":"S","steps":[{"id":"s1","title":"T","actions":[{"id":"x","label":"X","confirm":"click","step":"s2"},{"id":"y","label":"Y","confirm":"click","step":""}]}]}]}`))
	if err != nil {
		t.Fatalf("parseRowDetail: %v", err)
	}
	sacts := list(t, list(t, jsonOf(t, d), "sections")[0], "steps")[0].(map[string]any)["actions"].([]any)
	if sacts[0].(map[string]any)["step"] != "s2" || sacts[1].(map[string]any)["step"] != nil {
		t.Errorf("step actions = %s, want step s2 kept on x and the empty step dropped on y", dump(sacts))
	}
}

func TestABuiltinActionAndANoticeActionDropTheirStep(t *testing.T) {
	row := firstRow(t, oneRow(`{"id":"r","title":"R","actions":[{"id":"o","label":"Open","builtin":"open","url":"https://example.com/x","step":"s1"}]}`))
	if got := list(t, row, "actions")[0].(map[string]any); got["step"] != nil || got["builtin"] != "open" {
		t.Errorf("built-in action = %s, want it kept without a step", dump(got))
	}
	p, err := parsePage(json.RawMessage(`{"state":"ready","groups":[],"notices":[{"id":"n","text":"T","action":{"id":"a","label":"A","confirm":"click","step":"s1"}}]}`))
	if err != nil {
		t.Fatalf("parsePage: %v", err)
	}
	n := list(t, jsonOf(t, p), "notices")[0].(map[string]any)
	if a, _ := n["action"].(map[string]any); a == nil || a["id"] != "a" || a["step"] != nil {
		t.Errorf("notice = %s, want its module action kept without a step", dump(n))
	}
}

func TestALinkInALinksSectionKeepsItsSubtitleAndChipsAndANoticeLinkDropsThem(t *testing.T) {
	d, err := parseRowDetail(json.RawMessage(`{"sections":[{"kind":"links","title":"L","links":[
		{"label":"PR 7","url":"https://example.com/pr/7","subtitle":"Add retries\nsecond line","chips":[{"label":"Merged","tone":"good"}]},
		{"label":"Plain","url":"https://example.com/p"}]}]}`))
	if err != nil {
		t.Fatalf("parseRowDetail: %v", err)
	}
	links := list(t, list(t, jsonOf(t, d), "sections")[0], "links")
	pr := links[0].(map[string]any)
	if pr["subtitle"] != "Add retries" || len(list(t, pr, "chips")) != 1 || list(t, pr, "chips")[0].(map[string]any)["label"] != "Merged" {
		t.Errorf("link = %s, want its one-line subtitle and its chip", dump(pr))
	}
	if plain := links[1].(map[string]any); plain["subtitle"] != nil && plain["subtitle"] != "" {
		t.Errorf("plain link = %s, want no subtitle", dump(plain))
	}
	p, err := parsePage(json.RawMessage(`{"state":"ready","groups":[],"notices":[{"id":"n","text":"T","link":{"label":"L","url":"https://example.com/l","subtitle":"S","chips":` + repeat(`{"label":"c"}`, maxChips+1) + `}}]}`))
	if err != nil {
		t.Fatalf("a notice link with 11 chips: parsePage = %v, want the page kept (a notice link carries no chips)", err)
	}
	nl := list(t, jsonOf(t, p), "notices")[0].(map[string]any)["link"].(map[string]any)
	if nl["label"] != "L" || nl["subtitle"] != nil || nl["chips"] != nil {
		t.Errorf("notice link = %s, want only its label and url", dump(nl))
	}
}

func TestALinkOverTheChipLimitRefusesTheDetailEvenWhenItsURLIsBad(t *testing.T) {
	links := func(link string) string {
		return `{"sections":[{"kind":"links","title":"L","links":[` + link + `]}]}`
	}
	chips := repeat(`{"label":"c"}`, maxChips+1)
	if _, err := parseRowDetail(json.RawMessage(links(`{"label":"A","url":"https://example.com/a","chips":` + repeat(`{"label":"c"}`, maxChips) + `}`))); err != nil {
		t.Errorf("a link at the chip limit: %v, want it kept", err)
	}
	for name, link := range map[string]string{
		"a good link":    `{"label":"A","url":"https://example.com/a","chips":` + chips + `}`,
		"a bad-url link": `{"label":"A","url":"javascript:alert(1)","chips":` + chips + `}`,
	} {
		d, err := parseRowDetail(json.RawMessage(links(link)))
		if err == nil {
			t.Errorf("%s with 11 chips: parseRowDetail = %s, want the detail refused", name, dump(d))
		} else if strings.Contains(err.Error(), "row or step") {
			t.Errorf("%s: error %q names a row or step, want it to name a link", name, err)
		}
	}
}

func quote(s string) string { b, _ := json.Marshal(s); return string(b) }

const detailJSON = `{"sections":[
 {"kind":"links","title":"Links","links":[{"label":"Ticket","url":"https://example.com/t/1"},{"label":"Bad","url":"javascript:alert(1)"},{"label":"Plain","url":"http://example.com/p"}]},
 {"kind":"steps","title":"Steps","steps":[{"id":"s1","title":"Code review","subtitle":"With a pair\nsecond line","chips":[{"label":"open","tone":"odd"}],"actions":[{"id":"pass","label":"Pass","confirm":"form"}]}]},
 {"kind":"fields","title":"Details","fields":[{"label":"Owner","value":"amy"}]}]}`

func TestARowDetailKeepsItsSectionsAndDropsOnlyTheLinksWithBadURLs(t *testing.T) {
	d, err := parseRowDetail(json.RawMessage(detailJSON))
	if err != nil {
		t.Fatalf("parseRowDetail: %v", err)
	}
	secs := list(t, jsonOf(t, d), "sections")
	if len(secs) != 3 {
		t.Fatalf("sections = %s, want links, steps and fields", dump(secs))
	}
	var labels []string
	for _, l := range list(t, secs[0], "links") {
		labels = append(labels, l.(map[string]any)["label"].(string))
	}
	if strings.Join(labels, ",") != "Ticket,Plain" {
		t.Errorf("links kept = %v, want Ticket and Plain, the javascript link dropped by itself", labels)
	}
	step := list(t, secs[1], "steps")[0].(map[string]any)
	if step["id"] != "s1" || step["title"] != "Code review" || step["subtitle"] != "With a pair" || len(list(t, step, "actions")) != 1 {
		t.Errorf("step = %s, want its id, title, one-line subtitle and action", dump(step))
	}
	if tone := list(t, step, "chips")[0].(map[string]any)["tone"]; tone != "neutral" {
		t.Errorf("a step chip's tone = %v, want an unknown tone read as neutral", tone)
	}
	f := list(t, secs[2], "fields")[0].(map[string]any)
	if f["label"] != "Owner" || f["value"] != "amy" {
		t.Errorf("field = %s, want Owner = amy", dump(f))
	}
}

func TestARowDetailRefusesARepeatedStepIDAndDropsAStepOrFieldWithBadText(t *testing.T) {
	step := func(id string) string { return `{"id":"` + id + `","title":"T"}` }
	twoSections := `{"sections":[{"kind":"steps","title":"A","steps":[` + step("s") + `]},{"kind":"steps","title":"B","steps":[` + step("s") + `]}]}`
	if d, err := parseRowDetail(json.RawMessage(twoSections)); err == nil {
		t.Errorf("a step id repeated across sections: parseRowDetail = %s, want it refused", dump(d))
	}
	d, err := parseRowDetail(json.RawMessage(`{"sections":[{"kind":"steps","title":"A","steps":[` + step(`a\nb`) + `,` + step("ok") + `]},
		{"kind":"fields","title":"F","fields":[{"label":"bad","value":"x\u001b[31m"},{"label":"multi","value":"a\nb"}]}]}`))
	if err != nil {
		t.Fatalf("parseRowDetail: %v", err)
	}
	secs := list(t, jsonOf(t, d), "sections")
	if steps := list(t, secs[0], "steps"); len(steps) != 1 || steps[0].(map[string]any)["id"] != "ok" {
		t.Errorf("steps = %s, want the step with a control id dropped", dump(steps))
	}
	if fields := list(t, secs[1], "fields"); len(fields) != 1 || fields[0].(map[string]any)["label"] != "multi" {
		t.Errorf("fields = %s, want the field with an escape character dropped and the multi-line one kept", dump(fields))
	}
}

func TestARowDetailOverALimitIsRefusedWhole(t *testing.T) {
	steps := func(chips, actions int) string {
		return `{"sections":[{"kind":"steps","title":"S","steps":[{"id":"s","title":"T","chips":` + repeat(`{"label":"c"}`, chips) +
			`,"actions":` + repeat(`{"id":"a","label":"A","confirm":"click"}`, actions) + `}]}]}`
	}
	if _, err := parseRowDetail(json.RawMessage(steps(maxChips, maxActions))); err != nil {
		t.Errorf("a step at the chip and action limits: %v, want it kept", err)
	}
	for name, raw := range map[string]string{
		"11 chips on a step":   steps(maxChips+1, 0),
		"11 actions on a step": steps(0, maxActions+1),
		"over 1 MiB":           `{"sections":[{"kind":"fields","title":"F","fields":[{"label":"big","value":"` + strings.Repeat("x", maxRowDetail) + `"}]}]}`,
		"not an object":        `"text"`,
	} {
		if d, err := parseRowDetail(json.RawMessage(raw)); err == nil {
			t.Errorf("%s: parseRowDetail = %s, want it refused", name, dump(d))
		}
	}
}

func TestARowDetailJustUnderOneMiBIsKept(t *testing.T) {
	pad := maxRowDetail - len(`{"sections":[{"kind":"fields","title":"F","fields":[{"label":"big","value":""}]}]}`) - 1024
	raw := `{"sections":[{"kind":"fields","title":"F","fields":[{"label":"big","value":"` + strings.Repeat("x", pad) + `"}]}]}`
	if len(raw) > maxRowDetail {
		t.Fatalf("test input is %d bytes, want it under 1 MiB", len(raw))
	}
	if _, err := parseRowDetail(json.RawMessage(raw)); err != nil {
		t.Errorf("parseRowDetail of %d bytes = %v, want it kept", len(raw), err)
	}
}

func TestMessageKindsKeepOnlyTheOnesWithAKindLabelAndJobAndCutThemToOneLine(t *testing.T) {
	long := strings.Repeat("j", maxTitleRunes+40)
	raw := `[
	 {"kind":"create_issue","label":"Create a GitHub issue","job":"Create a GitHub issue for this ticket."},
	 {"kind":"no_label","job":"x"},
	 {"label":"No kind","job":"x"},
	 {"kind":"no_job","label":"No job"},
	 {"kind":7,"label":"Number kind","job":"x"},
	 {"kind":"num_label","label":3,"job":"x"},
	 {"kind":"multi","label":"First line\nsecond line","job":"Job line\nsecond"},
	 {"kind":"long","label":"` + long + `","job":"` + long + `"},
	 "junk"]`
	got := map[string]map[string]any{}
	var order []string
	for _, k := range parseMessageKinds(json.RawMessage(raw)) {
		m := jsonOf(t, k)
		id, _ := m["kind"].(string)
		got[id] = m
		order = append(order, id)
	}
	if strings.Join(order, ",") != "create_issue,multi,long" {
		t.Fatalf("kinds kept = %v, want create_issue, multi, long: a bad declaration drops that kind only", order)
	}
	if got["create_issue"]["label"] != "Create a GitHub issue" || got["create_issue"]["job"] != "Create a GitHub issue for this ticket." {
		t.Errorf("create_issue = %v, want its label and job as declared", got["create_issue"])
	}
	if got["multi"]["label"] != "First line" || got["multi"]["job"] != "Job line" {
		t.Errorf("multi = %v, want one line each", got["multi"])
	}
	for _, f := range []string{"label", "job"} {
		if n := len([]rune(got["long"][f].(string))); n != maxTitleRunes {
			t.Errorf("long %s is %d runes, want it cut to %d", f, n, maxTitleRunes)
		}
	}
}

func TestMessageKindsThatAreNotAListAreNone(t *testing.T) {
	for _, raw := range []string{`{}`, `"x"`, `null`, ``} {
		if k := parseMessageKinds(json.RawMessage(raw)); len(k) != 0 {
			t.Errorf("parseMessageKinds(%q) = %s, want none", raw, dump(k))
		}
	}
}

// A conn that answers page.get and row.get from what the test sets, and records each call.
type pageConn struct {
	page    json.RawMessage
	pageErr error
	rows    map[string]json.RawMessage
	calls   []pageCall
}

type pageCall struct {
	method  string
	params  string
	timeout time.Duration
}

func (c *pageConn) call(_ context.Context, method string, params any, timeout time.Duration) (json.RawMessage, error) {
	c.calls = append(c.calls, pageCall{method, dump(params), timeout})
	switch method {
	case "page.get":
		return c.page, c.pageErr
	case "row.get":
		id := params.(map[string]any)["id"].(string)
		if res, ok := c.rows[id]; ok {
			return res, nil
		}
		return nil, &rpcError{Code: -32000, Message: "no such row"}
	}
	return nil, fmt.Errorf("unexpected %s", method)
}
func (c *pageConn) stop()                   {}
func (c *pageConn) kill()                   {}
func (c *pageConn) exited() <-chan struct{} { return nil }
func (c *pageConn) exitReason() string      { return "" }

func pageRunner() *runner { return &runner{key: "k", sid: 1, notify: func() {}} }

func pageTitles(t *testing.T, r *runner) (state string, titles []string, waiting any) {
	t.Helper()
	v, ok := r.Page()
	if !ok {
		t.Fatal("Page() reports no page")
	}
	m := jsonOf(t, v)
	for _, g := range list(t, m, "groups") {
		titles = append(titles, g.(map[string]any)["title"].(string))
	}
	return m["state"].(string), titles, m["waiting"]
}

func TestAPageIsStartingWithNoGroupsUntilItsFirstGoodPageGet(t *testing.T) {
	r := pageRunner()
	state, titles, waiting := pageTitles(t, r)
	if state != "starting" || len(titles) != 0 || waiting != float64(0) {
		t.Errorf("before page.get: state %q groups %v waiting %v, want starting, none, 0", state, titles, waiting)
	}
	if groups, ok := jsonOf(t, mustPage(t, r))["groups"].([]any); !ok || groups == nil {
		t.Errorf("an empty page's groups = %v, want an empty list, not null", jsonOf(t, mustPage(t, r))["groups"])
	}
	c := &pageConn{page: json.RawMessage(pageOf(`[{"id":"a","title":"Alpha","rows":[]}]`, 3))}
	r.pullPage(t.Context(), c)
	state, titles, waiting = pageTitles(t, r)
	if state != "ready" || len(titles) != 1 || titles[0] != "Alpha" || waiting != float64(3) {
		t.Errorf("after a good page.get: state %q groups %v waiting %v, want ready, [Alpha], 3", state, titles, waiting)
	}
}

func mustPage(t *testing.T, r *runner) PageView {
	t.Helper()
	v, _ := r.Page()
	return v
}

func TestPageGetIsCalledWithNoParamsWithinThePluginCallLimit(t *testing.T) {
	c := &pageConn{page: json.RawMessage(pageOf(`[]`, 0))}
	pageRunner().pullPage(t.Context(), c)
	if len(c.calls) != 1 || c.calls[0].method != "page.get" || c.calls[0].params != "{}" || c.calls[0].timeout != callTimeout {
		t.Errorf("calls = %+v, want one page.get with {} and the call limit %s", c.calls, callTimeout)
	}
}

func TestAnAnswerThatIsOverALimitOrFailsLeavesTheLastGoodPageServed(t *testing.T) {
	good := pageOf(`[{"id":"a","title":"Alpha","rows":[{"id":"r1","title":"One"}]}]`, 2)
	for name, bad := range map[string]*pageConn{
		"over a limit":    {page: json.RawMessage(pageOf(`[{"id":"x","title":"Over","rows":`+rowsJSON(maxPageRows+1)+`}]`, 9))},
		"malformed":       {page: json.RawMessage(`{"groups":"nope"}`)},
		"a timeout":       {pageErr: errTimeout},
		"a generic error": {pageErr: errors.New("boom")},
	} {
		r := pageRunner()
		r.pullPage(t.Context(), &pageConn{page: json.RawMessage(good)})
		r.pullPage(t.Context(), bad)
		state, titles, waiting := pageTitles(t, r)
		if state != "ready" || len(titles) != 1 || titles[0] != "Alpha" || waiting != float64(2) {
			t.Errorf("%s: state %q groups %v waiting %v, want the good page (Alpha, waiting 2) still served", name, state, titles, waiting)
		}
	}
}

func TestABadFirstPageLeavesThePageStarting(t *testing.T) {
	r := pageRunner()
	r.pullPage(t.Context(), &pageConn{page: json.RawMessage(pageOf(groupsJSON(maxPageGroups+1), 5))})
	state, titles, waiting := pageTitles(t, r)
	if state != "starting" || len(titles) != 0 || waiting != float64(0) {
		t.Errorf("state %q groups %v waiting %v, want starting with nothing served: no good page yet", state, titles, waiting)
	}
}

func TestANewGoodPageReplacesTheOldOne(t *testing.T) {
	r := pageRunner()
	r.pullPage(t.Context(), &pageConn{page: json.RawMessage(pageOf(`[{"id":"a","title":"Alpha","rows":[]}]`, 1))})
	r.pullPage(t.Context(), &pageConn{page: json.RawMessage(pageOf(`[{"id":"b","title":"Beta","rows":[]}]`, 4))})
	if _, titles, waiting := pageTitles(t, r); len(titles) != 1 || titles[0] != "Beta" || waiting != float64(4) {
		t.Errorf("groups %v waiting %v, want Beta and 4", titles, waiting)
	}
}

func TestRowGetPassesTheIDAndIncludeForARowOnThePageAndAnUnknownRowNeverReachesThePlugin(t *testing.T) {
	r := pageRunner()
	c := &pageConn{
		page: json.RawMessage(pageOf(`[{"id":"a","title":"Alpha","rows":[{"id":"t1","title":"One"},{"id":"t2","title":"Two"}]}]`, 1)),
		rows: map[string]json.RawMessage{"t1": json.RawMessage(detailJSON)},
	}
	r.p, r.ready = c, true
	if _, err := r.rowDetail(t.Context(), "t1", nil); !errors.Is(err, errNoRow) {
		t.Fatalf("rowDetail before any page = %v, want errNoRow", err)
	}
	r.pullPage(t.Context(), c)
	calls := len(c.calls)

	d, err := r.rowDetail(t.Context(), "t1", []string{"closed", "queued"})
	if err != nil || len(list(t, jsonOf(t, d), "sections")) != 3 {
		t.Fatalf("rowDetail(t1) = %s, %v, want its three sections", dump(d), err)
	}
	last := c.calls[len(c.calls)-1]
	if last.method != "row.get" || last.params != `{"id":"t1","include":["closed","queued"]}` || last.timeout != callTimeout {
		t.Errorf("call = %+v, want row.get with the id and include and the call limit", last)
	}
	if _, err := r.rowDetail(t.Context(), "t2", nil); err == nil {
		t.Error("rowDetail(t2) the plugin errors on = nil error, want the error")
	}
	calls = len(c.calls)
	for _, id := range []string{"../../secret", "other", ""} {
		if _, err := r.rowDetail(t.Context(), id, nil); !errors.Is(err, errNoRow) {
			t.Errorf("rowDetail(%q) = %v, want errNoRow", id, err)
		}
	}
	if len(c.calls) != calls {
		t.Errorf("calls after unknown ids = %+v, want no row.get", c.calls[calls:])
	}
	if state, titles, _ := pageTitles(t, r); state != "ready" || len(titles) != 1 {
		t.Errorf("after the refused row.get: state %q groups %v, want the page untouched", state, titles)
	}
}

func TestASourceKnownToHaveNoPageHasNeitherPageNorRowDetail(t *testing.T) {
	r := pageRunner()
	r.noPage = true
	if _, ok := r.Page(); ok {
		t.Error("Page() = true, want false once initialize has been read without page")
	}
	if _, err := r.rowDetail(t.Context(), "t1", nil); !errors.Is(err, errNoPage) {
		t.Errorf("rowDetail = %v, want errNoPage", err)
	}
}
