package crewapi_test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/crewapi"
)

// --- GET crew carries update ---

func browseRows(t *testing.T, p *pubFixture) map[string]map[string]any {
	t.Helper()
	status, out := p.getJSON("/sources/hubot/crew")
	wantStatus(t, "browse", status, 200, out)
	rows, _ := out["members"].([]any)
	byName := map[string]map[string]any{}
	for _, r := range rows {
		m, _ := r.(map[string]any)
		byName[str(m, "name")] = m
	}
	return byName
}

func listUpstream(p *pubFixture, names ...string) {
	var list []map[string]any
	for _, n := range names {
		list = append(list, map[string]any{"name": n + ".md", "path": "crew/" + n + ".md", "type": "file", "sha": blobOf(n)})
	}
	p.gh.ok("GET", "repos/hubot/crew/contents/crew", list)
}

func TestUpdateBrowseRowsCarryTheUpdateKeyAndOnlyImportedOnesHaveAState(t *testing.T) {
	p := newImp(t, impOpts{pubOpts: pubOpts{noRepo: true}})
	p.clockIt(t)
	p.attach(att{name: "scout-hubot", upstreamName: "scout"})
	listUpstream(p, "scout", "bosun")
	rows := browseRows(t, p)
	for _, n := range []string{"scout", "bosun"} {
		if _, present := rows[n]["update"]; !present {
			t.Errorf("browse row %s has no update key: %v", n, rows[n])
		}
	}
	if rows["scout"]["imported"] != "scout-hubot" {
		t.Errorf("scout imported = %v, want scout-hubot", rows["scout"]["imported"])
	}
	if got, _ := rows["scout"]["update"].(string); got == "" {
		t.Errorf("the imported row has update %v, want one of the four states", rows["scout"]["update"])
	}
	if rows["bosun"]["update"] != nil {
		t.Errorf("the row that is not imported has update %v, want null", rows["bosun"]["update"])
	}
}

func TestUpdateAnOrphanOrUnusableSidecarIsNotMarkedImported(t *testing.T) {
	p := newImp(t, impOpts{pubOpts: pubOpts{noRepo: true}})
	p.clockIt(t)
	listUpstream(p, "scout")
	p.writeSidecarMap("scout", att{name: "scout"}.sidecar()) // a sidecar with no member beside it
	if got := browseRows(t, p)["scout"]["imported"]; got != nil {
		t.Errorf("an orphan sidecar marks the row imported as %v", got)
	}

	p.member("scout", "Mine.")
	bad := att{name: "scout"}.sidecar()
	bad["upstream_blob"] = "not-a-blob"
	p.writeSidecarMap("scout", bad)
	if got := browseRows(t, p)["scout"]["imported"]; got != nil {
		t.Errorf("a sidecar the reader refuses marks the row imported as %v", got)
	}
}

// --- GET /updates ---

func TestUpdateUpdatesRouteIsEngineerOnly(t *testing.T) {
	p := newImp(t, impOpts{pubOpts: pubOpts{noRepo: true}})
	p.attach(att{name: "moved"})
	status, body := p.asBot("GET", "/updates")
	wantStatus(t, "a bot's updates", status, 403, parse(t, body))
	p.wantCalls("a bot's updates", 0)
}

// --- preview ---

func TestUpdatePreviewShowsWhatApplyWouldWriteAndWritesNothing(t *testing.T) {
	u := newUpdatable(t, impOpts{})
	status, pv := u.preview("scout")
	wantStatus(t, "update preview", status, 200, pv)
	want := map[string]any{
		"name": "scout", "repo": "hubot/crew", "path": "crew/scout.md",
		"current_bytes": oldScout, "current_sha256": hexSum(oldScout),
		"bytes": newScout, "sha256": hexSum(newScout),
		"commit": newSHA, "tag": "v2", "blob": u.up.blob(), "up_to_date": false,
	}
	for k, v := range want {
		if !reflect.DeepEqual(pv[k], v) {
			t.Errorf("preview %s = %v, want %v", k, pv[k], v)
		}
	}
	for _, k := range []string{"card", "missing_skills", "scan"} {
		if _, ok := pv[k]; !ok {
			t.Errorf("preview has no %s: %v", k, pv)
		}
	}
	var got []string
	for _, c := range u.gh.calls() {
		got = append(got, c.Method+" "+c.Endpoint)
	}
	wantCalls := []string{
		"GET repos/hubot/crew",
		"GET repos/hubot/crew/commits/main",
		"GET repos/hubot/crew/contents/crew/scout.md?ref=" + newSHA,
		"GET repos/hubot/crew/tags?per_page=100",
	}
	if !reflect.DeepEqual(got, wantCalls) {
		t.Errorf("gh calls = %v\nwant %v", got, wantCalls)
	}
	if u.memberBytes("scout") != oldScout {
		t.Error("the preview changed the member")
	}
}

