package ships

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/allowrules"
	"github.com/jbuffin/admiral/internal/inbox"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/permlog"
	"github.com/jbuffin/admiral/internal/server"
	"github.com/jbuffin/admiral/internal/sessionhost"
)

type ruleRig struct {
	*usageRig
	home string
}

func newRuleRig(t *testing.T) *ruleRig {
	t.Helper()
	r := &ruleRig{usageRig: newUsageRig(t)}
	r.home = r.s.Launcher.Home
	r.s.UsageRead = func(context.Context, sessionhost.Session) ExitUsage { return ExitUsage{} }
	return r
}

// launch starts a launch's part of a ship's permission log, as a first build or a feedback round does.
func (r *ruleRig) launch(t *testing.T, ship, at, harness string) {
	t.Helper()
	if err := permlog.Append(r.home, ship, permlog.Event{Ev: "launch", At: at, Harness: harness, Fleet: "work", Repo: "acme/widgets"}); err != nil {
		t.Fatal(err)
	}
}

func claudeCall(tool, content string) permlog.Call {
	sug, _ := json.Marshal([]map[string]any{{"type": "addRules", "behavior": "allow", "destination": "localSettings",
		"rules": []map[string]string{{"toolName": tool, "ruleContent": content}}}})
	return permlog.Call{Tool: tool, Kind: "shell", Suggestions: sug}
}

// prompt logs a prompt and its answer; crewBoat is empty for the ship's own.
func (r *ruleRig) prompt(t *testing.T, ship, id, crewBoat string, call permlog.Call, ans permlog.Answer, by permlog.By) {
	t.Helper()
	evs := []permlog.Event{{Ev: "ask", ID: id, Crew: crewBoat, Call: &call}}
	if ans != permlog.Open {
		evs = append(evs, permlog.Event{Ev: "answer", ID: id, Crew: crewBoat, Answer: ans, By: by})
	}
	if err := permlog.Append(r.home, ship, evs...); err != nil {
		t.Fatal(err)
	}
}

func (r *ruleRig) handAllowed(t *testing.T, ship, id, content string) {
	t.Helper()
	r.prompt(t, ship, id, "", claudeCall("Bash", content), permlog.Allow, permlog.Person)
}

