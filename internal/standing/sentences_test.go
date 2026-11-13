package standing_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/standing"
)

var limitFields = []string{
	"ceilings.t1", "ceilings.t2", "ceilings.t3", "ceilings.t4", "ceilings.fleet",
	"sender_allowlist",
	"rates.launches_per_hour", "rates.launches_per_sender_per_day", "rates.concurrent",
	"quiet_hours", "repo_allowlist", "wall_clock_minutes",
	"spend.lookout_call_usd", "spend.daily_worker_usd", "spend.reviewer_call_usd",
	"quota_hold_percent", "write_deny",
	"skip.labels", "skip.senders", "skip.dedupe_keys",
}

func TestSentencesHaveOnePerLimitAndTwoFixedOnes(t *testing.T) {
	got := standing.Sentences(standing.PresetLimits(standing.Work))
	var fields []string
	var fixed []string
	for _, s := range got {
		if s.Sentence == "" {
			t.Errorf("sentence for %q is empty", s.Field)
		}
		if s.Field == "" {
			fixed = append(fixed, s.Sentence)
			continue
		}
		fields = append(fields, s.Field)
	}
	if strings.Join(fields, ",") != strings.Join(limitFields, ",") {
		t.Errorf("sentence fields = %v, want one per Limit in catalog order: %v", fields, limitFields)
	}
	if len(fixed) != 2 || !strings.Contains(fixed[0], "push") || !strings.Contains(fixed[1], "registries") {
		t.Errorf("fixed sentences = %q, want two with no field: a restricted ship never pushes, and its network is package registries only", fixed)
	}
}

func sentenceFor(t *testing.T, l standing.Limits, field string) string {
	t.Helper()
	for _, s := range standing.Sentences(l) {
		if s.Field == field {
			return s.Sentence
		}
	}
	t.Fatalf("no sentence for %s", field)
	return ""
}

func TestSentencesReadTheValues(t *testing.T) {
	home := standing.PresetLimits(standing.Home)
	home.Ceilings.T3 = standing.Ceiling{Level: standing.A1}
	if s := sentenceFor(t, home, "ceilings.t3"); !strings.Contains(s, "Strangers (T3)") || !strings.Contains(s, "proposal") {
		t.Errorf("T3 at A1 = %q, want it to say strangers (T3) get at most a proposal", s)
	}
	if s := sentenceFor(t, standing.PresetLimits(standing.Work), "wall_clock_minutes"); !strings.Contains(s, "60") {
		t.Errorf("wall clock = %q, want it to name 60 minutes", s)
	}
	if s := sentenceFor(t, standing.PresetLimits(standing.Work), "rates.launches_per_hour"); !strings.Contains(s, "2") {
		t.Errorf("launches per hour = %q, want it to name 2", s)
	}
	if s := sentenceFor(t, standing.PresetLimits(standing.Work), "quota_hold_percent"); !strings.Contains(s, "90") {
		t.Errorf("quota hold = %q, want it to name 90%%", s)
	}
}

func TestSentenceForNoQuietHoursSaysLaunchesAreNeverHeld(t *testing.T) {
	s := sentenceFor(t, standing.PresetLimits(standing.Work), "quiet_hours")
	if !strings.Contains(s, "never held") {
		t.Errorf("quiet hours with no windows = %q, want it to say launches are never held for the time of day", s)
	}
	l := standing.PresetLimits(standing.Work)
	l.QuietHours = []standing.Window{{Start: "22:00", End: "06:00", Weekdays: []string{"fri"}}}
	s = sentenceFor(t, l, "quiet_hours")
	if !strings.Contains(s, "22:00") || !strings.Contains(s, "06:00") || strings.Contains(s, "never held") {
		t.Errorf("quiet hours with a window = %q, want it to name 22:00 and 06:00", s)
	}
}

func TestSentencesAvoidTheWordsTheGlossaryRetires(t *testing.T) {
	banned := regexp.MustCompile(`(?i)\b(rule|policy|permission|order)s?\b`)
	rich := standing.PresetLimits(standing.Home)
	rich.SenderAllowlist = []string{"alice@acme.test"}
	rich.RepoAllowlist = []string{"acme/widget"}
	rich.WriteDeny = []string{".github/**"}
	rich.QuietHours = []standing.Window{{Start: "22:00", End: "06:00", Weekdays: []string{"fri"}}}
	rich.Skip.Senders = []standing.Scoped{{Value: "bot@ci.test", Routine: "triage"}}
	for name, l := range map[string]standing.Limits{"work": standing.PresetLimits(standing.Work), "home": standing.PresetLimits(standing.Home), "rich": rich} {
		got := standing.Sentences(l)
		if len(got) == 0 {
			t.Errorf("%s: no sentences", name)
		}
		for _, s := range got {
			if w := banned.FindString(s.Sentence); w != "" {
				t.Errorf("%s: sentence %q uses %q", name, s.Sentence, w)
			}
		}
	}
}

func TestTheQuotaHoldSentenceSaysWhatTheLimitDoes(t *testing.T) {
	for _, s := range standing.Sentences(standing.PresetLimits(standing.Work)) {
		if s.Field != "quota_hold_percent" {
			continue
		}
		for _, want := range []string{"unattended", "plan window", "90%"} {
			if !strings.Contains(s.Sentence, want) {
				t.Errorf("sentence %q lacks %q", s.Sentence, want)
			}
		}
		if strings.Contains(s.Sentence, "outside events") {
			t.Errorf("sentence %q still says outside events", s.Sentence)
		}
		return
	}
	t.Fatal("no quota_hold_percent sentence")
}
