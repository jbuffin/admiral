package ledger

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

const launchSpec = `{"fleet":"home","harness":"claude","dir":"/code/widget","skill":{"kind":"none"},"text":"fix the build","autopilot":true,"resources":["db"]}`

func remind(text string, trig Trigger) Commitment {
	return Commitment{Text: text, Trigger: trig, Action: ActionRemind}
}

func mustCommit(t *testing.T, l *Ledger, fleet string, c Commitment, via string) Commitment {
	t.Helper()
	got, err := l.AddCommitment(fleet, c, via)
	if err != nil || got.ID == "" {
		t.Fatalf("AddCommitment(%+v) = %+v, %v; want a commitment with an id", c, got, err)
	}
	return got
}

func openIDs(t *testing.T, l *Ledger, fleet string) []string {
	t.Helper()
	cs, err := l.Commitments(fleet)
	if err != nil {
		t.Fatal(err)
	}
	out := []string{}
	for _, c := range cs {
		out = append(out, c.ID)
	}
	return out
}

func find(t *testing.T, l *Ledger, fleet, id string) (Commitment, bool) {
	t.Helper()
	cs, err := l.Commitments(fleet)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cs {
		if c.ID == id {
			return c, true
		}
	}
	return Commitment{}, false
}

// rawAdded is an added commitment event written by hand, so a test does not lean on AddCommitment.
func rawAdded(id, text string) string {
	return fmt.Sprintf(`{"v":1,"ts":"2026-11-12T09:00:00Z","event":"added","id":%q,"kind":"commitment","text":%q,"trigger":{"kind":"none"},"action":"remind"}`+"\n", id, text)
}

func rawEvent(event, id, reason string) string {
	if reason == "" {
		return fmt.Sprintf(`{"v":1,"ts":"2026-11-12T09:30:00Z","event":%q,"id":%q}`+"\n", event, id)
	}
	return fmt.Sprintf(`{"v":1,"ts":"2026-11-12T09:30:00Z","event":%q,"id":%q,"reason":%q}`+"\n", event, id, reason)
}

func rawCommitments(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString(rawAdded(fmt.Sprintf("c%02d", i), fmt.Sprintf("promise %d", i)))
	}
	return b.String()
}

func TestACommitmentIsStoredForEachTriggerKindAndReadBack(t *testing.T) {
	for _, trig := range []Trigger{
		{Kind: TriggerShipSailed, Subject: "saltpaw"},
		{Kind: TriggerShipAnchored, Subject: "saltpaw"},
		{Kind: TriggerPRMerged, Subject: "https://github.com/acme/widget/pull/7"},
		{Kind: TriggerPRClosed, Subject: "https://github.com/acme/widget/pull/7"},
		{Kind: TriggerIssueClosed, Subject: "https://github.com/acme/widget/issues/9"},
		{Kind: TriggerTime, Subject: "2026-11-13T09:00:00Z"},
		{Kind: TriggerNone},
	} {
		t.Run(trig.Kind, func(t *testing.T) {
			l, _ := newLedger(t)
			added := mustCommit(t, l, "home", remind("tell me when it lands", trig), "admiralbot")
			if added.Trigger != trig || added.Action != ActionRemind || added.Text != "tell me when it lands" || added.Via != "admiralbot" || !added.TS.Equal(t0) {
				t.Errorf("added = %+v, want trigger %+v, remind, the text, via admiralbot, ts %v", added, trig, t0)
			}
			got, ok := find(t, l, "home", added.ID)
			if !ok || got.Trigger != trig || got.Action != ActionRemind || got.Text != "tell me when it lands" || got.Waiting {
				t.Errorf("read back = %+v, %v; want the same commitment, not waiting", got, ok)
			}
		})
	}
}

func TestALaunchCommitmentKeepsItsStoredSpec(t *testing.T) {
	l, _ := newLedger(t)
	added := mustCommit(t, l, "home", Commitment{Text: "launch the fix", Trigger: Trigger{Kind: TriggerShipAnchored, Subject: "saltpaw"}, Action: ActionLaunch, Launch: json.RawMessage(launchSpec)}, "admiralbot")
	got, ok := find(t, l, "home", added.ID)
	if !ok || got.Action != ActionLaunch {
		t.Fatalf("read back = %+v, %v; want a launch commitment", got, ok)
	}
	var want, have map[string]any
	if err := json.Unmarshal([]byte(launchSpec), &want); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(got.Launch, &have); err != nil || !reflect.DeepEqual(want, have) {
		t.Errorf("launch spec = %s (%v), want %s", got.Launch, err, launchSpec)
	}
}

