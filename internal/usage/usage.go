// Package usage reads the token and money figures a harness leaves at the end of a run.
package usage

import (
	"bufio"
	"bytes"
	"errors"
	"io"
)

// ErrNoFigures means the transcript holds no usage line, which is not the same as zero use.
var ErrNoFigures = errors.New("usage: no figures in transcript")

// Tokens counts by class. The classes are disjoint: Input excludes cache reads and writes,
// and Output excludes Reasoning, so figures add up without counting a token twice.
type Tokens struct {
	Input      int64
	Output     int64
	Reasoning  int64
	CacheRead  int64
	CacheWrite int64
}

func (t Tokens) Add(o Tokens) Tokens {
	return Tokens{t.Input + o.Input, t.Output + o.Output, t.Reasoning + o.Reasoning, t.CacheRead + o.CacheRead, t.CacheWrite + o.CacheWrite}
}

// Sub gives the use since an earlier cumulative reading, such as the previous round of a resumed session.
func (t Tokens) Sub(o Tokens) Tokens {
	return Tokens{t.Input - o.Input, t.Output - o.Output, t.Reasoning - o.Reasoning, t.CacheRead - o.CacheRead, t.CacheWrite - o.CacheWrite}
}

// Basis says what a Money amount is measured in; amounts of different bases never sum.
type Basis string

const (
	BasisEstimate Basis = "estimate" // US dollars, the harness's own estimate
	BasisCredits  Basis = "credits"  // AI credits
)

type Money struct {
	Amount float64
	Basis  Basis
	// Incomplete marks an estimate that left out a model the harness has no price for.
	Incomplete bool
	// PremiumRequestCost is Copilot's premium request count for the run; zero elsewhere.
	PremiumRequestCost float64
}

// Add and Sub return false when the bases differ.
func (m Money) Add(o Money) (Money, bool) {
	if m.Basis != o.Basis {
		return Money{}, false
	}
	return Money{m.Amount + o.Amount, m.Basis, m.Incomplete || o.Incomplete, m.PremiumRequestCost + o.PremiumRequestCost}, true
}

func (m Money) Sub(o Money) (Money, bool) {
	if m.Basis != o.Basis {
		return Money{}, false
	}
	return Money{m.Amount - o.Amount, m.Basis, m.Incomplete || o.Incomplete, m.PremiumRequestCost - o.PremiumRequestCost}, true
}

// Window is one rate-limit window of a plan.
type Window struct {
	UsedPercent   float64
	WindowMinutes int
	ResetsAt      int64 // unix seconds
}

// Quota is the plan's rate-limit state when the figures were written (Codex only).
type Quota struct {
	Primary, Secondary *Window
	PlanType           string
}

// Figures are one harness's totals for a session. Tokens and Money are cumulative across a resume.
type Figures struct {
	Tokens Tokens
	Money  *Money // nil when the harness reports none
	Quota  *Quota
}

// Sub gives the use since an earlier reading of the same session. The result carries no Quota:
// a window's state is not a difference. Money is nil when the bases differ.
func (f Figures) Sub(prev Figures) Figures {
	out := Figures{Tokens: f.Tokens.Sub(prev.Tokens)}
	switch {
	case f.Money != nil && prev.Money != nil:
		if m, ok := f.Money.Sub(*prev.Money); ok {
			out.Money = &m
		}
	case f.Money != nil && prev.Money == nil:
		m := *f.Money
		out.Money = &m
	}
	return out
}

// eachLine calls fn with every non-empty line, however long. fn gets a slice it must not keep.
func eachLine(r io.Reader, fn func([]byte)) error {
	br := bufio.NewReaderSize(r, 64*1024)
	for {
		line, err := br.ReadBytes('\n')
		if line = bytes.TrimSpace(line); len(line) > 0 {
			fn(line)
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
