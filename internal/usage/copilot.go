package usage

import (
	"bytes"
	"encoding/json"
	"io"
	"sort"
)

type copilotUsage struct {
	InputTokens      int64 `json:"inputTokens"`
	OutputTokens     int64 `json:"outputTokens"`
	CacheReadTokens  int64 `json:"cacheReadTokens"`
	CacheWriteTokens int64 `json:"cacheWriteTokens"`
	ReasoningTokens  int64 `json:"reasoningTokens"`
}

// tokens makes the classes disjoint: inputTokens includes the cache reads and writes, and outputTokens
// is taken to include reasoning as the other harnesses do (unconfirmed: the sample has none).
func (u copilotUsage) tokens() Tokens {
	return Tokens{Input: u.InputTokens - u.CacheReadTokens - u.CacheWriteTokens, Output: u.OutputTokens - u.ReasoningTokens, Reasoning: u.ReasoningTokens, CacheRead: u.CacheReadTokens, CacheWrite: u.CacheWriteTokens}
}

type copilotEvent struct {
	Type string `json:"type"`
	Data struct {
		TotalNanoAiu float64 `json:"totalNanoAiu"`
		// Agent and snapshot copies repeat these figures, so only the top level is read.
		ModelMetrics map[string]struct {
			Usage copilotUsage `json:"usage"`
		} `json:"modelMetrics"`
		AccountingSnapshot struct {
			TotalPremiumRequestCost float64 `json:"totalPremiumRequestCost"`
			AICreditsStatus         string  `json:"aiCreditsStatus"`
		} `json:"accountingSnapshot"`
		Usage *struct {
			copilotUsage
			CopilotUsage struct {
				TotalNanoAiu float64 `json:"totalNanoAiu"`
			} `json:"copilotUsage"`
		} `json:"usage"`
	} `json:"data"`
}

const nanoPerCredit = 1e9

// ParseCopilot reads the last session.shutdown and adds the session.usage_record events written after it.
// Without a shutdown the records are summed alone. Whether a shutdown is cumulative across a resume or
// covers one process is unconfirmed (the recorded sample has a single run); counting the records after it
// neither drops nor repeats any. The premium request cost is nil when records were added on top, since a
// record's own cost is not a request count.
func ParseCopilot(r io.Reader) (Figures, error) {
	var a CopilotAcc
	if err := eachLine(r, a.Line); err != nil {
		return Figures{}, err
	}
	return a.Figures()
}

// CopilotAcc sums an events file line by line, so a reader of an append-only file can keep its totals and feed
// only the new lines.
type CopilotAcc struct {
	base      *Figures
	since     Figures
	haveSince bool
}

// Line takes one trimmed, non-empty line of the file.
func (a *CopilotAcc) Line(line []byte) {
	if !bytes.Contains(line, []byte(`"session.`)) {
		return
	}
	var e copilotEvent
	if json.Unmarshal(line, &e) != nil {
		return
	}
	switch e.Type {
	case "session.shutdown":
		if len(e.Data.ModelMetrics) == 0 {
			return // scrubbed or empty
		}
		names := make([]string, 0, len(e.Data.ModelMetrics))
		for n := range e.Data.ModelMetrics {
			names = append(names, n)
		}
		sort.Strings(names)
		var t Tokens
		for _, n := range names {
			t = t.Add(e.Data.ModelMetrics[n].Usage.tokens())
		}
		snap := e.Data.AccountingSnapshot
		premium := snap.TotalPremiumRequestCost
		a.base = &Figures{Tokens: t, Money: &Money{Amount: e.Data.TotalNanoAiu / nanoPerCredit, Basis: BasisCredits,
			Incomplete: snap.AICreditsStatus != "" && snap.AICreditsStatus != "complete", PremiumRequestCost: &premium}}
		a.since, a.haveSince = Figures{}, false
	case "session.usage_record":
		u := e.Data.Usage
		if u == nil {
			return
		}
		if a.since.Money == nil {
			a.since.Money = &Money{Basis: BasisCredits}
		}
		a.since.Tokens = a.since.Tokens.Add(u.tokens())
		a.since.Money.Amount += u.CopilotUsage.TotalNanoAiu / nanoPerCredit
		a.haveSince = true
	}
}

// Figures is what the lines so far add up to; ErrNoFigures when none carries any.
func (a *CopilotAcc) Figures() (Figures, error) {
	base, since, haveSince := a.base, a.since, a.haveSince
	switch {
	case base == nil && !haveSince:
		return Figures{}, ErrNoFigures
	case base == nil:
		return since, nil
	case !haveSince:
		return *base, nil
	}
	m, _ := base.Money.Add(*since.Money)
	m.PremiumRequestCost = nil
	return Figures{Tokens: base.Tokens.Add(since.Tokens), Money: &m}, nil
}