func TestACommitmentIsOneEventLineWithTheNoteFieldsPlusTriggerActionAndLaunch(t *testing.T) {
	l, home := newLedger(t)
	added := mustCommit(t, l, "home", Commitment{Text: "launch the fix", Trigger: Trigger{Kind: TriggerPRMerged, Subject: "https://github.com/acme/widget/pull/7"}, Action: ActionLaunch, Launch: json.RawMessage(launchSpec)}, "admiralbot")
	got := lines(t, home, "home")
	if len(got) != 1 {
		t.Fatalf("lines = %q, want one", got)
	}
	var ev map[string]any
	if err := json.Unmarshal([]byte(got[0]), &ev); err != nil {
		t.Fatalf("line %q is not JSON: %v", got[0], err)
	}
	want := map[string]any{"v": float64(1), "ts": "2026-11-12T10:00:00Z", "event": "added", "id": added.ID, "kind": "commitment", "text": "launch the fix", "via": "admiralbot", "action": "launch",
		"trigger": map[string]any{"kind": "pr_merged", "subject": "https://github.com/acme/widget/pull/7"}}
	for k, w := range want {
		if !reflect.DeepEqual(ev[k], w) {
			t.Errorf("event %s = %v, want %v (line %s)", k, ev[k], w, got[0])
		}
	}
	if _, ok := ev["launch"].(map[string]any); !ok {
		t.Errorf("event launch = %v, want the spec as a JSON object", ev["launch"])
	}
}

func TestCommitmentTextIsTrimmedAndRefusedWhenEmptyTooLongOrHidden(t *testing.T) {
	l, home := newLedger(t)
	got := mustCommit(t, l, "home", remind("  \uFEFF tell me when it lands \n", Trigger{Kind: TriggerNone}), "")
	if got.Text != "tell me when it lands" {
		t.Errorf("text = %q, want it trimmed", got.Text)
	}
	before := len(lines(t, home, "home"))
	for name, c := range map[string]struct{ text, want string }{
		"empty":      {"   ", "commitment"},
		"too long":   {strings.Repeat("a", MaxNoteChars+1), "commitment"},
		"zero-width": {"tell me\u200B when", "commitment"},
		"bidi":       {"tell me \u202E when", "commitment"},
		"control":    {"tell me\x00 when", "commitment"},
	} {
		added, err := l.AddCommitment("home", remind(c.text, Trigger{Kind: TriggerNone}), "")
		var re *RefusedError
		if !errors.As(err, &re) || added.ID != "" {
			t.Errorf("%s: AddCommitment = %+v, %v; want a RefusedError and nothing stored", name, added, err)
			continue
		}
		if !strings.Contains(re.Msg, c.want) || strings.Contains(re.Msg, "note") {
			t.Errorf("%s: refusal %q should say commitment, never note", name, re.Msg)
		}
	}
	if after := len(lines(t, home, "home")); after != before {
		t.Errorf("ledger has %d lines after the refusals, want the %d it had", after, before)
	}
}

func TestACommitmentOfExactlyTheLimitIsStored(t *testing.T) {
	l, _ := newLedger(t)
	mustCommit(t, l, "home", remind(strings.Repeat("é", MaxNoteChars), Trigger{Kind: TriggerNone}), "")
}

func TestAnUnknownTriggerOrAnOversizeLaunchSpecIsRefused(t *testing.T) {
	l, home := newLedger(t)
	big := `{"text":"` + strings.Repeat("x", MaxLaunchBytes) + `"}`
	for name, c := range map[string]Commitment{
		"unknown trigger": remind("x", Trigger{Kind: "sunrise"}),
		"unknown action":  {Text: "x", Trigger: Trigger{Kind: TriggerNone}, Action: "email"},
		"oversize launch": {Text: "x", Trigger: Trigger{Kind: TriggerNone}, Action: ActionLaunch, Launch: json.RawMessage(big)},
	} {
		added, err := l.AddCommitment("home", c, "")
		var re *RefusedError
		if !errors.As(err, &re) || added.ID != "" {
			t.Errorf("%s: AddCommitment = %+v, %v; want a RefusedError", name, added, err)
		}
	}
	if got := lines(t, home, "home"); len(got) != 0 {
		t.Errorf("lines = %q, want nothing stored", got)
	}
}