func TestUpdatePreviewWithNoTagHasATagKeyThatIsNull(t *testing.T) {
	u := newUpdatable(t, impOpts{})
	u.up.tags = nil
	u.gh.unseed()
	u.gh.seed(u.up)
	status, pv := u.preview("scout")
	wantStatus(t, "update preview", status, 200, pv)
	if v, present := pv["tag"]; !present || v != nil {
		t.Errorf("tag = %v (present %v), want a null tag key", v, present)
	}
}

func TestUpdatePreviewOfARenamedMemberHashesTheRawUpstreamBytes(t *testing.T) {
	u := newUpdatable(t, impOpts{})
	renamed := strings.Replace(oldScout, "name: scout", "name: scout-hubot", 1)
	u.attach(att{name: "scout-hubot", upstreamName: "scout", text: renamed, blob: blobOf("scout-old")})
	pv := u.previewOK("scout-hubot")
	if pv["sha256"] != hexSum(newScout) {
		t.Errorf("sha256 = %v, want the raw upstream bytes' %s", pv["sha256"], hexSum(newScout))
	}
	if pv["current_sha256"] != hexSum(renamed) {
		t.Errorf("current_sha256 = %v, want the local bytes'", pv["current_sha256"])
	}
	if pv["path"] != "crew/scout.md" {
		t.Errorf("path = %v", pv["path"])
	}
}

func TestUpdatePreviewIsUpToDateWhenTheBytesMatchTheMember(t *testing.T) {
	u := newUpdatable(t, impOpts{})
	u.up.text = oldScout
	u.gh.unseed()
	u.gh.seed(u.up)
	pv := u.previewOK("scout")
	if pv["up_to_date"] != true {
		t.Errorf("up_to_date = %v, want true", pv["up_to_date"])
	}
}

func TestUpdatePreviewOfAMemberRemovedUpstreamIs404(t *testing.T) {
	u := newUpdatable(t, impOpts{})
	u.gh.unseed()
	u.gh.ok("GET", "repos/hubot/crew", repoAs("hubot/crew", "main"))
	u.gh.ok("GET", "repos/hubot/crew/commits/main", map[string]any{"sha": newSHA})
	u.gh.answer("GET", "repos/hubot/crew/contents/crew/scout.md?ref="+newSHA, 404, nil, "")
	status, pv := u.preview("scout")
	wantStatus(t, "update preview", status, 404, pv)
	wantText(t, "update preview", pv, "was not found in", "hubot/crew")
}

func TestUpdatePreviewRefusesWhatImportRefusesAndWritesNothing(t *testing.T) {
	big := "---\nname: scout\nrole: triager\npick-me-when: x.\n---\n\n" + strings.Repeat("a", 64*1024)
	for name, text := range map[string]string{
		"hidden direction mark": memberFile("scout", "") + "‮",
		"invalid utf-8":         "---\nname: scout\nrole: triager\npick-me-when: x.\n---\n\n\xff\xfe\n",
		"over 64 KiB":           big,
		"ad-hoc":                "---\nname: ad-hoc\nrole: triager\npick-me-when: x.\n---\n\nHi.\n",
	} {
		t.Run(name, func(t *testing.T) {
			u := newUpdatable(t, impOpts{})
			u.up.text = text
			u.gh.unseed()
			u.gh.seed(u.up)
			status, pv := u.preview("scout")
			wantStatus(t, "update preview", status, 422, pv)
			if u.memberBytes("scout") != oldScout {
				t.Error("the member changed")
			}
		})
	}
}

