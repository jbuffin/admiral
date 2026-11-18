package sources

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func noticeJSON(id, text string) string {
	return fmt.Sprintf(`{"id":%s,"text":%s}`, quote(id), quote(text))
}

func noticesPage(notices string) string {
	return `{"groups":` + alphaPage + `,"waiting":0,"notices":` + notices + `}`
}

func parsedNotices(t *testing.T, raw string) []map[string]any {
	t.Helper()
	p, err := parsePage(json.RawMessage(raw))
	if err != nil {
		t.Fatalf("parsePage: %v", err)
	}
	return noticeList(t, jsonOf(t, p))
}

func noticeList(t *testing.T, v map[string]any) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, n := range list(t, v, "notices") {
		out = append(out, n.(map[string]any))
	}
	return out
}

func noticeIDs(ns []map[string]any) string {
	var ids []string
	for _, n := range ns {
		ids = append(ids, n["id"].(string))
	}
	return strings.Join(ids, ",")
}

func manyNotices(n int) string {
	items := make([]string, n)
	for i := range items {
		items[i] = noticeJSON(fmt.Sprintf("n%d", i), "Notice")
	}
	return "[" + strings.Join(items, ",") + "]"
}

// AC1-no-fields
func TestAPageWithoutNoticesOrLoadingServesNoticesAsAnEmptyList(t *testing.T) {
	p, err := parsePage(json.RawMessage(pageOf(alphaPage, 3)))
	if err != nil {
		t.Fatalf("parsePage: %v", err)
	}
	got := jsonOf(t, p)
	if n, ok := got["notices"].([]any); !ok || len(n) != 0 {
		t.Errorf("parsed notices = %v, want an empty list, not null or absent", got["notices"])
	}

	c := &pageConn{page: json.RawMessage(pageOf(alphaPage, 3))}
	r := livePageRunner(t, c)
	served := pageAnswer(t, r)
	if n, ok := served["notices"].([]any); !ok || len(n) != 0 {
		t.Errorf("served notices = %v, want an empty list", served["notices"])
	}
	if served["state"] != "ready" || served["waiting"] != float64(3) || len(list(t, served, "groups")) != 1 {
		t.Errorf("page = %s, want ready, the groups and waiting 3 as before", dump(served))
	}

	fresh := pageRunner()
	if n, ok := pageAnswer(t, fresh)["notices"].([]any); !ok || len(n) != 0 {
		t.Errorf("notices before any page.get = %v, want an empty list", pageAnswer(t, fresh)["notices"])
	}
}

// AC2-notice-shape
func TestNoticesAreServedInOrderWithTheirIDTextToneAndLink(t *testing.T) {
	ns := parsedNotices(t, noticesPage(`[
		{"id":"update","text":"An update is waiting","tone":"warn","link":{"label":"Release notes","url":"https://example.com/notes"}},
		{"id":"pick","text":"Pick your project","tone":"info"},
		{"id":"down","text":"Upstream is failing","tone":"error"}]`))
	if noticeIDs(ns) != "update,pick,down" {
		t.Fatalf("notices = %s, want update, pick, down in order", dump(ns))
	}
	for i, want := range []struct{ text, tone string }{{"An update is waiting", "warn"}, {"Pick your project", "info"}, {"Upstream is failing", "error"}} {
		if ns[i]["text"] != want.text || ns[i]["tone"] != want.tone {
			t.Errorf("notice %d = %s, want text %q tone %q", i, dump(ns[i]), want.text, want.tone)
		}
	}
	link, _ := ns[0]["link"].(map[string]any)
	if link["label"] != "Release notes" || link["url"] != "https://example.com/notes" {
		t.Errorf("link = %s, want label Release notes and the https url", dump(ns[0]["link"]))
	}
	if ns[1]["link"] != nil {
		t.Errorf("a notice with no link serves link = %v, want none", ns[1]["link"])
	}
}

// AC2-notice-shape
func TestANoticeToneOutsideTheFixedSetServesAsInfo(t *testing.T) {
	ns := parsedNotices(t, noticesPage(`[
		{"id":"a","text":"A","tone":"warning"},{"id":"b","text":"B","tone":"red"},{"id":"c","text":"C"},
		{"id":"d","text":"D","tone":7},{"id":"e","text":"E","tone":"#ff0000"},{"id":"f","text":"F","tone":"good"}]`))
	if len(ns) != 6 {
		t.Fatalf("notices = %s, want all six kept", dump(ns))
	}
	for _, n := range ns {
		if n["tone"] != "info" {
			t.Errorf("notice %s tone = %v, want info", n["id"], n["tone"])
		}
	}
}