func TestTheThirtyFirstOpenCommitmentIsRefusedWithAReason(t *testing.T) {
	l, home := newLedger(t)
	seed(t, home, "home", rawCommitments(MaxCommitments))
	added, err := l.AddCommitment("home", remind("one too many", Trigger{Kind: TriggerNone}), "")
	var re *RefusedError
	if !errors.As(err, &re) || added.ID != "" {
		t.Fatalf("AddCommitment = %+v, %v; want a RefusedError", added, err)
	}
	if !strings.Contains(re.Msg, "30") || !strings.Contains(re.Msg, "commitment") {
		t.Errorf("refusal %q should say the cap, 30, and commitments", re.Msg)
	}
	if got := lines(t, home, "home"); len(got) != MaxCommitments {
		t.Errorf("ledger has %d lines, want the %d seeded", len(got), MaxCommitments)
	}
}

func TestWaitingCommitmentsCountTowardTheCapAndClosedOnesDoNot(t *testing.T) {
	l, home := newLedger(t)
	seed(t, home, "home", rawCommitments(MaxCommitments-1)+rawAdded("w1", "waiting one")+rawEvent("fired", "w1", ""))
	if _, err := l.AddCommitment("home", remind("one too many", Trigger{Kind: TriggerNone}), ""); err == nil {
		t.Error("a waiting commitment did not count toward the cap")
	}
	seed(t, home, "home", rawCommitments(MaxCommitments-1)+rawAdded("d1", "done one")+rawEvent("done", "d1", "fired"))
	mustCommit(t, l, "home", remind("room again", Trigger{Kind: TriggerNone}), "")
	seed(t, home, "home", rawCommitments(MaxCommitments)+rawEvent("cancelled", "c00", ""))
	mustCommit(t, l, "home", remind("room after a cancel", Trigger{Kind: TriggerNone}), "")
}

func TestThirtyOpenCommitmentsDoNotBlockANote(t *testing.T) {
	l, home := newLedger(t)
	seed(t, home, "home", rawCommitments(MaxCommitments))
	if e, err := l.AddNote("home", "a note", ""); err != nil || e.ID == "" {
		t.Fatalf("AddNote with %d open commitments = %+v, %v; want it stored", MaxCommitments, e, err)
	}
}

func TestThirtyOpenNotesDoNotBlockACommitment(t *testing.T) {
	l, _ := newLedger(t)
	for i := 0; i < MaxNotes; i++ {
		mustAdd(t, l, "home", fmt.Sprintf("note %d", i), "")
	}
	mustCommit(t, l, "home", remind("still fits", Trigger{Kind: TriggerNone}), "")
	if _, err := l.AddNote("home", "the 31st note", ""); err == nil {
		t.Error("the 31st note was stored: the note cap is gone")
	}
}

func TestCommitmentsAreListedPerFleetAndNotesStayNotes(t *testing.T) {
	l, _ := newLedger(t)
	n := mustAdd(t, l, "home", "a note", "")
	a := mustCommit(t, l, "home", remind("first", Trigger{Kind: TriggerNone}), "")
	b := mustCommit(t, l, "home", remind("second", Trigger{Kind: TriggerNone}), "")
	other := mustCommit(t, l, "work", remind("elsewhere", Trigger{Kind: TriggerNone}), "")
	if got := openIDs(t, l, "home"); !reflect.DeepEqual(got, []string{a.ID, b.ID}) {
		t.Errorf("home commitments = %v, want %v oldest first and no note", got, []string{a.ID, b.ID})
	}
	if got := openIDs(t, l, "work"); !reflect.DeepEqual(got, []string{other.ID}) {
		t.Errorf("work commitments = %v, want only its own", got)
	}
	notes, err := l.Notes("home")
	if err != nil || len(notes) != 1 || notes[0].ID != n.ID {
		t.Errorf("Notes = %+v, %v; want only the note", notes, err)
	}
}

func TestFiredMakesALaunchCommitmentWaitingAndItStaysOpen(t *testing.T) {
	l, _ := newLedger(t)
	c := mustCommit(t, l, "home", Commitment{Text: "launch the fix", Trigger: Trigger{Kind: TriggerNone}, Action: ActionLaunch, Launch: json.RawMessage(launchSpec)}, "admiralbot")
	if err := l.MarkFired("home", c.ID); err != nil {
		t.Fatal(err)
	}
	got, ok := find(t, l, "home", c.ID)
	if !ok || !got.Waiting {
		t.Errorf("after fired: %+v, %v; want it listed and waiting", got, ok)
	}
}