func TestUpdatePreviewAnswersARateLimitWithTheResetTime(t *testing.T) {
	u := newUpdatable(t, impOpts{})
	u.gh.unseed()
	u.gh.answer("GET", "repos/hubot/crew", 403, nil, "gh: API rate limit exceeded for user ID 1. Try again in 42 minutes. (HTTP 403)")
	status, pv := u.preview("scout")
	wantStatus(t, "update preview", status, 429, pv)
	wantText(t, "update preview", pv, "Try again in 42 minutes")
}

func TestUpdatePreviewAndApplyOfADetachedMemberAre409WithNoGitHubCall(t *testing.T) {
	u := newUpdatable(t, impOpts{})
	writeFile(t, u.crewDir()+"/scout.md", oldScout+"\nEdited here.\n")
	status, pv := u.preview("scout")
	wantStatus(t, "update preview", status, 409, pv)
	wantText(t, "update preview", pv, "Edited here, so it no longer gets updates from", "hubot/crew")
	status, out := u.post("/members/scout/update", map[string]any{"commit": newSHA, "sha256": hexSum(newScout), "current_sha256": hexSum(oldScout)})
	wantStatus(t, "apply", status, 409, out)
	wantText(t, "apply", out, "Edited here, so it no longer gets updates from", "hubot/crew")
	u.wantCalls("a detached member", 0)
}

func TestUpdatePreviewOfAMemberWithAnUnusableSidecarIs409(t *testing.T) {
	for name, edit := range map[string]func(m map[string]any){
		"upstream_name is not the path stem": func(m map[string]any) { m["upstream_name"] = "other" },
		"blob is not 40 hex":                 func(m map[string]any) { m["upstream_blob"] = "xyz" },
	} {
		t.Run(name, func(t *testing.T) {
			u := newUpdatable(t, impOpts{})
			m := att{name: "scout", text: oldScout}.sidecar()
			edit(m)
			u.writeSidecarMap("scout", m)
			status, pv := u.preview("scout")
			wantStatus(t, "update preview", status, 409, pv)
			u.wantCalls("an unusable sidecar", 0)
		})
	}
}

func TestUpdatePreviewOfAMemberThatWasNeverImportedIs404(t *testing.T) {
	u := newUpdatable(t, impOpts{})
	u.member("plain", "Mine.")
	status, pv := u.preview("plain")
	wantStatus(t, "update preview of a plain member", status, 404, pv)
	status, pv = u.preview("nobody")
	wantStatus(t, "update preview of no member", status, 404, pv)
	u.wantCalls("members that were never imported", 0)
}

func TestUpdatePreviewAndApplyWithNoGitHubClientOrAnotherHostAre409AndCallNothing(t *testing.T) {
	for name, o := range map[string]pubOpts{"no client": {noGitHub: true}, "other host": {host: "ghe.example.com"}} {
		t.Run(name, func(t *testing.T) {
			u := newUpdatable(t, impOpts{pubOpts: o})
			status, pv := u.preview("scout")
			wantStatus(t, "update preview", status, 409, pv)
			if o.noGitHub {
				wantText(t, "update preview", pv, connectText)
			}
			status, out := u.post("/members/scout/update", map[string]any{"commit": newSHA, "sha256": hexSum(newScout), "current_sha256": hexSum(oldScout)})
			wantStatus(t, "apply", status, 409, out)
			if got := len(u.gh.calls()); got != 0 {
				t.Errorf("%d gh calls: %v", got, endpoints(u.gh.calls()))
			}
		})
	}
}

func TestUpdatePreviewAndApplyWorkWithSharingOff(t *testing.T) {
	u := newUpdatable(t, impOpts{pubOpts: pubOpts{off: true}})
	pv := u.previewOK("scout")
	status, out := u.apply("scout", pv, nil)
	wantStatus(t, "apply", status, 200, out)
}

// --- apply ---

