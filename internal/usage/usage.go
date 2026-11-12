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
	// PremiumRequestCost is Copilot's premium request count for the run; nil when the harness gave none.
	PremiumRequestCost *float64
}

func combinePremium(a, b *float64, op func(x, y float64) float64) *float64 {
	if a == nil || b == nil {
		return nil
	}
	v := op(*a, *b)
	return &v
}

// Add and Sub return false when the bases differ.
func (m Money) Add(o Money) (Money, bool) {
	if m.Basis != o.Basis {
		return Money{}, false
	}
	return Money{m.Amount + o.Amount, m.Basis, m.Incomplete || o.Incomplete, combinePremium(m.PremiumRequestCost, o.PremiumRequestCost, func(x, y float64) float64 { return x + y })}, true
}

func (m Money) Sub(o Money) (Money, bool) {
	if m.Basis != o.Basis {
		return Money{}, false
	}
	return Money{m.Amount - o.Amount, m.Basis, m.Incomplete || o.Incomplete, combinePremium(m.PremiumRequestCost, o.PremiumRequestCost, func(x, y float64) float64 { return x - y })}, true
}

// Window is one rate-limit window of a plan.
type Window struct {
	UsedPercent   float64
	WindowMinutes int
	ResetsAt      int64 // unix seconds
}

// Credits is a plan's pay-as-you-go balance. Balance is the raw text the harness wrote; empty when it gave none.
type Credits struct {
	HasCredits bool
	Unlimited  bool
	Balance    string
}

// Quota is the plan's rate-limit state when the figures were written (Codex only).
type Quota struct {
	Primary, Secondary *Window
	PlanType           string
	Credits            *Credits
	ReachedType        string // which limit stopped the run; empty when none did
}

// Figures are one harness's totals for a session. Tokens and Money are cumulative across a resume.
type Figures struct {
	Tokens Tokens
	Money  *Money // nil when the harness reports none
	Quota  *Quota
}

func (t Tokens) negative() bool {
	return t.Input < 0 || t.Output < 0 || t.Reasoning < 0 || t.CacheRead < 0 || t.CacheWrite < 0
}

// Sub gives the use since an earlier reading of the same session. The result carries no Quota:
// a window's state is not a difference. ok is false on a reset (a class or the money went down,
// or the money bases differ), and the caller must not store the result as usage.
func (f Figures) Sub(prev Figures) (d Figures, ok bool) {
	d = Figures{Tokens: f.Tokens.Sub(prev.Tokens)}
	ok = !d.Tokens.negative()
	switch {
	case f.Money != nil && prev.Money != nil:
		m, same := f.Money.Sub(*prev.Money)
		if !same || m.Amount < 0 {
			ok = false
		}
		if same {
			d.Money = &m
		}
	case f.Money != nil:
		m := *f.Money
		d.Money = &m
	}
	return d, ok
}

// maxLine bounds one line: a longer one is skipped, so a hostile or runaway transcript cannot fill memory.
const maxLine = 4 << 20

// eachLine calls fn with every non-empty line of at most maxLine bytes. fn gets a slice it must not keep.
func eachLine(r io.Reader, fn func([]byte)) error {
	br := bufio.NewReaderSize(r, 64*1024)
	var line []byte
	skip := false
	for {
		chunk, err := br.ReadSlice('\n')
		if !skip {
			line = append(line, chunk...)
			skip = len(line) > maxLine
			if skip {
				line = line[:0]
			}
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if !skip {
			if l := bytes.TrimSpace(line); len(l) > 0 {
				fn(l)
			}
		}
		line, skip = line[:0], false
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