func (r *ruleRig) report(t *testing.T, ship string, body any) {
	t.Helper()
	path, _, _ := launcher.ShipFiles(r.home, ship)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(body)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func (r *ruleRig) end(ship string) { r.s.queueEnded(ending1(exitedShip(ship, nil))) }

type proposal struct {
	file  string
	fleet string
	cards []map[string]any
}

func (c proposalCards) rule(text string) map[string]any {
	for _, card := range c {
		if card["rule"] == text {
			return card
		}
		if p, ok := card["pattern"].([]any); ok && len(p) > 0 {
			var words []string
			for _, w := range p {
				words = append(words, w.(string))
			}
			if strings.Join(words, " ") == text {
				return card
			}
		}
	}
	return nil
}

type proposalCards []map[string]any

// proposals are the allow rule proposal items in the _admiral inbox, answered or not.
func (r *ruleRig) proposals(t *testing.T) []proposal {
	t.Helper()
	dir := filepath.Join(r.home, "inbox", inbox.AdmiralSession)
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	var out []proposal
	for _, f := range files {
		if strings.HasSuffix(f, ".answer.json") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var it struct {
			Kind       string `json:"kind"`
			AllowRules struct {
				Fleet string           `json:"fleet"`
				Cards []map[string]any `json:"cards"`
			} `json:"allow_rules"`
		}
		if json.Unmarshal(b, &it) != nil || it.Kind != "allow_rules" {
			continue
		}
		out = append(out, proposal{f, it.AllowRules.Fleet, it.AllowRules.Cards})
	}
	return out
}

// cards is every card of every proposal.
func (r *ruleRig) cards(t *testing.T) proposalCards {
	t.Helper()
	var out proposalCards
	for _, p := range r.proposals(t) {
		out = append(out, p.cards...)
	}
	return out
}

func promptIDs(card map[string]any) []string {
	var ids []string
	ps, _ := card["prompts"].([]any)
	for _, p := range ps {
		ids = append(ids, p.(map[string]any)["id"].(string))
	}
	return ids
}

// promptShips are the ship names the card's prompts carry, in order.
func promptShips(card map[string]any) []string {
	var ships []string
	ps, _ := card["prompts"].([]any)
	for _, p := range ps {
		ship, _ := p.(map[string]any)["ship"].(string)
		ships = append(ships, ship)
	}
	return ships
}

const viewRuleText = "Bash(gh pr view:*)"

func TestAHandAllowedPromptLeavesOneCardWhenItsRoundEnds(t *testing.T) {
	t.Parallel()
	r := newRuleRig(t)
	r.launch(t, "saltpaw", "2026-11-11T10:00:00Z", "claude")
	r.handAllowed(t, "saltpaw", "p1", "gh pr view:*")
	r.end("saltpaw")

	ps := r.proposals(t)
	if len(ps) != 1 || ps[0].fleet != "work" || len(ps[0].cards) != 1 {
		t.Fatalf("proposals = %+v, want one item for fleet work with one card", ps)
	}
	c := ps[0].cards[0]
	if c["rule"] != viewRuleText || c["harness"] != "claude" || c["repo"] != "acme/widgets" || c["count"] != float64(1) {
		t.Errorf("card = %v, want %s for claude in acme/widgets, count 1", c, viewRuleText)
	}
	if ids := promptIDs(c); len(ids) != 1 || ids[0] != "p1" {
		t.Errorf("card prompts = %v, want p1", ids)
	}
	if ships := promptShips(c); !slices.Equal(ships, []string{"saltpaw"}) {
		t.Errorf("card prompt ships = %v, want saltpaw", ships)
	}
}

func TestASecondShipWithTheSamePromptRaisesTheCardsCountAndMakesNoSecondCard(t *testing.T) {
	t.Parallel()
	r := newRuleRig(t)
	r.launch(t, "saltpaw", "2026-11-11T10:00:00Z", "claude")
	r.handAllowed(t, "saltpaw", "p1", "gh pr view:*")
	r.end("saltpaw")
	r.launch(t, "dory", "2026-11-11T11:00:00Z", "claude")
	r.handAllowed(t, "dory", "p1", "gh pr view:*")
	r.end("dory")

	if ps := r.proposals(t); len(ps) != 1 {
		t.Fatalf("%d proposal items, want the one still waiting to get the second ship's prompt", len(ps))
	}
	cards := r.cards(t)
	if len(cards) != 1 || cards[0]["count"] != float64(2) || len(promptIDs(cards[0])) != 2 {
		t.Errorf("cards = %v, want one card with count 2 and both prompts", cards)
	}
}

func TestACrewBoatsHandAllowedPromptProposesUnderItsShip(t *testing.T) {
	t.Parallel()
	r := newRuleRig(t)
	r.launch(t, "saltpaw", "2026-11-11T10:00:00Z", "claude")
	r.prompt(t, "saltpaw", "b1", "implementer-1", claudeCall("Bash", "gh pr view:*"), permlog.Allow, permlog.Person)
	r.end("saltpaw")

	if cards := r.cards(t); len(cards) != 1 || cards[0]["rule"] != viewRuleText {
		t.Fatalf("cards = %v, want one for the crew boat's prompt", cards)
	}
	r.launch(t, "dory", "2026-11-11T11:00:00Z", "claude")
	r.prompt(t, "dory", "b1", "reviewer-1", claudeCall("Bash", "gh pr view:*"), permlog.Allow, permlog.Person)
	r.end("dory")
	cards := r.cards(t)
	if len(cards) != 1 || cards[0]["count"] != float64(2) {
		t.Fatalf("cards = %v, want the second ship's boat to raise the count to 2", cards)
	}
	if ships := promptShips(cards[0]); !slices.Equal(ships, []string{"saltpaw", "dory"}) {
		t.Errorf("card prompt ships = %v, want each boat's prompt to name its ship, saltpaw then dory", ships)
	}
}

func TestARestrictedShipAndAT2EventShipProposeNothingButAT1OneDoes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		restricted bool
		tier       string
		want       int
	}{
		{"restricted", true, "T0", 0},
		{"T2 event", false, "T2", 0},
		{"T3 event", false, "T3", 0},
		{"T1 event", false, "T1", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRuleRig(t)
			r.s.EventShip = func(fleet, session string) (bool, bool, string, error) {
				if fleet != "work" || session != "saltpaw" {
					t.Errorf("EventShip(%q, %q), want the ship's fleet and session", fleet, session)
				}
				return c.restricted, true, c.tier, nil
			}
			r.launch(t, "saltpaw", "2026-11-11T10:00:00Z", "claude")
			r.handAllowed(t, "saltpaw", "p1", "gh pr view:*")
			r.end("saltpaw")
			if got := len(r.cards(t)); got != c.want {
				t.Errorf("%d cards, want %d", got, c.want)
			}
		})
	}
}