func TestUpdateApplyWritesTheUpstreamBytesAndMovesTheSidecar(t *testing.T) {
	u := newUpdatable(t, impOpts{})
	u.clk.advance(time.Hour)
	pv := u.previewOK("scout")
	status, out := u.apply("scout", pv, nil)
	wantStatus(t, "apply", status, 200, out)
	if got := u.memberBytes("scout"); got != newScout {
		t.Errorf("member = %q, want the upstream bytes", got)
	}
	sc := u.sidecarOf("scout")
	for k, v := range map[string]any{"commit": newSHA, "tag": "v2", "upstream_blob": u.up.blob(), "content_sha256": hexSum(newScout)} {
		if sc[k] != v {
			t.Errorf("sidecar %s = %v, want %v", k, sc[k], v)
		}
	}
	for k, v := range map[string]any{"version": float64(1), "host": "github.com", "repo": "hubot/crew", "path": "crew/scout.md",
		"upstream_name": "scout", "imported_at": "2026-11-20T10:00:00Z", "from_elsewhere": "keep me"} {
		if sc[k] != v {
			t.Errorf("sidecar %s = %v, want it kept as %v", k, sc[k], v)
		}
	}
	if at, err := time.Parse(time.RFC3339, str(sc, "updated_at")); err != nil || !at.After(time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("updated_at = %v, want a later RFC 3339 time", sc["updated_at"])
	}
	if got := u.updateOf("scout"); got != "none" {
		t.Errorf("after Apply update = %q, want none", got)
	}
	if w := u.gh.writes(); len(w) != 0 {
		t.Errorf("Apply wrote to GitHub: %v", w)
	}
}

func TestUpdateApplyOfARenamedMemberWritesTheRenamedBytesWithoutAFalse409(t *testing.T) {
	u := newUpdatable(t, impOpts{})
	renamed := strings.Replace(oldScout, "name: scout", "name: scout-hubot", 1)
	u.attach(att{name: "scout-hubot", upstreamName: "scout", text: renamed, blob: blobOf("scout-old")})
	pv := u.previewOK("scout-hubot")
	status, out := u.apply("scout-hubot", pv, nil)
	wantStatus(t, "apply", status, 200, out)
	got := u.memberBytes("scout-hubot")
	if !strings.Contains(got, "name: scout-hubot") || !strings.Contains(got, "Work as scout, better.") {
		t.Errorf("member = %q, want the new body under the local name", got)
	}
}

func TestUpdateApplyAfterALocalEditOrAnUpstreamChangeIsAChangedConflictAndWritesNothing(t *testing.T) {
	t.Run("local edit", func(t *testing.T) {
		u := newUpdatable(t, impOpts{})
		pv := u.previewOK("scout")
		edited := oldScout + "\nEdited after the preview.\n"
		u.attach(att{name: "scout", text: edited, tag: "v1"})
		status, out := u.apply("scout", pv, nil)
		wantStatus(t, "apply", status, 409, out)
		if out["kind"] != "changed" {
			t.Errorf("kind = %v, want changed", out["kind"])
		}
		wantText(t, "apply", out, changedText)
		if u.memberBytes("scout") != edited {
			t.Error("Apply overwrote the edit")
		}
	})
	t.Run("upstream change", func(t *testing.T) {
		u := newUpdatable(t, impOpts{})
		pv := u.previewOK("scout")
		u.up.text = newScout + "\nMore.\n"
		u.gh.unseed()
		u.gh.seed(u.up)
		status, out := u.apply("scout", pv, nil)
		wantStatus(t, "apply", status, 409, out)
		if out["kind"] != "changed" {
			t.Errorf("kind = %v, want changed", out["kind"])
		}
		if u.memberBytes("scout") != oldScout {
			t.Error("Apply wrote bytes the preview did not show")
		}
	})
}

