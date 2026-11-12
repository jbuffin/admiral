package keymap_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/keymap"
)

// menuModel is Claude's AskUserQuestion menu as the recordings show it: a tab per question, option rows, an Other row
// that takes typed text (and, on a multi-select, ticks), a Submit row under a multi-select, and a review screen.
type menuModel struct {
	t         *testing.T
	qs        []keymap.CallQuestion
	tab       int
	cur       []int
	ticked    [][]bool
	other     []string
	answered  []bool
	review    bool
	submitted bool
}

func newMenu(t *testing.T, qs []keymap.CallQuestion) *menuModel {
	m := &menuModel{t: t, qs: qs, cur: make([]int, len(qs)), ticked: make([][]bool, len(qs)), other: make([]string, len(qs)), answered: make([]bool, len(qs))}
	for i, q := range qs {
		m.ticked[i] = make([]bool, len(q.Labels))
	}
	return m
}

func (m *menuModel) rows(i int) int {
	if m.qs[i].Multi {
		return len(m.qs[i].Labels) + 2
	}
	return len(m.qs[i].Labels) + 1
}

func (m *menuModel) answer(i int) string {
	var parts []string
	for j, on := range m.ticked[i] {
		if on {
			parts = append(parts, m.qs[i].Labels[j])
		}
	}
	if m.other[i] != "" {
		parts = append(parts, m.other[i])
	}
	return strings.Join(parts, ", ")
}

func (m *menuModel) next() {
	m.answered[m.tab] = true
	if m.tab < len(m.qs)-1 {
		m.tab++
	} else if len(m.qs) > 1 || m.qs[0].Multi {
		m.review = true
	} else {
		m.submitted = true
	}
}

func (m *menuModel) press(keys string) {
	for rest := keys; rest != ""; {
		if m.review {
			if rest[0] == '1' {
				m.submitted, m.review = true, false
			}
			rest = rest[1:]
			continue
		}
		i := m.tab
		n := len(m.qs[i].Labels)
		onOther := m.cur[i] == n
		switch {
		case strings.HasPrefix(rest, "\x1b[B"):
			m.cur[i] = (m.cur[i] + 1) % m.rows(i)
			rest = rest[3:]
			continue
		case rest[0] == '\r':
			switch {
			case m.qs[i].Multi && m.cur[i] == n+1:
				m.next()
			case m.qs[i].Multi && m.cur[i] < n:
				m.ticked[i][m.cur[i]] = !m.ticked[i][m.cur[i]]
			case !m.qs[i].Multi && onOther && m.other[i] != "":
				m.next()
			case !m.qs[i].Multi && !onOther:
				m.ticked[i][m.cur[i]] = true
				m.next()
			}
		case onOther:
			m.other[i] += rest[:1]
		case !m.qs[i].Multi && rest[0] >= '1' && int(rest[0]-'0') <= n+1:
			d := int(rest[0] - '0')
			if d == n+1 {
				m.cur[i] = n
			} else {
				m.ticked[i][d-1] = true
				m.next()
			}
		}
		rest = rest[1:]
	}
}