func TestDoneAndDroppedCloseACommitmentOpenOrWaiting(t *testing.T) {
	for _, closer := range []struct {
		name string
		do   func(l *Ledger, fleet, id string) error
	}{
		{"done", func(l *Ledger, f, id string) error { return l.Done(f, id, "it fired") }},
		{"dropped", func(l *Ledger, f, id string) error { return l.Drop(f, id, "the PR closed without merging") }},
		{"cancelled", func(l *Ledger, f, id string) error { return l.CancelCommitment(f, id) }},
	} {
		for _, waiting := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/waiting=%v", closer.name, waiting), func(t *testing.T) {
				l, _ := newLedger(t)
				c := mustCommit(t, l, "home", Commitment{Text: "x", Trigger: Trigger{Kind: TriggerNone}, Action: ActionLaunch, Launch: json.RawMessage(launchSpec)}, "")
				keep := mustCommit(t, l, "home", remind("stays", Trigger{Kind: TriggerNone}), "")
				if waiting {
					if err := l.MarkFired("home", c.ID); err != nil {
						t.Fatal(err)
					}
				}
				if err := closer.do(l, "home", c.ID); err != nil {
					t.Fatalf("%s: %v", closer.name, err)
				}
				if got := openIDs(t, l, "home"); !reflect.DeepEqual(got, []string{keep.ID}) {
					t.Errorf("open after %s = %v, want only %s", closer.name, got, keep.ID)
				}
			})
		}
	}
}

func TestDoneAndDroppedWriteTheirReasonIntoTheEvent(t *testing.T) {
	for event, do := range map[string]func(l *Ledger, id string) error{
		"done": func(l *Ledger, id string) error {
			return l.Done("home", id, "the dock was removed, so it became a reminder")
		},
		"dropped": func(l *Ledger, id string) error { return l.Drop("home", id, "the PR closed without merging") },
	} {
		l, home := newLedger(t)
		c := mustCommit(t, l, "home", remind("x", Trigger{Kind: TriggerNone}), "")
		if err := do(l, c.ID); err != nil {
			t.Fatalf("%s: %v", event, err)
		}
		got := lines(t, home, "home")
		if len(got) != 2 {
			t.Fatalf("%s: lines = %q, want the add and the close", event, got)
		}
		var ev map[string]any
		if err := json.Unmarshal([]byte(got[1]), &ev); err != nil {
			t.Fatal(err)
		}
		if ev["event"] != event || ev["id"] != c.ID || ev["reason"] == nil || ev["reason"] == "" {
			t.Errorf("%s: line %s, want event %s for %s with a reason", event, got[1], event, c.ID)
		}
	}
}

func TestACancelledCommitmentWritesACancelledEventWithNoReason(t *testing.T) {
	l, home := newLedger(t)
	c := mustCommit(t, l, "home", remind("x", Trigger{Kind: TriggerNone}), "")
	if err := l.CancelCommitment("home", c.ID); err != nil {
		t.Fatal(err)
	}
	got := lines(t, home, "home")
	if len(got) != 2 || !strings.Contains(got[1], `"event":"cancelled"`) || !strings.Contains(got[1], `"id":"`+c.ID+`"`) {
		t.Errorf("lines = %q, want the add then a cancelled event for %s", got, c.ID)
	}
}

func TestAClosedCommitmentNeverChangesAgain(t *testing.T) {
	for _, closing := range []string{rawEvent("done", "x1", "it fired"), rawEvent("dropped", "x1", "the PR closed"), rawEvent("cancelled", "x1", "")} {
		for name, change := range map[string]func(l *Ledger) error{
			"fired":     func(l *Ledger) error { return l.MarkFired("home", "x1") },
			"done":      func(l *Ledger) error { return l.Done("home", "x1", "again") },
			"dropped":   func(l *Ledger) error { return l.Drop("home", "x1", "again") },
			"shown":     func(l *Ledger) error { return l.MarkShown("home", "x1") },
			"cancelled": func(l *Ledger) error { return l.CancelCommitment("home", "x1") },
		} {
			l, home := newLedger(t)
			seed(t, home, "home", rawAdded("x1", "closed one")+closing)
			before := lines(t, home, "home")
			if err := change(l); !errors.Is(err, ErrNotFound) {
				t.Errorf("%s after %s: err = %v, want ErrNotFound", name, strings.TrimSpace(closing), err)
			}
			if after := lines(t, home, "home"); !reflect.DeepEqual(before, after) {
				t.Errorf("%s after a close appended a line: %q", name, after)
			}
		}
	}
}