// AC2-notice-shape
func TestANoticeTextIsOneLineWithoutInvisibleCharactersAndAtMost300Characters(t *testing.T) {
	ns := parsedNotices(t, noticesPage(`[
		{"id":"lines","text":"First line\nSecond line"},
		{"id":"bidi","text":"pay‮evil​\u0085ok"},
		{"id":"long","text":`+quote(strings.Repeat("é", 400))+`},
		{"id":"exact","text":`+quote(strings.Repeat("x", 300))+`}]`))
	if len(ns) != 4 {
		t.Fatalf("notices = %s, want all four kept", dump(ns))
	}
	if ns[0]["text"] != "First line" {
		t.Errorf("text = %q, want the first line only", ns[0]["text"])
	}
	if ns[1]["text"] != "payevilok" {
		t.Errorf("text = %q, want the invisible and bidi characters stripped", ns[1]["text"])
	}
	if n := len([]rune(ns[2]["text"].(string))); n != 300 {
		t.Errorf("a 400-character text came out %d characters, want 300", n)
	}
	if n := len([]rune(ns[3]["text"].(string))); n != 300 {
		t.Errorf("a 300-character text came out %d characters, want it kept whole", n)
	}
}

// AC2-notice-shape
func TestANoticeWhoseTextIsEmptyAfterCleaningIsDroppedAndTheRestOfTheAnswerStays(t *testing.T) {
	raw := `{"groups":` + alphaPage + `,"waiting":2,"notices":[
		{"id":"missing"},{"id":"blank","text":"   "},{"id":"invisible","text":"​‮"},{"id":"number","text":7},
		{"id":"kept","text":"Kept"},{"id":"newline","text":"\nlater line"}]}`
	p, err := parsePage(json.RawMessage(raw))
	if err != nil {
		t.Fatalf("parsePage: %v, want the answer to stand", err)
	}
	got := jsonOf(t, p)
	if ids := noticeIDs(noticeList(t, got)); ids != "kept" {
		t.Errorf("notices kept = %q, want only kept", ids)
	}
	if len(list(t, got, "groups")) != 1 || got["waiting"] != float64(2) {
		t.Errorf("page = %s, want the groups and waiting kept", dump(got))
	}
}

// AC2-notice-shape
func TestANoticeActionIsIgnored(t *testing.T) {
	ns := parsedNotices(t, noticesPage(`[{"id":"a","text":"A","action":{"id":"go","label":"Go","confirm":"click"}}]`))
	if len(ns) != 1 {
		t.Fatalf("notices = %s, want the notice kept", dump(ns))
	}
	if _, has := ns[0]["action"]; has {
		t.Errorf("notice = %s, want no action on it", dump(ns[0]))
	}
}

// AC3-bad-link
func TestABadNoticeLinkDropsOnlyTheLink(t *testing.T) {
	for name, link := range map[string]string{
		"a javascript url":   `{"label":"L","url":"javascript:alert(1)"}`,
		"a file url":         `{"label":"L","url":"file:///etc/passwd"}`,
		"a relative url":     `{"label":"L","url":"//example.com/x"}`,
		"a url with a space": `{"label":"L","url":"https://exa mple.com"}`,
		"no label":           `{"url":"https://example.com/x"}`,
		"an empty label":     `{"label":"","url":"https://example.com/x"}`,
		"an invisible label": `{"label":"​","url":"https://example.com/x"}`,
		"no url":             `{"label":"L"}`,
	} {
		ns := parsedNotices(t, noticesPage(`[{"id":"a","text":"Kept","tone":"warn","link":`+link+`},{"id":"b","text":"Next"}]`))
		if noticeIDs(ns) != "a,b" || ns[0]["text"] != "Kept" || ns[0]["tone"] != "warn" {
			t.Errorf("%s: notices = %s, want both kept with the first one's text and tone", name, dump(ns))
			continue
		}
		if ns[0]["link"] != nil {
			t.Errorf("%s: link = %s, want it dropped", name, dump(ns[0]["link"]))
		}
	}
	ns := parsedNotices(t, noticesPage(`[{"id":"a","text":"A","link":{"label":"L","url":"http://example.com/p"}}]`))
	if link, _ := ns[0]["link"].(map[string]any); link == nil || link["url"] != "http://example.com/p" {
		t.Errorf("link = %s, want an http url kept", dump(ns[0]["link"]))
	}
}

