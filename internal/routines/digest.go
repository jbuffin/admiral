package routines

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jbuffin/admiral/internal/sessionhost"
	"github.com/jbuffin/admiral/internal/textsafe"
)

// FactSection is one titled group of facts; a section with no lines is left out of the digest.
type FactSection struct {
	Title string
	Lines []string
}

// DigestFacts gathers what changed in a fleet in the window (from, to] for one routine. The scheduler adds the routine
// runs itself. commit keeps what the facts were read against for this routine's next window; the scheduler calls it only
// once the run is recorded Queued or nothing new, so a failed run consumes nothing.
type DigestFacts interface {
	Facts(fleet, routine string, from, to time.Time) (sections []FactSection, commit func(), err error)
}

const (
	// ReasonNothingNew is the reason of the Skipped record a digest run writes when no section holds a line.
	ReasonNothingNew = "nothing new"
	// maxSectionLines and maxFactRunes bound each section and line; maxFactBytes bounds the whole fact sheet.
	maxSectionLines = 25
	maxFactRunes    = 240
	maxFactBytes    = 10 * 1024
	// factSlack is room kept for section titles and "and N more" lines past the facts' own budget.
	factSlack = 512
)

// fold drops control and invisible characters and folds the rest onto one line, so text can't fake a marker line.
func fold(s string) string {
	s = strings.Map(func(r rune) rune {
		if textsafe.Hidden(r) {
			return ' '
		}
		return r
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

// oneLine is a fact folded onto one line and cut at maxFactRunes.
func oneLine(s string) string {
	s = fold(s)
	if utf8.RuneCountInString(s) > maxFactRunes {
		s = string([]rune(s)[:maxFactRunes]) + "…"
	}
	return s
}

// digestStart is where the window opens: the last run of this routine that queued a turn or found nothing new, else its creation.
func (s *Scheduler) digestStart(fleet string, r Routine) time.Time {
	recs, err := s.Store.Recent(fleet, r.ID, 0)
	if err == nil {
		for _, rec := range recs {
			if rec.Outcome == Queued || (rec.Outcome == Skipped && rec.Reason == ReasonNothingNew) {
				return rec.At
			}
		}
	}
	return r.CreatedAt
}

// routineRuns lists the other routines' runs and proposals of the fleet in the window.
func (s *Scheduler) routineRuns(fleet string, self Routine, from, to time.Time) (FactSection, error) {
	list, err := s.Store.List(fleet)
	if err != nil {
		return FactSection{}, err
	}
	names := map[string]string{}
	for _, r := range list {
		names[r.ID] = r.Name
	}
	recs, err := s.Store.Records(fleet)
	if err != nil {
		return FactSection{}, err
	}
	var lines []string
	for i := len(recs) - 1; i >= 0; i-- { // Records is newest first
		rec := recs[i]
		if rec.Routine == self.ID || !rec.At.After(from) || rec.At.After(to) {
			continue
		}
		name, ok := names[rec.Routine]
		if !ok {
			continue
		}
		switch rec.Outcome {
		case Launched, Proposed, Failed, Reported:
		default:
			continue
		}
		line := fmt.Sprintf("%s: %s", name, rec.Outcome)
		if rec.Action != "" {
			line += " (" + rec.Action + ")"
		}
		if rec.Reason != "" {
			line += ": " + rec.Reason
		}
		lines = append(lines, line)
	}
	return FactSection{Title: "Routine runs and proposals", Lines: lines}, nil
}

// digestText is the turn: its marker line, the turn id line the chat matches the pasted turn by, what AdmiralBot is to do, the capped fact sheet, then the engineer's extra words.
func digestText(r Routine, turn string, from, to time.Time, sections []FactSection) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[Digest: %s]\nturn: %s\n", r.Name, turn)
	b.WriteString("Write the engineer a short summary of the facts below, with links where a fact has one. " +
		"Don't repeat what a ship's report already told them. You may offer follow-up work, " +
		"but do nothing until the engineer answers: every write you make in this turn is refused.\n")
	fmt.Fprintf(&b, "Window: %s to %s\n", from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339))
	tail := ""
	if d := r.Do.Digest; d != nil {
		if extra := fold(d.Extra); extra != "" {
			tail = "\nExtra words from the engineer: " + extra + "\n"
		}
	}
	// the facts give way to the extra words, so the whole turn fits one paste
	budget := min(maxFactBytes, sessionhost.MaxPaste-b.Len()-len(tail)-factSlack)
	used := 0
	for _, sec := range sections {
		if len(sec.Lines) == 0 {
			continue
		}
		b.WriteString("\n" + oneLine(sec.Title) + ":\n")
		shown := 0
		for _, l := range sec.Lines {
			l = "- " + oneLine(l)
			if shown == maxSectionLines || used+len(l) > budget {
				break
			}
			b.WriteString(l + "\n")
			used += len(l)
			shown++
		}
		if more := len(sec.Lines) - shown; more > 0 {
			fmt.Fprintf(&b, "- and %d more\n", more)
		}
	}
	b.WriteString(tail)
	return b.String()
}

// digest runs one digest: it gathers the facts and queues a turn on the fleet's report-turn queue, or records that nothing is new.
func (s *Scheduler) digest(fleet string, r Routine, rec Trigger) error {
	delete(s.waiting, r.ID)
	fail := func(reason string) error {
		rec.Outcome, rec.Reason = Failed, reason
		s.recordFor(fleet, r, rec)
		return fmt.Errorf("%s", reason)
	}
	// a queued turn is delivered only while the chat is on, so a digest for a fleet with it off would sit there unread
	if !s.Chat.ChatOn(fleet) {
		return fail("the fleet's chat is off")
	}
	if s.Facts == nil {
		return fail("no fact source")
	}
	from, to := s.digestStart(fleet, r), rec.At
	sections, commit, err := s.Facts.Facts(fleet, r.ID, from, to)
	if err != nil {
		return fail(err.Error())
	}
	runs, err := s.routineRuns(fleet, r, from, to)
	if err != nil {
		return fail(err.Error())
	}
	sections = append(sections, runs)
	var lit []FactSection
	for _, sec := range sections {
		if len(sec.Lines) > 0 {
			lit = append(lit, sec)
		}
	}
	if len(lit) == 0 {
		rec.Outcome, rec.Reason = Skipped, ReasonNothingNew
		s.recordFor(fleet, r, rec)
		commit()
		return nil
	}
	id := newID()
	turn := "digest:" + r.ID + ":" + id
	if err := s.Chat.EnqueueTurn(fleet, turn, digestText(r, turn, from, to, lit)); err != nil {
		return fail(err.Error())
	}
	rec.ID, rec.Outcome = id, Queued
	s.recordFor(fleet, r, rec)
	commit()
	return nil
}
