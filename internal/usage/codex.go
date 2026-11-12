package usage

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
)

type codexTotals struct {
	InputTokens     int64 `json:"input_tokens"`
	CachedInput     int64 `json:"cached_input_tokens"`
	CacheWrite      int64 `json:"cache_write_input_tokens"`
	OutputTokens    int64 `json:"output_tokens"`
	ReasoningOutput int64 `json:"reasoning_output_tokens"`
}

type codexWindow struct {
	UsedPercent   float64 `json:"used_percent"`
	WindowMinutes int     `json:"window_minutes"`
	ResetsAt      int64   `json:"resets_at"`
}

type codexRateLimits struct {
	Primary   *codexWindow `json:"primary"`
	Secondary *codexWindow `json:"secondary"`
	PlanType  string       `json:"plan_type"`
	Credits   *struct {
		HasCredits bool            `json:"has_credits"`
		Unlimited  bool            `json:"unlimited"`
		Balance    json.RawMessage `json:"balance"`
	} `json:"credits"`
	ReachedType *string `json:"rate_limit_reached_type"`
}

type codexTokenCount struct {
	Payload struct {
		Type string `json:"type"`
		Info *struct {
			Total codexTotals `json:"total_token_usage"`
		} `json:"info"`
		RateLimits *codexRateLimits `json:"rate_limits"`
	} `json:"payload"`
}

var tokenCountMarker = []byte(`"token_count"`)

// ParseCodex reads a rollout's token_count events: the last total_token_usage, and the last rate_limits.
// An event whose info is null still updates the quota. Codex reports no money.
func ParseCodex(r io.Reader) (Figures, error) {
	var tokens *Tokens
	var quota *Quota
	err := eachLine(r, func(line []byte) {
		if !bytes.Contains(line, tokenCountMarker) {
			return
		}
		var e codexTokenCount
		if json.Unmarshal(line, &e) != nil || e.Payload.Type != "token_count" {
			return
		}
		if e.Payload.Info != nil {
			c := e.Payload.Info.Total
			tokens = &Tokens{Input: c.InputTokens - c.CachedInput - c.CacheWrite, Output: c.OutputTokens - c.ReasoningOutput, Reasoning: c.ReasoningOutput, CacheRead: c.CachedInput, CacheWrite: c.CacheWrite}
		}
		if rl := e.Payload.RateLimits; rl != nil {
			quota = &Quota{Primary: rl.Primary.window(), Secondary: rl.Secondary.window(), PlanType: rl.PlanType}
			if rl.ReachedType != nil {
				quota.ReachedType = *rl.ReachedType
			}
			if cr := rl.Credits; cr != nil {
				quota.Credits = &Credits{HasCredits: cr.HasCredits, Unlimited: cr.Unlimited}
				if b := string(cr.Balance); b != "" && b != "null" {
					quota.Credits.Balance = strings.Trim(b, `"`)
				}
			}
		}
	})
	if err != nil {
		return Figures{}, err
	}
	if tokens == nil {
		return Figures{}, ErrNoFigures
	}
	return Figures{Tokens: *tokens, Quota: quota}, nil
}

func (w *codexWindow) window() *Window {
	if w == nil {
		return nil
	}
	return &Window{UsedPercent: w.UsedPercent, WindowMinutes: w.WindowMinutes, ResetsAt: w.ResetsAt}
}