// AC4-refusals
func TestAnAnswerWithAnUnusableNoticeIsRefusedWhole(t *testing.T) {
	for name, notices := range map[string]string{
		"21 notices":         manyNotices(21),
		"no id":              `[{"text":"A"}]`,
		"an empty id":        `[{"id":"","text":"A"}]`,
		"a number id":        `[{"id":7,"text":"A"}]`,
		"a control id":       `[{"id":"a\nb","text":"A"}]`,
		"an id over the cap": `[{"id":"` + strings.Repeat("i", maxIDRunes+1) + `","text":"A"}]`,
		"a repeated id":      `[{"id":"a","text":"A"},{"id":"a","text":"B"}]`,
	} {
		if p, err := parsePage(json.RawMessage(noticesPage(notices))); err == nil {
			t.Errorf("%s: parsePage = %s, want the whole answer refused", name, dump(p))
		}
	}
}

// AC4-refusals
func TestTwentyNoticesAndAnIDAtTheCapAreKept(t *testing.T) {
	if ns := parsedNotices(t, noticesPage(manyNotices(20))); len(ns) != 20 {
		t.Errorf("20 notices: %d kept, want all 20", len(ns))
	}
	ns := parsedNotices(t, noticesPage(`[{"id":"`+strings.Repeat("i", maxIDRunes)+`","text":"A"}]`))
	if len(ns) != 1 {
		t.Errorf("an id at the cap: notices = %s, want it kept", dump(ns))
	}
	// A notice id may repeat a group or row id: the rules say unique among the page's notices.
	if ns := parsedNotices(t, `{"groups":`+alphaPage+`,"notices":[{"id":"a","text":"Same as the group"},{"id":"r1","text":"Same as the row"}]}`); len(ns) != 2 {
		t.Errorf("notice ids that match a group or row id: %s, want both kept", dump(ns))
	}
}

// AC4-refusals
func TestARefusedNoticesAnswerKeepsTheLastGoodGroupsAndNotices(t *testing.T) {
	good := noticesPage(`[{"id":"keep","text":"Kept notice","tone":"warn"}]`)
	for name, bad := range map[string]string{
		"21 notices":    noticesPage(manyNotices(21)),
		"no id":         noticesPage(`[{"text":"A"}]`),
		"a repeated id": noticesPage(`[{"id":"a","text":"A"},{"id":"a","text":"B"}]`),
	} {
		c := &pageConn{page: json.RawMessage(good)}
		r := livePageRunner(t, c)
		c.page = json.RawMessage(strings.Replace(bad, "Alpha", "Beta", 1))
		r.note(1, "page.changed", nil)
		r.processPulls(t.Context(), c)
		got := pageAnswer(t, r)
		if got["state"] != "ready" || noticeIDs(noticeList(t, got)) != "keep" {
			t.Errorf("%s: page = %s, want ready with the last good notice keep", name, dump(got))
		}
		if groups := list(t, got, "groups"); len(groups) != 1 || groups[0].(map[string]any)["title"] != "Alpha" {
			t.Errorf("%s: groups = %s, want the last good Alpha", name, dump(groups))
		}
	}
}

// AC5-loading-first
func TestLoadingBeforeAnyGoodPageStaysStartingAndServesTheNoticesWithoutGroupsOrWaiting(t *testing.T) {
	c := &pageConn{page: json.RawMessage(`{"loading":true,"groups":` + alphaPage + `,"waiting":4,"notices":[
		{"id":"pick","text":"Pick your project","tone":"warn","link":{"label":"Settings","url":"https://example.com/s"}}]}`)}
	r := livePageRunner(t, c)
	got := pageAnswer(t, r)
	if got["state"] != "starting" {
		t.Fatalf("page = %s, want state starting while loading", dump(got))
	}
	ns := noticeList(t, got)
	if len(ns) != 1 || ns[0]["id"] != "pick" || ns[0]["text"] != "Pick your project" || ns[0]["tone"] != "warn" {
		t.Errorf("notices = %s, want the loading answer's notice", dump(ns))
	}
	if groups := list(t, got, "groups"); len(groups) != 0 || got["waiting"] != float64(0) {
		t.Errorf("groups %s waiting %v, want none and 0 until a non-loading answer", dump(groups), got["waiting"])
	}
	if l := liveOf(t, r); l["state"] != "starting" || l["waiting"] != float64(0) {
		t.Errorf("live block = %s, want starting and waiting 0", dump(l))
	}
	if got["error"] != nil && got["error"] != "" {
		t.Errorf("page = %s, want no error", dump(got))
	}
}