func TestUpdateApplyRefusesABadBodyBeforeAnyGitHubCall(t *testing.T) {
	good := map[string]any{"commit": newSHA, "sha256": hexSum(newScout), "current_sha256": hexSum(oldScout)}
	for name, over := range map[string]map[string]any{
		"short commit":       {"commit": "abc"},
		"commit not hex":     {"commit": strings.Repeat("z", 40)},
		"missing commit":     {"commit": nil},
		"short sha256":       {"sha256": "abc"},
		"sha256 not hex":     {"sha256": strings.Repeat("g", 64)},
		"missing sha256":     {"sha256": nil},
		"short current":      {"current_sha256": "abc"},
		"missing current":    {"current_sha256": nil},
		"current not a text": {"current_sha256": 7},
	} {
		t.Run(name, func(t *testing.T) {
			u := newUpdatable(t, impOpts{})
			body := map[string]any{}
			for k, v := range good {
				body[k] = v
			}
			for k, v := range over {
				body[k] = v
			}
			status, out := u.post("/members/scout/update", body)
			wantStatus(t, "apply", status, 400, out)
			u.wantCalls("a bad body", 0)
			if u.memberBytes("scout") != oldScout {
				t.Error("the member changed")
			}
		})
	}
}

func TestUpdateApplyThatCannotWriteTheSidecarRestoresTheMemberAndSaysNothingChanged(t *testing.T) {
	u := newUpdatable(t, impOpts{})
	pv := u.previewOK("scout")
	before := u.sidecarOf("scout")
	crewapi.FailSidecarWrites(t)
	status, out := u.apply("scout", pv, nil)
	wantStatus(t, "apply", status, 500, out)
	wantText(t, "apply", out, "nothing changed")
	if got := u.memberBytes("scout"); got != oldScout {
		t.Errorf("member = %q, want the old bytes back", got)
	}
	if !reflect.DeepEqual(u.sidecarOf("scout"), before) {
		t.Error("the sidecar changed")
	}
}

func TestUpdateApplyOfARemovedMemberIs404(t *testing.T) {
	u := newUpdatable(t, impOpts{})
	pv := u.previewOK("scout")
	u.gh.unseed()
	u.gh.answer("GET", "repos/hubot/crew/contents/crew/scout.md?ref="+newSHA, 404, nil, "")
	status, out := u.apply("scout", pv, nil)
	wantStatus(t, "apply", status, 404, out)
	wantText(t, "apply", out, "was not found in", "hubot/crew")
}

func TestUpdateApplyDoesNotInvalidateTheListingCache(t *testing.T) {
	p := newImp(t, impOpts{})
	p.clockIt(t)
	p.attach(att{name: "scout", repo: shareRepo, text: oldScout})
	up := upstream{owner: "octocat", repo: "admiral-crew", name: "scout", text: newScout, commit: newSHA, tags: []string{"v2"}}
	p.gh.seed(up)
	p.gh.upstreamListing(shareRepo, map[string]string{"scout": up.blob()})
	u := &updatable{pubFixture: p, up: up}
	status, _ := p.getJSON("/sharing")
	wantStatus(t, "sharing", status, 200, nil)
	before := len(p.gh.listings())
	if before == 0 {
		t.Fatal("nothing listed the share repo before Apply")
	}
	pv := u.previewOK("scout")
	status, out := u.apply("scout", pv, nil)
	wantStatus(t, "apply", status, 200, out)
	status, _ = p.getJSON("/sharing")
	wantStatus(t, "sharing", status, 200, nil)
	if got := len(p.gh.listings()); got != before {
		t.Errorf("listing calls went from %d to %d: Apply must not refetch the listing", before, got)
	}
}

// --- bots ---

func TestUpdateBotsAreRefusedOnEveryUpdateRouteAndNoGitHubCallIsMade(t *testing.T) {
	u := newUpdatable(t, impOpts{})
	for _, r := range []struct{ method, path string }{
		{"GET", "/updates"},
		{"POST", "/members/scout/update/preview"},
		{"POST", "/members/scout/update"},
	} {
		status, body := u.asBot(r.method, r.path)
		wantStatus(t, r.method+" "+r.path+" as a bot", status, 403, parse(t, body))
	}
	u.wantCalls("a bot on the update routes", 0)
	if u.memberBytes("scout") != oldScout {
		t.Error("a bot changed the member")
	}
}