func TestAnEventShipWhoseTierWontParseOrWhoseLookupFailsProposesNothing(t *testing.T) {
	t.Parallel()
	for name, lookup := range map[string]func(string, string) (bool, bool, string, error){
		"a tier that won't parse": func(string, string) (bool, bool, string, error) { return false, true, "high", nil },
		"an empty tier":           func(string, string) (bool, bool, string, error) { return false, true, "", nil },
		"a lookup error": func(string, string) (bool, bool, string, error) {
			return false, false, "", errors.New("trigger log unreadable")
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRuleRig(t)
			r.s.EventShip = lookup
			r.launch(t, "saltpaw", "2026-11-11T10:00:00Z", "claude")
			r.handAllowed(t, "saltpaw", "p1", "gh pr view:*")
			r.end("saltpaw")
			if cards := r.cards(t); len(cards) != 0 {
				t.Errorf("cards = %v, want none: a ship whose launch can't be read fails closed", cards)
			}
		})
	}
}

func TestAShipWithNoReportOrANonV2ReportStillProposes(t *testing.T) {
	t.Parallel()
	for name, report := range map[string]any{
		"no report":     nil,
		"a v1 report":   map[string]any{"version": 1, "result": "done", "why": "ok"},
		"an unreadable": "not an object",
	} {
		t.Run(name, func(t *testing.T) {
			r := newRuleRig(t)
			if report != nil {
				r.report(t, "saltpaw", report)
			}
			r.launch(t, "saltpaw", "2026-11-11T10:00:00Z", "claude")
			r.handAllowed(t, "saltpaw", "p1", "gh pr view:*")
			r.end("saltpaw")
			if cards := r.cards(t); len(cards) != 1 {
				t.Errorf("cards = %v, want one", cards)
			}
		})
	}
}

func TestAReportsReasonShowsOnTheCardWhoseRuleMatchesAndAddsNoCandidate(t *testing.T) {
	t.Parallel()
	r := newRuleRig(t)
	r.report(t, "saltpaw", map[string]any{"version": 2, "result": "done", "why": "ok", "allow_rules": []map[string]string{
		{"rule": viewRuleText, "reason": "read-only lookups of the PR I was asked to review"},
		{"rule": "Bash(rm -rf:*)", "reason": "never proposed, so never shown"},
	}})
	r.launch(t, "saltpaw", "2026-11-11T10:00:00Z", "claude")
	r.handAllowed(t, "saltpaw", "p1", "gh pr view:*")
	r.handAllowed(t, "saltpaw", "p2", "gh issue view:*")
	r.end("saltpaw")

	cards := r.cards(t)
	if len(cards) != 2 {
		t.Fatalf("cards = %v, want the two the prompts drew and none for the report's unknown rule", cards)
	}
	if c := cards.rule(viewRuleText); c == nil || c["reason"] != "read-only lookups of the PR I was asked to review" {
		t.Errorf("view card = %v, want the report's reason", c)
	}
	if c := cards.rule("Bash(gh issue view:*)"); c == nil || c["reason"] != nil && c["reason"] != "" {
		t.Errorf("issue card = %v, want no reason", c)
	}
}