// AC5-loading-first
func TestALaterAnswerWithoutLoadingDrawsTheGroupsAndItsOwnNotices(t *testing.T) {
	c := &pageConn{page: json.RawMessage(`{"loading":true,"groups":[],"notices":[{"id":"pick","text":"Pick your project"}]}`)}
	r := livePageRunner(t, c)
	before := versionOf(t, r)
	c.page = json.RawMessage(`{"groups":` + alphaPage + `,"waiting":2,"notices":[{"id":"later","text":"Later notice"}]}`)
	r.note(1, "page.changed", nil)
	r.processPulls(t.Context(), c)
	got := pageAnswer(t, r)
	if got["state"] != "ready" || len(list(t, got, "groups")) != 1 || got["waiting"] != float64(2) {
		t.Errorf("page = %s, want ready with the groups and waiting 2", dump(got))
	}
	if noticeIDs(noticeList(t, got)) != "later" {
		t.Errorf("notices = %s, want only the new answer's notice", dump(noticeList(t, got)))
	}
	if versionOf(t, r) <= before {
		t.Errorf("version stayed %d, want it to move so a client refetches", before)
	}
}

// AC5-loading-first
func TestNoticesFromALoadingAnswerStayThroughAuthRequiredAndError(t *testing.T) {
	loading := `{"loading":true,"notices":[{"id":"pick","text":"Pick your project"}]}`
	c := &pageConn{page: json.RawMessage(loading)}
	r := livePageRunner(t, c)
	r.note(1, "auth.required", json.RawMessage(`{"message":"Sign in"}`))
	got := pageAnswer(t, r)
	if got["state"] != "auth_required" || noticeIDs(noticeList(t, got)) != "pick" || len(list(t, got, "groups")) != 0 {
		t.Errorf("under auth.required = %s, want auth_required, the loading notice and no groups", dump(got))
	}

	c2 := &pageConn{page: json.RawMessage(loading)}
	r2 := livePageRunner(t, c2)
	r2.fail(errors.New("the plugin exited with status 1"))
	got = pageAnswer(t, r2)
	if got["state"] != "error" || noticeIDs(noticeList(t, got)) != "pick" || len(list(t, got, "groups")) != 0 {
		t.Errorf("after a crash = %s, want error, the loading notice and no groups", dump(got))
	}
}

// AC6-loading-later
func TestALoadingAnswerAfterAGoodPageChangesNothing(t *testing.T) {
	c := &pageConn{page: json.RawMessage(`{"groups":` + alphaPage + `,"waiting":3,"notices":[{"id":"keep","text":"Kept notice"}]}`)}
	r := livePageRunner(t, c)
	c.page = json.RawMessage(`{"loading":true,"groups":[{"id":"b","title":"Beta","rows":[]}],"waiting":9,"notices":[{"id":"new","text":"New notice"}]}`)
	r.note(1, "page.changed", nil)
	r.processPulls(t.Context(), c)
	got := pageAnswer(t, r)
	if got["state"] != "ready" || got["waiting"] != float64(3) {
		t.Errorf("page = %s, want ready with waiting 3", dump(got))
	}
	if groups := list(t, got, "groups"); len(groups) != 1 || groups[0].(map[string]any)["title"] != "Alpha" {
		t.Errorf("groups = %s, want the last good Alpha", dump(groups))
	}
	if noticeIDs(noticeList(t, got)) != "keep" {
		t.Errorf("notices = %s, want the last good notice keep", dump(noticeList(t, got)))
	}
	if l := liveOf(t, r); l["state"] != "ready" || l["waiting"] != float64(3) {
		t.Errorf("live block = %s, want ready and waiting 3", dump(l))
	}

	c.page = json.RawMessage(`{"groups":[{"id":"c","title":"Gamma","rows":[]}],"waiting":1}`)
	r.note(1, "page.changed", nil)
	r.processPulls(t.Context(), c)
	if state, titles, waiting := pageTitles(t, r); state != "ready" || len(titles) != 1 || titles[0] != "Gamma" || waiting != float64(1) {
		t.Errorf("after the next good answer: state %q groups %v waiting %v, want Gamma and 1", state, titles, waiting)
	}
}