func (m *menuModel) screen() string {
	var b strings.Builder
	b.WriteString("❯ earlier prompt\n────────\n")
	if m.submitted {
		b.WriteString("⏺ User answered Claude's questions:\n")
		return b.String()
	}
	if m.review {
		var tabs []string
		for range m.qs {
			tabs = append(tabs, "☒ Q")
		}
		b.WriteString("←  " + strings.Join(tabs, "  ") + "  ✔ Submit  →\n\nReview your answers\n\n")
		for i, q := range m.qs {
			fmt.Fprintf(&b, " ● %s\n   → %s\n", q.Text, m.answer(i))
		}
		b.WriteString("\nReady to submit your answers?\n\n❯ 1. Submit answers\n  2. Cancel\n")
		return b.String()
	}
	if len(m.qs) == 1 && !m.qs[0].Multi {
		b.WriteString(" ☐ Q\n\n")
	} else {
		var tabs []string
		for i := range m.qs {
			mark := "☐"
			if m.answered[i] {
				mark = "☒"
			}
			tabs = append(tabs, mark+" Q")
		}
		b.WriteString("←  " + strings.Join(tabs, "  ") + "  ✔ Submit  →\n\n")
	}
	i := m.tab
	q := m.qs[i]
	b.WriteString(q.Text + "\n\n")
	cursor := func(row int) string {
		if m.cur[i] == row {
			return "❯"
		}
		return " "
	}
	for j, l := range q.Labels {
		switch {
		case q.Multi && m.ticked[i][j]:
			fmt.Fprintf(&b, "%s %d. [✔] %s\n         a description\n", cursor(j), j+1, l)
		case q.Multi:
			fmt.Fprintf(&b, "%s %d. [ ] %s\n         a description\n", cursor(j), j+1, l)
		case m.ticked[i][j]:
			fmt.Fprintf(&b, "%s %d. %s ✔\n     a description\n", cursor(j), j+1, l)
		default:
			fmt.Fprintf(&b, "%s %d. %s\n     a description\n", cursor(j), j+1, l)
		}
	}
	n := len(q.Labels)
	switch {
	case q.Multi && m.other[i] != "":
		fmt.Fprintf(&b, "%s %d. [✔] %s\n", cursor(n), n+1, m.other[i])
	case q.Multi:
		fmt.Fprintf(&b, "%s %d. [ ] Type something\n", cursor(n), n+1)
	case m.other[i] != "":
		fmt.Fprintf(&b, "%s %d. %s\n", cursor(n), n+1, m.other[i])
	default:
		fmt.Fprintf(&b, "%s %d. Type something.\n", cursor(n), n+1)
	}
	if q.Multi {
		row := "Next"
		if i == len(m.qs)-1 {
			row = "Submit"
		}
		fmt.Fprintf(&b, "%s    %s\n", cursor(n+1), row)
	}
	b.WriteString("────────\n  Chat about this\n\nEnter to select\n")
	return b.String()
}

func claudeMap(t *testing.T) keymap.Map {
	m, ok := keymap.For("claude")
	if !ok {
		t.Fatal("no key map for claude")
	}
	return m
}

// play drives a plan against the model the way the server does: every step must show its result before the next is sent.
func play(t *testing.T, qs []keymap.CallQuestion) *menuModel {
	t.Helper()
	plan, ok := claudeMap(t).Plan(qs)
	if !ok {
		t.Fatal("the key map has no plan for this call")
	}
	m := newMenu(t, qs)
	if !plan.Fresh(m.screen()) {
		t.Fatalf("a fresh menu is not seen as fresh:\n%s", m.screen())
	}
	for _, st := range plan.Steps {
		m.press(st.Keys)
		if !st.Done(m.screen()) {
			t.Fatalf("after %q the pane does not show %s:\n%s", st.Keys, st.Want, m.screen())
		}
	}
	if plan.Review != nil {
		if !plan.Review(m.screen()) {
			t.Fatalf("the review screen is not the intended answers:\n%s", m.screen())
		}
		m.press(plan.Submit.Keys)
		if !plan.Submit.Done(m.screen()) {
			t.Fatalf("the menu is still on review after Submit:\n%s", m.screen())
		}
	}
	return m
}

func labels(ls ...string) []string { return ls }

func TestClaudeCallPlanReplaysOnTheMenuModelAndEndsOnTheIntendedReview(t *testing.T) {
	colour := keymap.CallQuestion{Text: "Which colour?", Labels: labels("Red", "Green", "Blue"), Choose: []int{3}}
	toppings := keymap.CallQuestion{Text: "Which toppings?", Labels: labels("Cheese", "Ham", "Olives"), Multi: true, Choose: []int{1, 3}, Other: "Pine 2"}
	size := keymap.CallQuestion{Text: "What size?", Labels: labels("Small", "Large"), Other: "2 big slices, thanks"}
	how := keymap.CallQuestion{Text: "Pickup or courier?", Labels: labels("Pickup", "Courier"), Choose: []int{2}}
	m := play(t, []keymap.CallQuestion{colour, toppings, size, how})
	for i, want := range []string{"Blue", "Cheese, Olives, Pine 2", "2 big slices, thanks", "Courier"} {
		if got := m.answer(i); got != want {
			t.Errorf("answer %d = %q, want %q", i+1, got, want)
		}
	}
	if !m.submitted {
		t.Error("the call was not submitted")
	}
	if m := play(t, []keymap.CallQuestion{{Text: "Which toppings?", Labels: labels("Cheese", "Ham", "Olives"), Multi: true, Choose: []int{1, 3}}}); m.answer(0) != "Cheese, Olives" || !m.submitted {
		t.Errorf("a single multi-select ended on %q, submitted %v", m.answer(0), m.submitted)
	}
	if m := play(t, []keymap.CallQuestion{{Text: "Which colour?", Labels: labels("Red", "Green", "Blue"), Other: "Purple"}}); m.answer(0) != "Purple" || !m.submitted {
		t.Errorf("a single Other ended on %q, submitted %v", m.answer(0), m.submitted)
	}
	if m := play(t, []keymap.CallQuestion{{Text: "Which colour?", Labels: labels("Red", "Green", "Blue"), Choose: []int{2}}}); m.answer(0) != "Green" || !m.submitted {
		t.Errorf("a single digit ended on %q, submitted %v", m.answer(0), m.submitted)
	}
}

