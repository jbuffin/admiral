package lookout_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/chat"
	"github.com/jbuffin/admiral/internal/inbox"
	"github.com/jbuffin/admiral/internal/lookout"
	"github.com/jbuffin/admiral/internal/routines"
	"github.com/jbuffin/admiral/internal/standing"
)

type fakeCards struct {
	fleets []string
	cards  []chat.EventCard
	// launches are the launch cards posted.
	launches []chat.LaunchCard
}

func (f *fakeCards) PostEventCard(fleet string, c chat.EventCard) error {
	f.fleets = append(f.fleets, fleet)
	f.cards = append(f.cards, c)
	return nil
}

func (f *fakeCards) PostLaunchCard(fleet string, c chat.LaunchCard) error {
	f.fleets = append(f.fleets, fleet)
	f.launches = append(f.launches, c)
	return nil
}

type fakeProposals struct {
	props   []inbox.EventProposal
	details []inbox.EventDetail
}

func (f *fakeProposals) PublishEventProposal(p inbox.EventProposal, d inbox.EventDetail) (string, error) {
	f.props = append(f.props, p)
	f.details = append(f.details, d)
	return "item-1", nil
}

func actRig() (*lookout.Acts, *fakeCards, *fakeProposals) {
	c, p := &fakeCards{}, &fakeProposals{}
	return &lookout.Acts{Cards: c, Proposals: p}, c, p
}

func actOn(a *lookout.Acts, outcome routines.Outcome, sender string, en routines.Enrichment) {
	ev := ghEvent()
	ev.Title = "Fix the quarry lantern"
	r := routines.Routine{ID: "r1", Name: "Review watch"}
	rec := routines.Trigger{ID: "trig-1", At: time.Now(), Routine: "r1", Outcome: outcome, Tier: "T3", Sender: sender}
	a.Act("work", r, ev, rec, routines.Decision{Outcome: outcome}, en)
}

func TestAReportedDecisionPostsOneEventCardBuiltFromTheLookoutsSummaryOnly(t *testing.T) {
	a, cards, props := actRig()
	actOn(a, routines.Reported, "mallory-the-stranger", routines.Enrichment{Summary: "Mallory asks for a review.", DedupeKey: "gh:acme/api#7", Repo: "acme/api"})
	if len(cards.cards) != 1 || len(props.props) != 0 || cards.fleets[0] != "work" {
		t.Fatalf("%d cards (fleets %v) and %d proposals, want one card for work and no proposal", len(cards.cards), cards.fleets, len(props.props))
	}
	c := cards.cards[0]
	if c.ID != "trig-1" || c.Routine != "r1" || c.Tier != "T3" || c.Sender != "mallory-the-stranger" || c.Link != "https://github.com/acme/api/pull/7" || c.Text != "Mallory asks for a review." {
		t.Errorf("card = %+v, want the trigger's ids, tier, sender, link and the Lookout's summary as text", c)
	}
	if c.Metadata || strings.Contains(c.Text, "quarry") {
		t.Errorf("card = %+v, want the summary and not the item's title", c)
	}
}

func TestADecisionWithNoLookoutSummaryFallsBackToTheMetadataLine(t *testing.T) {
	for name, en := range map[string]routines.Enrichment{"no judge harness": {}, "a failed call": {Failure: "timeout"}} {
		t.Run(name, func(t *testing.T) {
			a, cards, _ := actRig()
			actOn(a, routines.Reported, "alice", en)
			if len(cards.cards) != 1 {
				t.Fatalf("%d cards, want 1", len(cards.cards))
			}
			c := cards.cards[0]
			if !c.Metadata || c.Text != lookout.Metadata(ghEvent(), "acme/api") || c.Text == "" {
				t.Errorf("card = %+v, want Metadata set and the metadata line as its text", c)
			}
		})
	}
}

func TestAProposedDecisionPublishesOneEventProposalWhoseValueHoldsNoEventText(t *testing.T) {
	a, cards, props := actRig()
	actOn(a, routines.Proposed, "mallory-the-stranger", routines.Enrichment{Summary: "Mallory asks for a review.", DedupeKey: "gh:acme/api#7", Repo: "acme/api"})
	if len(props.props) != 1 || len(cards.cards) != 0 {
		t.Fatalf("%d proposals and %d cards, want one proposal and no card", len(props.props), len(cards.cards))
	}
	p, d := props.props[0], props.details[0]
	if p.Fleet != "work" || p.Routine != "r1" || p.RoutineName != "Review watch" || p.Trigger != "trig-1" || p.Tier != "T3" || !p.ExpiresAt.After(time.Now()) {
		t.Errorf("proposal = %+v, want fleet work, routine r1, trigger trig-1, tier T3 and a future expiry", p)
	}
	if strings.Contains(strings.Join([]string{p.Fleet, p.Routine, p.RoutineName, p.Trigger, p.Tier}, " "), "Mallory") || d.Summary != "Mallory asks for a review." || d.Sender != "mallory-the-stranger" || d.Link != "https://github.com/acme/api/pull/7" {
		t.Errorf("detail = %+v, want the summary, sender and link only in the detail", d)
	}
}