func TestAReasonIsCutToTwoHundredCharacters(t *testing.T) {
	t.Parallel()
	r := newRuleRig(t)
	r.report(t, "saltpaw", map[string]any{"version": 2, "result": "done", "why": "ok", "allow_rules": []map[string]string{
		{"rule": viewRuleText, "reason": strings.Repeat("x", 300)},
	}})
	r.launch(t, "saltpaw", "2026-11-11T10:00:00Z", "claude")
	r.handAllowed(t, "saltpaw", "p1", "gh pr view:*")
	r.end("saltpaw")

	cards := r.cards(t)
	if len(cards) != 1 || cards[0]["reason"] != strings.Repeat("x", 200) {
		t.Errorf("cards = %v, want the reason cut to 200 characters", cards)
	}
}

func TestACodexCardHasNoRepoSaysItCoversEveryRepoAndTakesItsReasonByTheWordsOfItsPattern(t *testing.T) {
	t.Parallel()
	r := newRuleRig(t)
	r.report(t, "saltpaw", map[string]any{"version": 2, "result": "done", "why": "ok", "allow_rules": []map[string]string{
		{"rule": "make check", "reason": "runs the repo's checks"},
	}})
	r.launch(t, "saltpaw", "2026-11-11T10:00:00Z", "codex")
	r.prompt(t, "saltpaw", "p1", "", permlog.Call{Tool: "Bash", Kind: "shell", Command: "make check"}, permlog.Allow, permlog.Person)
	r.end("saltpaw")

	ps := r.proposals(t)
	if len(ps) != 1 || ps[0].fleet != "work" || len(ps[0].cards) != 1 {
		t.Fatalf("proposals = %+v, want one item for fleet work with one card", ps)
	}
	c := ps[0].cards[0]
	if c["harness"] != "codex" || c["every_repo"] != true || c["reason"] != "runs the repo's checks" {
		t.Errorf("card = %v, want codex, every_repo true and the reason", c)
	}
	if repo, has := c["repo"]; has && repo != "" {
		t.Errorf("card repo = %v, want none: a Codex rule covers every repo of the fleet", repo)
	}
	if p, _ := c["pattern"].([]any); len(p) != 2 || p[0] != "make" || p[1] != "check" {
		t.Errorf("card pattern = %v, want [make check]", c["pattern"])
	}
}

func TestAFeedbackRoundCountsOnlyItsOwnPrompts(t *testing.T) {
	t.Parallel()
	r := newRuleRig(t)
	r.launch(t, "saltpaw", "2026-11-11T10:00:00Z", "claude")
	r.handAllowed(t, "saltpaw", "p1", "gh pr view:*")
	r.end("saltpaw")
	r.launch(t, "saltpaw", "2026-11-11T12:00:00Z", "claude")
	r.handAllowed(t, "saltpaw", "p2", "gh pr view:*")
	r.end("saltpaw")

	cards := r.cards(t)
	if len(cards) != 1 || cards[0]["count"] != float64(2) {
		t.Fatalf("cards = %v, want one card counting each round's prompt once", cards)
	}
	if ids := promptIDs(cards[0]); len(ids) != 2 || ids[0] != "p1" || ids[1] != "p2" {
		t.Errorf("card prompts = %v, want p1 then p2", ids)
	}
}

func TestAFeedbackRoundReadsOnlyItsOwnPromptsEvenWhenTheEarlierItemIsGone(t *testing.T) {
	t.Parallel()
	r := newRuleRig(t)
	r.launch(t, "saltpaw", "2026-11-11T10:00:00Z", "claude")
	r.handAllowed(t, "saltpaw", "p1", "gh pr view:*")
	r.end("saltpaw")
	for _, p := range r.proposals(t) {
		if err := os.Remove(p.file); err != nil {
			t.Fatal(err)
		}
	}
	r.launch(t, "saltpaw", "2026-11-11T12:00:00Z", "claude")
	r.handAllowed(t, "saltpaw", "p2", "gh pr view:*")
	r.end("saltpaw")

	cards := r.cards(t)
	if len(cards) != 1 || cards[0]["count"] != float64(1) {
		t.Fatalf("cards = %v, want one card counting only the feedback round's prompt", cards)
	}
	if ids := promptIDs(cards[0]); len(ids) != 1 || ids[0] != "p2" {
		t.Errorf("card prompts = %v, want p2 only: the earlier round's p1 is not read again", ids)
	}
}