func TestClaudeCallPlanRefusesWhatTheMenuHasNoKeysFor(t *testing.T) {
	q := func(mod func(*keymap.CallQuestion)) []keymap.CallQuestion {
		c := keymap.CallQuestion{Text: "Which?", Labels: labels("A", "B", "C")}
		mod(&c)
		return []keymap.CallQuestion{c}
	}
	for name, qs := range map[string][]keymap.CallQuestion{
		"no answer":              q(func(c *keymap.CallQuestion) { c.Multi = true }),
		"two for a single":       q(func(c *keymap.CallQuestion) { c.Choose = []int{1, 2} }),
		"options out of order":   q(func(c *keymap.CallQuestion) { c.Multi, c.Choose = true, []int{2, 1} }),
		"an option past the end": q(func(c *keymap.CallQuestion) { c.Multi, c.Choose = true, []int{4} }),
		"no question text":       q(func(c *keymap.CallQuestion) { c.Text, c.Choose = "", []int{1} }),
		"five questions":         make([]keymap.CallQuestion, 5),
	} {
		if _, ok := claudeMap(t).Plan(qs); ok {
			t.Errorf("%s gave a plan, want none", name)
		}
	}
	for _, h := range []string{"copilot", "codex"} {
		other, _ := keymap.For(h)
		if _, ok := other.Plan(q(func(c *keymap.CallQuestion) { c.Choose = []int{1} })); ok {
			t.Errorf("%s has a plan for a call, which no recording backs", h)
		}
	}
}

func TestClaudeCallPlanStopsWhenTheMenuIsNotFreshOrDoesNotFollow(t *testing.T) {
	qs := []keymap.CallQuestion{
		{Text: "Which colour?", Labels: labels("Red", "Green", "Blue"), Choose: []int{3}},
		{Text: "Which toppings?", Labels: labels("Cheese", "Ham", "Olives"), Multi: true, Choose: []int{1}},
	}
	plan, _ := claudeMap(t).Plan(qs)
	touched := newMenu(t, qs)
	touched.press("2")
	if plan.Fresh(touched.screen()) {
		t.Error("a menu already on its second question counts as fresh")
	}
	moved := newMenu(t, qs)
	moved.cur[0] = 1
	if plan.Fresh(moved.screen()) {
		t.Error("a menu with its cursor on row 2 counts as fresh")
	}
	lone := []keymap.CallQuestion{{Text: "Which colour?", Labels: labels("Red", "Green", "Blue"), Choose: []int{2}}}
	lonePlan, _ := claudeMap(t).Plan(lone)
	if last := lonePlan.Steps[len(lonePlan.Steps)-1]; last.Done(newMenu(t, lone).screen()) {
		t.Error("a lone single-choice menu still asking counts as answered: a lost key would report success")
	}
	wrong := newMenu(t, []keymap.CallQuestion{{Text: "Which colour?", Labels: labels("Red", "Green", "Blue"), Choose: []int{2}}, qs[1]})
	wrong.press("2\r")
	wrong.press("1")
	if plan.Review == nil || plan.Review(wrong.screen()) {
		t.Error("a review that lists other answers is accepted")
	}
}