func TestAnUnknownCommitmentIdIsNotFoundForEveryEvent(t *testing.T) {
	l, home := newLedger(t)
	seed(t, home, "home", rawAdded("known", "x"))
	for name, err := range map[string]error{
		"fired":     l.MarkFired("home", "nope"),
		"done":      l.Done("home", "nope", "r"),
		"dropped":   l.Drop("home", "nope", "r"),
		"shown":     l.MarkShown("home", "nope"),
		"cancelled": l.CancelCommitment("home", "nope"),
		"no fleet":  l.CancelCommitment("work", "known"),
	} {
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("%s of an unknown id: err = %v, want ErrNotFound", name, err)
		}
	}
}

func TestANoteIdIsNotACommitmentAndACommitmentIdIsNotANote(t *testing.T) {
	l, _ := newLedger(t)
	n := mustAdd(t, l, "home", "a note", "")
	c := mustCommit(t, l, "home", remind("a promise", Trigger{Kind: TriggerNone}), "")
	if err := l.CancelCommitment("home", n.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("CancelCommitment(note id) = %v, want ErrNotFound", err)
	}
	if err := l.Cancel("home", c.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Cancel(commitment id) = %v, want ErrNotFound", err)
	}
	if _, err := l.Promote("home", c.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Promote(commitment id) = %v, want ErrNotFound", err)
	}
	if got := openIDs(t, l, "home"); !reflect.DeepEqual(got, []string{c.ID}) {
		t.Errorf("commitments = %v, want the commitment untouched", got)
	}
	if notes, _ := l.Notes("home"); len(notes) != 1 {
		t.Errorf("notes = %+v, want the note untouched", notes)
	}
}

func TestTheStillWantThisMarkSticksWithoutClosingTheCommitment(t *testing.T) {
	l, _ := newLedger(t)
	c := mustCommit(t, l, "home", remind("someday", Trigger{Kind: TriggerNone}), "")
	if got, _ := find(t, l, "home", c.ID); got.Shown {
		t.Fatalf("a new commitment is already marked shown: %+v", got)
	}
	if err := l.MarkShown("home", c.ID); err != nil {
		t.Fatal(err)
	}
	got, ok := find(t, l, "home", c.ID)
	if !ok || !got.Shown {
		t.Fatalf("after the mark: %+v, %v; want it open and marked shown", got, ok)
	}
	if err := l.MarkShown("home", c.ID); err != nil {
		t.Errorf("marking twice: %v, want it harmless", err)
	}
	if got, ok := find(t, l, "home", c.ID); !ok || !got.Shown {
		t.Errorf("after marking twice: %+v, %v", got, ok)
	}
}

func TestTheShownMarkSurvivesAFreshLedgerOverTheSameFile(t *testing.T) {
	l, home := newLedger(t)
	c := mustCommit(t, l, "home", remind("someday", Trigger{Kind: TriggerNone}), "")
	if err := l.MarkShown("home", c.ID); err != nil {
		t.Fatal(err)
	}
	again := New(home, l.now)
	if got, ok := find(t, again, "home", c.ID); !ok || !got.Shown {
		t.Errorf("a new ledger read %+v, %v; want the mark kept in the file", got, ok)
	}
}

func TestACommitmentEventForANoteIdLeavesTheNoteOpen(t *testing.T) {
	l, home := newLedger(t)
	n := mustAdd(t, l, "home", "a note", "")
	seed(t, home, "home", strings.Join(lines(t, home, "home"), "\n")+"\n"+rawEvent("done", n.ID, "x")+rawEvent("dropped", n.ID, "x"))
	if notes, err := l.Notes("home"); err != nil || len(notes) != 1 {
		t.Errorf("Notes = %+v, %v; want the note still open", notes, err)
	}
}

func TestAFiredEventForARemindCommitmentIsRefused(t *testing.T) {
	l, _ := newLedger(t)
	c := mustCommit(t, l, "home", Commitment{Text: "tell me", Trigger: Trigger{Kind: TriggerNone}, Action: ActionRemind}, "admiralbot")
	if err := l.MarkFired("home", c.ID); err == nil {
		t.Fatal("MarkFired on a remind commitment = nil, want a refusal")
	}
	if got, ok := find(t, l, "home", c.ID); !ok || got.Waiting {
		t.Errorf("after the refusal: %+v, %v; want it open and not waiting", got, ok)
	}
}
