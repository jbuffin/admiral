package keymap

import (
	"regexp"
	"strconv"
	"strings"
)

// Step is keys for the menu and what the pane must show once it has taken them; a nil Done waits for nothing.
type Step struct {
	Keys string
	Done func(screen string) bool
	Want string
}

// Plan answers a call one step at a time: Fresh says the menu is untouched, each step's Done says the pane followed, and
// Review says the review screen holds exactly the intended answers before Submit is sent. Without Review the last step submits.
type Plan struct {
	Fresh  func(screen string) bool
	Steps  []Step
	Review func(screen string) bool
	Submit Step
}

const down = "\x1b[B"

// claudePlan steps through Claude's question tabs, each step waiting for the pane because keys sent faster than a tab redraws are lost.
// A multi-select moves with arrows (digits toggle but never move the cursor); a call that is not one single-choice question ends on a review screen.
func claudePlan(qs []CallQuestion) (Plan, bool) {
	if len(qs) < 1 || len(qs) > 4 {
		return Plan{}, false
	}
	review := len(qs) > 1 || qs[0].Multi
	var p Plan
	p.Fresh = fresh(qs)
	for i, q := range qs {
		n := len(q.Labels)
		if n < 1 || n > 8 || strings.TrimSpace(q.Text) == "" {
			return Plan{}, false
		}
		// What the pane shows after this question is answered.
		after := func(string) bool { return true }
		if i < len(qs)-1 {
			after = onQuestion(qs[i+1], i+1)
		} else if review {
			after = onReview
		}
		if !q.Multi {
			switch {
			case len(q.Choose) == 1 && q.Other == "" && q.Choose[0] >= 1 && q.Choose[0] <= n:
				p.Steps = append(p.Steps, Step{Keys: strconv.Itoa(q.Choose[0]), Done: doneUnless(after, !review && i == len(qs)-1), Want: "the next screen"})
			case len(q.Choose) == 0 && q.Other != "":
				p.Steps = append(p.Steps,
					Step{Keys: strconv.Itoa(n + 1), Done: cursorOn(n + 1), Want: "the cursor on the Other row"},
					Step{Keys: q.Other, Done: shows(q.Other), Want: "the Other text"},
					Step{Keys: "\r", Done: doneUnless(after, !review && i == len(qs)-1), Want: "the next screen"})
			default:
				return Plan{}, false
			}
			continue
		}
		if len(q.Choose) == 0 && q.Other == "" {
			return Plan{}, false
		}
		row, prev := 1, 0
		for _, c := range q.Choose {
			if c <= prev || c > n {
				return Plan{}, false
			}
			prev = c
			if c > row {
				p.Steps = append(p.Steps, Step{Keys: strings.Repeat(down, c-row), Done: cursorOn(c), Want: "the cursor on option " + strconv.Itoa(c)})
			}
			p.Steps = append(p.Steps, Step{Keys: "\r", Done: ticked(c), Want: "option " + strconv.Itoa(c) + " ticked"})
			row = c
		}
		if q.Other != "" {
			p.Steps = append(p.Steps,
				Step{Keys: strings.Repeat(down, n+1-row), Done: cursorOn(n + 1), Want: "the cursor on the Other row"},
				Step{Keys: q.Other, Done: shows(q.Other), Want: "the Other text"})
			row = n + 1
		}
		p.Steps = append(p.Steps,
			Step{Keys: strings.Repeat(down, n+2-row), Done: onSubmitRow, Want: "the cursor on the row that moves on"},
			Step{Keys: "\r", Done: after, Want: "the next screen"})
	}
	if review {
		p.Review = reviewShows(qs)
		p.Submit = Step{Keys: "1", Done: func(s string) bool { return !onReview(s) }, Want: "the answers submitted"}
	}
	return p, true
}

// doneUnless leaves a question's step unchecked when nothing follows it: a lone single-choice answer has no next screen.
func doneUnless(after func(string) bool, last bool) func(string) bool {
	if last {
		return nil
	}
	return after
}

var (
	cursorRow = func(n int) *regexp.Regexp { return regexp.MustCompile(`(?m)^❯ +` + strconv.Itoa(n) + `\. `) }
	// Claude labels the row under a multi-select Next on every tab but the last, where it says Submit.
	submitRow = regexp.MustCompile(`(?m)^❯ +(Next|Submit)\s*$`)
)

func compact(s string) string { return strings.Join(strings.Fields(s), "") }

// menu is the part of the screen from the tab line down, which is where Claude draws the question; ok is false when no menu is shown.
func menu(screen string) (tabs, rest string, ok bool) {
	lines := strings.Split(screen, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		t := strings.TrimSpace(lines[i])
		if strings.HasPrefix(t, "←") || strings.HasPrefix(t, "☐") || strings.HasPrefix(t, "☒") {
			return t, strings.Join(lines[i+1:], "\n"), true
		}
	}
	return "", "", false
}

func fresh(qs []CallQuestion) func(string) bool {
	return func(screen string) bool {
		tabs, rest, ok := menu(screen)
		return ok && strings.Count(tabs, "☒") == 0 && strings.Count(tabs, "☐") == len(qs) &&
			strings.Contains(compact(rest), compact(qs[0].Text)) && cursorRow(1).MatchString(rest) && !strings.Contains(rest, "✔")
	}
}

// onQuestion is the menu of question i with the questions before it answered and its own cursor on row 1.
func onQuestion(q CallQuestion, i int) func(string) bool {
	return func(screen string) bool {
		tabs, rest, ok := menu(screen)
		return ok && strings.Count(tabs, "☒") == i && strings.Contains(compact(rest), compact(q.Text)) && cursorRow(1).MatchString(rest)
	}
}

func cursorOn(n int) func(string) bool {
	return func(screen string) bool { _, rest, ok := menu(screen); return ok && cursorRow(n).MatchString(rest) }
}

func ticked(n int) func(string) bool {
	re := regexp.MustCompile(`(?m)^[❯ ] +` + strconv.Itoa(n) + `\. \[✔\]`)
	return func(screen string) bool { _, rest, ok := menu(screen); return ok && re.MatchString(rest) }
}

func shows(text string) func(string) bool {
	return func(screen string) bool {
		_, rest, ok := menu(screen)
		return ok && strings.Contains(compact(rest), compact(text))
	}
}

func onSubmitRow(screen string) bool {
	_, rest, ok := menu(screen)
	return ok && submitRow.MatchString(rest)
}

func onReview(screen string) bool { return strings.Contains(compact(screen), "Reviewyouranswers") }

// reviewShows is true when the review screen lists every question with exactly the intended answer, in order.
func reviewShows(qs []CallQuestion) func(string) bool {
	var want strings.Builder
	for _, q := range qs {
		var parts []string
		for _, c := range q.Choose {
			parts = append(parts, q.Labels[c-1])
		}
		if q.Other != "" {
			parts = append(parts, q.Other)
		}
		want.WriteString("●" + compact(q.Text) + "→" + compact(strings.Join(parts, ", ")))
	}
	return func(screen string) bool { return onReview(screen) && strings.Contains(compact(screen), want.String()) }
}
