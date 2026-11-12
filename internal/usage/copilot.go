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

// tokens makes the classes disjoint: Copilot's inputTokens includes the cache reads and writes.
func (u copilotUsage) tokens() Tokens {
	return Tokens{Input: u.InputTokens - u.CacheReadTokens - u.CacheWriteTokens, Output: u.OutputTokens, Reasoning: u.ReasoningTokens, CacheRead: u.CacheReadTokens, CacheWrite: u.CacheWriteTokens}
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

// ParseCopilot reads the session.shutdown event, or without one sums the session.usage_record events.
func ParseCopilot(r io.Reader) (Figures, error) {
	var shutdown *Figures
	var records Figures
	haveRecords := false
	err := eachLine(r, func(line []byte) {
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
			shutdown = &Figures{Tokens: t, Money: &Money{Amount: e.Data.TotalNanoAiu / nanoPerCredit, Basis: BasisCredits, PremiumRequestCost: e.Data.AccountingSnapshot.TotalPremiumRequestCost}}
		case "session.usage_record":
			u := e.Data.Usage
			if u == nil {
				return
			}
			if records.Money == nil {
				records.Money = &Money{Basis: BasisCredits}
			}
			records.Tokens = records.Tokens.Add(u.tokens())
			records.Money.Amount += u.CopilotUsage.TotalNanoAiu / nanoPerCredit
			haveRecords = true
		}
	})
	if err != nil {
		return Figures{}, err
	}
	if shutdown != nil {
		return *shutdown, nil
	}
	if !haveRecords {
		return Figures{}, ErrNoFigures
	}
	return records, nil
}