func TestANewShipReusingAnOldNameDoesNotProposeTheOldShipsPromptsAgain(t *testing.T) {
	t.Parallel()
	r := newRuleRig(t)
	r.launch(t, "saltpaw", "2026-11-11T10:00:00Z", "claude")
	r.handAllowed(t, "saltpaw", "p1", "gh pr view:*")
	r.end("saltpaw")
	first := r.proposals(t)
	if len(first) != 1 {
		t.Fatalf("proposals = %+v, want one", first)
	}
	r.removeItem(t, first[0].file)

	r.launch(t, "saltpaw", "2026-11-12T10:00:00Z", "claude")
	r.handAllowed(t, "saltpaw", "p2", "gh issue view:*")
	r.end("saltpaw")

	ps := r.proposals(t)
	if len(ps) != 2 {
		t.Fatalf("proposals = %+v, want the removed item and a new one", ps)
	}
	for _, p := range ps {
		if p.file == first[0].file {
			continue
		}
		if len(p.cards) != 1 || p.cards[0]["rule"] != "Bash(gh issue view:*)" {
			t.Errorf("new item cards = %v, want only the new ship's rule", p.cards)
		}
	}
}

func TestAnItemWithAnAnswerIsNeverRewrittenAndALaterRoundsSameRuleLandsOnANewOne(t *testing.T) {
	t.Parallel()
	r := newRuleRig(t)
	r.launch(t, "saltpaw", "2026-11-11T10:00:00Z", "claude")
	r.handAllowed(t, "saltpaw", "p1", "gh pr view:*")
	r.end("saltpaw")
	first := r.proposals(t)
	if len(first) != 1 {
		t.Fatalf("proposals = %+v, want one", first)
	}
	before, _ := os.ReadFile(first[0].file)
	r.removeItem(t, first[0].file)

	r.launch(t, "dory", "2026-11-12T10:00:00Z", "claude")
	r.handAllowed(t, "dory", "p1", "gh pr view:*")
	r.end("dory")

	if after, _ := os.ReadFile(first[0].file); string(after) != string(before) {
		t.Errorf("the answered item was rewritten:\n%s\nwas\n%s", after, before)
	}
	ps := r.proposals(t)
	if len(ps) != 2 {
		t.Fatalf("proposals = %+v, want the answered item and a new one", ps)
	}
	for _, p := range ps {
		if p.file != first[0].file && (len(p.cards) != 1 || p.cards[0]["rule"] != viewRuleText || p.cards[0]["count"] != float64(1)) {
			t.Errorf("new item cards = %v, want the rule again with count 1", p.cards)
		}
	}
	// Remove only hid the first item, so the rule was never skipped.
	if fl, _ := r.s.Launcher.AllowRules().List("work"); len(fl.Skipped) != 0 || len(fl.Rules) != 0 {
		t.Errorf("the store holds %+v after a Remove", fl)
	}
}

func TestARuleTheFleetAlreadyHasOrSkippedIsNotProposed(t *testing.T) {
	t.Parallel()
	rule := allowrules.Rule{Harness: "claude", Repo: "acme/widgets", Rule: viewRuleText}
	for name, seed := range map[string]func(*allowrules.Store) error{
		"already added": func(s *allowrules.Store) error { return s.Add("work", rule) },
		"skipped":       func(s *allowrules.Store) error { return s.Skip("work", rule) },
	} {
		t.Run(name, func(t *testing.T) {
			r := newRuleRig(t)
			if err := seed(r.s.Launcher.AllowRules()); err != nil {
				t.Fatal(err)
			}
			r.launch(t, "saltpaw", "2026-11-11T10:00:00Z", "claude")
			r.handAllowed(t, "saltpaw", "p1", "gh pr view:*")
			r.end("saltpaw")
			if cards := r.cards(t); len(cards) != 0 {
				t.Errorf("cards = %v, want none", cards)
			}
		})
	}
}

// removeItem is the engineer's Remove on an item, through the inbox service.
func (r *ruleRig) removeItem(t *testing.T, file string) {
	t.Helper()
	svc := &inbox.Service{Home: r.home, Hub: server.NewHub()}
	svc.Refresh()
	id := strings.TrimSuffix(filepath.Base(file), ".json")
	if _, err := svc.RemoveItem(inbox.AdmiralSession, id); err != nil {
		t.Fatalf("Remove %s: %v", id, err)
	}
}