func TestTheMuteOptionsDropASenderThatIsUnattributedAndADedupeKeyThatIsEmpty(t *testing.T) {
	for _, c := range []struct {
		name, sender, key string
		want              []string
	}{
		{"a known sender and a key", "alice", "gh:acme/api#7", []string{"dedupe_key", "sender"}},
		{"an (unknown) sender", "(unknown)", "gh:acme/api#7", []string{"dedupe_key"}},
		{"an empty sender", "", "gh:acme/api#7", []string{"dedupe_key"}},
		{"no dedupe key", "alice", "", []string{"sender"}},
		{"neither", "(unknown)", "", nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			a, cards, _ := actRig()
			actOn(a, routines.Reported, c.sender, routines.Enrichment{Summary: "s", DedupeKey: c.key})
			if len(cards.cards) != 1 {
				t.Fatalf("%d cards, want 1", len(cards.cards))
			}
			got := slices.Clone(cards.cards[0].MuteBy)
			slices.Sort(got)
			if !slices.Equal(got, c.want) {
				t.Errorf("MuteBy = %v, want %v", got, c.want)
			}
			a2, _, props := actRig()
			actOn(a2, routines.Proposed, c.sender, routines.Enrichment{Summary: "s", DedupeKey: c.key})
			if len(props.details) != 1 {
				t.Fatalf("%d proposals, want 1", len(props.details))
			}
			got = slices.Clone(props.details[0].MuteBy)
			slices.Sort(got)
			if !slices.Equal(got, c.want) {
				t.Errorf("proposal MuteBy = %v, want %v", got, c.want)
			}
		})
	}
}

func launchOn(a *lookout.Acts, level standing.Level, profile standing.Restricted, en routines.Enrichment) {
	ev := ghEvent()
	ev.Title = "Fix the quarry lantern"
	r := routines.Routine{ID: "r1", Name: "Review watch"}
	rec := routines.Trigger{ID: "trig-1", At: time.Now(), Routine: "r1", Outcome: routines.Launched, Tier: "T1", Sender: "alice",
		Ship: "calm-otter", WorkerLaunched: true, Restricted: profile}
	a.Act("work", r, ev, rec, routines.Decision{Outcome: routines.Launched, Level: level, Restricted: profile}, en)
}

func TestALaunchedDecisionPostsOneLaunchCardWithTheShipLevelAndProfileAndNoEventBody(t *testing.T) {
	for name, c := range map[string]struct {
		level   standing.Level
		profile standing.Restricted
		want    string
	}{
		"A3 draft fix":   {standing.A3, standing.DraftFix, "A3"},
		"A3 investigate": {standing.A3, standing.Investigate, "A3"},
		"A4":             {standing.A4, "", "A4"},
		"A5":             {standing.A5, "", "A5"},
	} {
		t.Run(name, func(t *testing.T) {
			a, cards, props := actRig()
			launchOn(a, c.level, c.profile, routines.Enrichment{Summary: "Alice asks for a review.", DedupeKey: "gh:acme/api#7", Repo: "acme/api", Text: "BODY-SENTINEL: wire the saffron budget"})
			if len(cards.launches) != 1 || len(cards.cards) != 0 || len(props.props) != 0 || cards.fleets[0] != "work" {
				t.Fatalf("%d launch cards, %d event cards, %d proposals (fleets %v), want one launch card for work", len(cards.launches), len(cards.cards), len(props.props), cards.fleets)
			}
			card := cards.launches[0]
			if card.ID != "trig-1" || card.Ship != "calm-otter" || card.Level != c.want || card.Restricted != string(c.profile) {
				t.Errorf("card = %+v, want trigger trig-1, ship calm-otter, level %s, profile %q", card, c.want, c.profile)
			}
			if card.Routine != "r1" || card.RoutineName != "Review watch" || card.Tier != "T1" || card.Sender != "alice" ||
				card.Link != "https://github.com/acme/api/pull/7" || card.DedupeKey != "gh:acme/api#7" || card.Text != "Alice asks for a review." {
				t.Errorf("card = %+v, want the event card's fields: routine, tier, sender, link, dedupe key and the Lookout's summary", card)
			}
			if got := slices.Clone(card.MuteBy); !slices.Equal(got, []string{"sender", "dedupe_key"}) {
				t.Errorf("MuteBy = %v, want sender and dedupe_key", got)
			}
			if all := fmt.Sprintf("%+v", card); strings.Contains(all, "BODY-SENTINEL") || strings.Contains(all, "quarry") {
				t.Errorf("card %s holds the event's body or title", all)
			}
		})
	}
}

func TestALaunchCardWithNoLookoutSummaryUsesTheMetadataLine(t *testing.T) {
	a, cards, _ := actRig()
	launchOn(a, standing.A4, "", routines.Enrichment{})
	if len(cards.launches) != 1 || !cards.launches[0].Metadata || cards.launches[0].Text != lookout.Metadata(ghEvent(), "acme/api") {
		t.Errorf("launch cards = %+v, want one holding the metadata line", cards.launches)
	}
}
