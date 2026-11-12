package usage

import (
	"bytes"
	"encoding/json"
	"io"
)

type claudeCostState struct {
	Type                string                      `json:"type"`
	TotalCostUSD        float64                     `json:"totalCostUSD"`
	ModelUsage          map[string]claudeModelUsage `json:"modelUsage"`
	HasUnknownModelCost bool                        `json:"hasUnknownModelCost"`
}

// The ephemeral 5m/1h cache fields split cacheCreationInputTokens, so they are not read.
type claudeModelUsage struct {
	InputTokens              int64 `json:"inputTokens"`
	OutputTokens             int64 `json:"outputTokens"`
	ThinkingTokens           int64 `json:"thinkingTokens"`
	CacheReadInputTokens     int64 `json:"cacheReadInputTokens"`
	CacheCreationInputTokens int64 `json:"cacheCreationInputTokens"`
}

var costStateMarker = []byte(`"cost-state"`)

// ParseClaude reads the last cost-state line of a Claude transcript.
func ParseClaude(r io.Reader) (Figures, error) {
	var last *claudeCostState
	err := eachLine(r, func(line []byte) {
		if !bytes.Contains(line, costStateMarker) {
			return
		}
		var s claudeCostState
		if json.Unmarshal(line, &s) == nil && s.Type == "cost-state" {
			last = &s
		}
	})
	if err != nil {
		return Figures{}, err
	}
	if last == nil {
		return Figures{}, ErrNoFigures
	}
	var t Tokens
	for _, m := range last.ModelUsage {
		// outputTokens includes the thinking tokens, so Output is what is left of it.
		t = t.Add(Tokens{m.InputTokens, m.OutputTokens - m.ThinkingTokens, m.ThinkingTokens, m.CacheReadInputTokens, m.CacheCreationInputTokens})
	}
	return Figures{Tokens: t, Money: &Money{Amount: last.TotalCostUSD, Basis: BasisEstimate, Incomplete: last.HasUnknownModelCost}}, nil
}