// AC6-loading-later
func TestALoadingAnswerAfterAGoodPageStillClearsAPageError(t *testing.T) {
	c := &pageConn{page: json.RawMessage(pageOf(alphaPage, 2))}
	r := livePageRunner(t, c)
	c.page, c.pageErr = nil, &rpcError{Code: -32000, Message: "upstream down"}
	r.note(1, "page.changed", nil)
	r.processPulls(t.Context(), c)
	if got := pageAnswer(t, r); got["state"] != "error" {
		t.Fatalf("page = %s, want state error first", dump(got))
	}
	c.page, c.pageErr = json.RawMessage(`{"loading":true}`), nil
	r.note(1, "page.changed", nil)
	r.processPulls(t.Context(), c)
	got := pageAnswer(t, r)
	if got["state"] != "ready" || len(list(t, got, "groups")) != 1 || got["waiting"] != float64(2) {
		t.Errorf("page = %s, want ready with the last good groups and the error cleared", dump(got))
	}
}

// AC7-auth-error
func TestNoticesKeepShowingWhenTheSourceGoesAuthRequiredOrError(t *testing.T) {
	good := `{"groups":` + alphaPage + `,"waiting":2,"notices":[{"id":"keep","text":"Kept notice","tone":"error"}]}`

	c := &pageConn{page: json.RawMessage(good)}
	r := livePageRunner(t, c)
	r.note(1, "auth.required", json.RawMessage(`{"message":"Sign in"}`))
	got := pageAnswer(t, r)
	if got["state"] != "auth_required" || noticeIDs(noticeList(t, got)) != "keep" || len(list(t, got, "groups")) != 1 {
		t.Errorf("under auth.required = %s, want the notice kept with the groups", dump(got))
	}

	c2 := &pageConn{page: json.RawMessage(good)}
	r2 := livePageRunner(t, c2)
	r2.fail(errors.New("the plugin exited with status 1"))
	got = pageAnswer(t, r2)
	if got["state"] != "error" || noticeIDs(noticeList(t, got)) != "keep" || len(list(t, got, "groups")) != 1 {
		t.Errorf("after a crash = %s, want the notice kept with the groups", dump(got))
	}

	c3 := &pageConn{page: json.RawMessage(good)}
	r3 := livePageRunner(t, c3)
	c3.page, c3.pageErr = nil, &rpcError{Code: -32000, Message: "upstream down"}
	r3.note(1, "page.changed", nil)
	r3.processPulls(t.Context(), c3)
	got = pageAnswer(t, r3)
	if got["state"] != "error" || noticeIDs(noticeList(t, got)) != "keep" {
		t.Errorf("after a page.get error = %s, want the notice kept", dump(got))
	}
}

// AC8-waiting
func TestNoticesNeverAddToTheWaitingCountOrTheLiveBlock(t *testing.T) {
	notices := `[{"id":"a","text":"A","tone":"error"},{"id":"b","text":"B","tone":"warn"},{"id":"c","text":"C","tone":"info"}]`
	p, err := parsePage(json.RawMessage(`{"groups":` + alphaPage + `,"waiting":2,"notices":` + notices + `}`))
	if err != nil {
		t.Fatalf("parsePage: %v", err)
	}
	if got := jsonOf(t, p)["waiting"]; got != float64(2) {
		t.Errorf("waiting = %v with three notices, want the plugin's 2", got)
	}
	none, err := parsePage(json.RawMessage(`{"groups":` + alphaPage + `,"notices":` + notices + `}`))
	if err != nil {
		t.Fatalf("parsePage: %v", err)
	}
	if got := jsonOf(t, none)["waiting"]; got != float64(0) {
		t.Errorf("waiting = %v with no waiting and three notices, want 0", got)
	}

	c := &pageConn{page: json.RawMessage(`{"groups":` + alphaPage + `,"notices":` + notices + `}`)}
	r := livePageRunner(t, c)
	if l := liveOf(t, r); l["waiting"] != float64(0) {
		t.Errorf("live block = %s, want waiting 0 whatever the notices' tones", dump(l))
	}
	if got := pageAnswer(t, r); got["waiting"] != float64(0) || len(noticeList(t, got)) != 3 {
		t.Errorf("page = %s, want waiting 0 and three notices", dump(got))
	}
}
