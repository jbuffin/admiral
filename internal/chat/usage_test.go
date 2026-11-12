package chat

import (
	"testing"

	"github.com/jbuffin/admiral/internal/usage"
)

func est(amount float64) *usage.Money {
	return &usage.Money{Amount: amount, Basis: usage.BasisEstimate}
}

func fig(input int64, money float64) *usage.Figures {
	return &usage.Figures{Tokens: usage.Tokens{Input: input}, Money: est(money)}
}

func noLive() (usage.Figures, bool) { return usage.Figures{}, false }

// A reset's figures are what the chat used since the last reset: the transcript's cost-state is cumulative
// while its path stays the same, and a new path is a new session.
func TestResetUsageFirstResetRecordsTheWholeFile(t *testing.T) {
	var u resetUsage
	got := u.figures("/c/projects/p/a.jsonl", fig(100, 0.5), noLive)
	if got == nil || got.Tokens.Input != 100 || got.Money.Amount != 0.5 {
		t.Errorf("got %+v, want the whole file", got)
	}
}

func TestResetUsageSamePathRecordsTheDelta(t *testing.T) {
	var u resetUsage
	u.figures("/c/projects/p/a.jsonl", fig(100, 0.5), noLive)
	got := u.figures("/c/projects/p/a.jsonl", fig(250, 1.25), noLive)
	if got == nil || got.Tokens.Input != 150 || got.Money.Amount != 0.75 {
		t.Errorf("got %+v, want 150 tokens and 0.75 since the last reset", got)
	}
	got = u.figures("/c/projects/p/a.jsonl", fig(260, 1.5), noLive)
	if got == nil || got.Tokens.Input != 10 || got.Money.Amount != 0.25 {
		t.Errorf("got %+v, want the delta from the second reset, not the first", got)
	}
}

// Sub says reset when a figure went down: the file started over, so its figures are the use.
func TestResetUsageFigureThatWentDownRecordsTheCurrentFigures(t *testing.T) {
	var u resetUsage
	u.figures("/c/projects/p/a.jsonl", fig(500, 2), noLive)
	got := u.figures("/c/projects/p/a.jsonl", fig(40, 0.1), noLive)
	if got == nil || got.Tokens.Input != 40 || got.Money.Amount != 0.1 {
		t.Errorf("got %+v, want the current figures after a reset", got)
	}
}

// After a kill and restart the chat has a new transcript: its figures are its own.
func TestResetUsageNewPathRecordsTheWholeFile(t *testing.T) {
	var u resetUsage
	u.figures("/c/projects/p/a.jsonl", fig(500, 2), noLive)
	got := u.figures("/c/projects/p/b.jsonl", fig(30, 0.1), noLive)
	if got == nil || got.Tokens.Input != 30 {
		t.Errorf("got %+v, want the new file's figures, no delta across files", got)
	}
	got = u.figures("/c/projects/p/b.jsonl", fig(80, 0.3), noLive)
	if got == nil || got.Tokens.Input != 50 {
		t.Errorf("got %+v, want the delta against the new file", got)
	}
}

func TestResetUsageNoFiguresUsesTheLastLiveFigures(t *testing.T) {
	var u resetUsage
	live := usage.Figures{Tokens: usage.Tokens{Input: 9}, Money: est(0.05)}
	got := u.figures("/c/projects/p/a.jsonl", nil, func() (usage.Figures, bool) { return live, true })
	if got == nil || got.Tokens.Input != 9 || got.Money.Amount != 0.05 {
		t.Errorf("got %+v, want the live figures", got)
	}
}

func TestResetUsageNoFiguresAndNoLiveFiguresIsNil(t *testing.T) {
	var u resetUsage
	if got := u.figures("/c/projects/p/a.jsonl", nil, noLive); got != nil {
		t.Errorf("got %+v, want nil so the record has null figures", got)
	}
	if got := u.figures("/c/projects/p/a.jsonl", nil, nil); got != nil {
		t.Errorf("got %+v with no live seam at all, want nil", got)
	}
}

// A reset with no figures leaves the last reading alone: the next one is still a delta against it.
func TestResetUsageAReadThatFoundNothingKeepsThePreviousReading(t *testing.T) {
	var u resetUsage
	u.figures("/c/projects/p/a.jsonl", fig(100, 0.5), noLive)
	u.figures("/c/projects/p/a.jsonl", nil, noLive)
	got := u.figures("/c/projects/p/a.jsonl", fig(130, 0.7), noLive)
	if got == nil || got.Tokens.Input != 30 {
		t.Errorf("got %+v, want 30 since the last reading", got)
	}
}