// The plan's checks read the screens Claude Code drew in the recordings, not only the model's.
func TestClaudeCallPlanChecksReadTheRecordedScreens(t *testing.T) {
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join("testdata", "claude", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	colour := keymap.CallQuestion{Text: "Which colour?", Labels: labels("Red", "Green", "Blue"), Choose: []int{3}}
	toppings := keymap.CallQuestion{Text: "Which toppings?", Labels: labels("Cheese", "Ham", "Olives"), Multi: true, Choose: []int{1, 3}, Other: "Pine"}
	plan, _ := claudeMap(t).Plan([]keymap.CallQuestion{colour, toppings})
	if !plan.Fresh(read("batch-q1-menu.txt")) {
		t.Error("the recorded fresh first menu is not seen as fresh")
	}
	if plan.Fresh(read("batch-q2-multi-menu.txt")) {
		t.Error("the recorded second tab is seen as fresh")
	}
	steps := plan.Steps
	if !steps[0].Done(read("batch-q2-multi-menu.txt")) {
		t.Error("the recorded second tab does not satisfy the step after answering the first")
	}
	if !steps[len(steps)-1].Done(read("batch-review-arrows.txt")) {
		t.Error("the recorded review screen does not satisfy the last step")
	}
	if !plan.Review(read("batch-review-arrows.txt")) {
		t.Error("the recorded review of Blue and Cheese, Olives, Pine is not accepted")
	}
	if plan.Review(read("batch-review.txt")) {
		t.Error("the recorded review of Green and Cheese is accepted for Blue and Cheese, Olives, Pine")
	}
	if !plan.Submit.Done(read("after-batch-submit.txt")) {
		t.Error("the pane after Submit still reads as the review screen")
	}
}

// Two runs recorded on Claude Code 2.1.322, answered by the server's plan: a lone single-choice question answered with Other
// ends with no review screen, and Other text that starts with a digit and holds a space reaches Claude whole on both kinds.
func TestClaudeCallPlanOnRecordedOtherAnswersWithADigitAndASpace(t *testing.T) {
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join("testdata", "claude", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	single, _ := claudeMap(t).Plan([]keymap.CallQuestion{{Text: "Which size would you like?", Labels: labels("Small", "Large"), Other: "2 big slices"}})
	if !single.Fresh(read("rec-single-other-menu.txt")) || single.Review != nil {
		t.Error("the recorded lone single-choice menu is not fresh, or the plan expects a review screen it does not get")
	}
	if !strings.Contains(read("rec-single-other-after.txt"), "→ 2 big slices") {
		t.Error("the recorded run does not show Claude receiving the whole Other text")
	}
	multi, _ := claudeMap(t).Plan([]keymap.CallQuestion{{Text: "Which extras would you like?", Labels: labels("Cheese", "Olives"), Multi: true, Choose: []int{1}, Other: "2 big slices"}})
	if !multi.Fresh(read("rec-multi-other-menu.txt")) || multi.Review == nil {
		t.Error("the recorded lone multi-select menu is not fresh, or the plan expects no review screen, which a multi-select shows")
	}
	if !strings.Contains(read("rec-multi-other-after.txt"), "→ Cheese, 2 big slices") {
		t.Error("the recorded run does not show Claude receiving Cheese and the whole Other text")
	}
}

// Recorded on Claude Code 2.1.322: the menu draws question and label Markdown as typed, wraps a long Other text rather than
// cutting it, and keeps every tab's box on a 20-column pane, so the plan's checks hold on all three.
func TestClaudeCallPlanOnRecordedMarkdownLongOtherAndNarrowMenus(t *testing.T) {
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join("testdata", "claude", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	other := "-1 is fine,"
	for i := 1; i < 60; i++ {
		other += fmt.Sprintf(" word%d", i)
	}
	other += " end"
	size := keymap.CallQuestion{Text: "Which **size** fits `main.go`?", Labels: labels("**Small**", "Large [docs](https://example.com)"), Other: other}
	extras := keymap.CallQuestion{Text: "Pick _extras_ please", Labels: labels("Cheese", "`Ham`"), Multi: true, Choose: []int{2}}
	plan, _ := claudeMap(t).Plan([]keymap.CallQuestion{size, extras})
	if !plan.Fresh(read("rec-md-menu.txt")) {
		t.Error("the recorded menu with Markdown in its question and labels is not seen as fresh")
	}
	if typed := plan.Steps[1]; !typed.Done(read("rec-md-long-other-typed.txt")) {
		t.Errorf("the recorded long Other text does not satisfy the step that types it (%s)", typed.Want)
	}
	if !plan.Review(read("rec-md-review.txt")) {
		t.Error("the recorded review of the Markdown call with a long Other text is not accepted")
	}
	four := make([]keymap.CallQuestion, 4)
	for i, q := range []string{"Q one?", "Q two?", "Q three?", "Q four?"} {
		four[i] = keymap.CallQuestion{Text: q, Labels: labels("Alpha", "Beta"), Choose: []int{1}}
	}
	if plan, _ := claudeMap(t).Plan(four); !plan.Fresh(read("rec-four-tabs-20-cols.txt")) {
		t.Error("the recorded four-question menu on a 20-column pane is not seen as fresh")
	}
}
