package usage

import (
	"bytes"
	"encoding/json"
	"time"
)

// LiveReading is what one status-line line holds. Seen is zero when the line carries no time of its own.
type LiveReading struct {
	SessionID string
	Seen      time.Time
	Figures   *Figures
	Quota     *Quota
}

// Claude's status line names the five-hour and seven-day windows without their lengths.
const (
	fiveHourMinutes = 300
	sevenDayMinutes = 10080
)

// numField decodes obj[key] as a number; a missing, null or wrong-typed value is absent, so one bad field drops only itself.
func numField(obj map[string]json.RawMessage, key string) (float64, bool) {
	raw, ok := obj[key]
	if !ok {
		return 0, false
	}
	var v *float64
	if json.Unmarshal(raw, &v) != nil || v == nil {
		return 0, false
	}
	return *v, true
}

func strField(obj map[string]json.RawMessage, key string) string {
	var s string
	if raw, ok := obj[key]; ok {
		_ = json.Unmarshal(raw, &s)
	}
	return s
}

func objField(obj map[string]json.RawMessage, key string) map[string]json.RawMessage {
	var m map[string]json.RawMessage
	if raw, ok := obj[key]; ok {
		_ = json.Unmarshal(raw, &m)
	}
	return m
}

func windowOf(obj map[string]json.RawMessage, key string, minutes int) *Window {
	w := objField(obj, key)
	used, ok := numField(w, "used_percentage")
	if !ok {
		return nil
	}
	resets, _ := numField(w, "resets_at")
	return &Window{UsedPercent: used, WindowMinutes: minutes, ResetsAt: int64(resets)}
}

// ParseStatusline reads Claude's status-line JSON, raw or as `admiral statusline` keeps it (the same field names
// plus the time it was kept). ok is false when the line holds neither figures nor a quota.
func ParseStatusline(b []byte) (LiveReading, bool) {
	var obj map[string]json.RawMessage
	if json.Unmarshal(b, &obj) != nil || obj == nil {
		return LiveReading{}, false
	}
	r := LiveReading{SessionID: strField(obj, "session_id")}
	if t, err := time.Parse(time.RFC3339, strField(obj, "time")); err == nil {
		r.Seen = t
	}
	var fig Figures
	have := false
	if usd, ok := numField(objField(obj, "cost"), "total_cost_usd"); ok {
		fig.Money, have = &Money{Amount: usd, Basis: BasisEstimate}, true
	}
	cw := objField(obj, "context_window")
	in, hasIn := numField(cw, "total_input_tokens")
	out, hasOut := numField(cw, "total_output_tokens")
	if hasIn || hasOut {
		fig.Tokens, have = Tokens{Input: int64(in), Output: int64(out)}, true
	}
	if have {
		r.Figures = &fig
	}
	if rl := objField(obj, "rate_limits"); rl != nil {
		q := Quota{Primary: windowOf(rl, "five_hour", fiveHourMinutes), Secondary: windowOf(rl, "seven_day", sevenDayMinutes)}
		if sp := objField(rl, "spend_limit"); sp != nil {
			if used, ok := numField(sp, "used_percentage"); ok {
				resets, _ := numField(sp, "resets_at")
				usd, _ := numField(sp, "used_usd")
				limit, _ := numField(sp, "limit_usd")
				q.Spend = &SpendLimit{UsedPercent: used, ResetsAt: int64(resets), UsedUSD: usd, LimitUSD: limit, Period: strField(sp, "period")}
			}
		}
		if q.Primary != nil || q.Secondary != nil || q.Spend != nil {
			r.Quota = &q
		}
	}
	if r.Figures == nil && r.Quota == nil {
		return LiveReading{}, false
	}
	return r, true
}

type codexTokenCounts struct {
	InputTokens           int64 `json:"inputTokens"`
	CachedInputTokens     int64 `json:"cachedInputTokens"`
	OutputTokens          int64 `json:"outputTokens"`
	ReasoningOutputTokens int64 `json:"reasoningOutputTokens"`
}

// ParseCodexTokenUsage reads a thread/tokenUsage/updated frame: the thread's running totals. Cached input sits
// inside inputTokens and reasoning inside outputTokens, so the classes are made disjoint.
func ParseCodexTokenUsage(frame []byte) (thread string, f Figures, ok bool) {
	if !bytes.Contains(frame, []byte("thread/tokenUsage/updated")) {
		return "", Figures{}, false
	}
	var m struct {
		Method string `json:"method"`
		Params struct {
			ThreadID   string `json:"threadId"`
			TokenUsage struct {
				Total *codexTokenCounts `json:"total"`
			} `json:"tokenUsage"`
		} `json:"params"`
	}
	if json.Unmarshal(frame, &m) != nil || m.Method != "thread/tokenUsage/updated" || m.Params.ThreadID == "" || m.Params.TokenUsage.Total == nil {
		return "", Figures{}, false
	}
	c := m.Params.TokenUsage.Total
	return m.Params.ThreadID, Figures{Tokens: Tokens{
		Input: max(c.InputTokens-c.CachedInputTokens, 0), Output: max(c.OutputTokens-c.ReasoningOutputTokens, 0),
		Reasoning: c.ReasoningOutputTokens, CacheRead: c.CachedInputTokens}}, true
}

type codexLiveWindow struct {
	UsedPercent        float64 `json:"usedPercent"`
	WindowDurationMins int     `json:"windowDurationMins"`
	ResetsAt           int64   `json:"resetsAt"`
}

func (w *codexLiveWindow) window() *Window {
	if w == nil {
		return nil
	}
	return &Window{UsedPercent: w.UsedPercent, WindowMinutes: w.WindowDurationMins, ResetsAt: w.ResetsAt}
}

// ParseCodexRateLimits reads an account/rateLimits/updated frame. The frame's shape comes from the app-server's
// documented schema; no real capture has checked it.
func ParseCodexRateLimits(frame []byte) (Quota, bool) {
	if !bytes.Contains(frame, []byte("account/rateLimits/updated")) {
		return Quota{}, false
	}
	var m struct {
		Method string `json:"method"`
		Params struct {
			RateLimits *struct {
				Primary   *codexLiveWindow `json:"primary"`
				Secondary *codexLiveWindow `json:"secondary"`
				PlanType  string           `json:"planType"`
			} `json:"rateLimits"`
		} `json:"params"`
	}
	if json.Unmarshal(frame, &m) != nil || m.Method != "account/rateLimits/updated" || m.Params.RateLimits == nil {
		return Quota{}, false
	}
	rl := m.Params.RateLimits
	q := Quota{Primary: rl.Primary.window(), Secondary: rl.Secondary.window(), PlanType: rl.PlanType}
	if q.Primary == nil && q.Secondary == nil {
		return Quota{}, false
	}
	return q, true
}
