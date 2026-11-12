package chat

import (
	"encoding/json"
	"time"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/delay"
)

// FlushPrompt is pasted into the chat before Admiral clears it; the flush turn stays out of the history.
const FlushPrompt = "Admiral is about to clear your context. If the engineer asked you to remember anything you haven't saved with save_note, save it now. Then reply with just: done."

const (
	MarkerNewChat   = "New chat"
	MarkerRefreshed = "Context refreshed"
	// Refreshing is the 409 a send gets while a reset runs.
	Refreshing = "AdmiralBot is refreshing its context. Send again in a moment."
)

// ResetRule names the rule that made a reset due.
type ResetRule string

const (
	ResetNone      ResetRule = ""
	ResetThreshold ResetRule = "threshold"
	ResetIdle      ResetRule = "idle"
	ResetDaily     ResetRule = "daily"
)

// IdleContextFloor is the context size above which the long-idle rule fires.
const IdleContextFloor = 100_000

// ResetInput is everything the scheduler reads about one fleet's chat.
type ResetInput struct {
	Now             time.Time // its location decides what 04:00 means
	Running         bool
	State           string // Snapshot.State; only "idle" counts
	QuestionWaiting bool
	IdleSince       time.Time
	Context         int64
	HaveContext     bool
	ContextSeen     time.Time // when the reading was kept; one before LastReset is stale
	Threshold       int       // config.ChatNever (0) turns every rule off
	LastReset       time.Time // or the session's start
	TurnSinceReset  bool
	ShortIdle       time.Duration // the threshold rule's idle time (10 min)
	LongIdle        time.Duration // the idle rule's idle time (60 min)
}

// DailyHour is the local hour of the daily reset.
const DailyHour = 4

// DueReset is the rule that makes a reset due now, or ResetNone.
func DueReset(in ResetInput) ResetRule {
	if !in.Running || in.State != "idle" || in.QuestionWaiting || in.Threshold == config.ChatNever || in.IdleSince.IsZero() {
		return ResetNone
	}
	// A reading kept at or before the last reset still holds the old size: Claude has not redrawn since the clear.
	if in.HaveContext && in.ContextSeen.After(in.LastReset) {
		idle := in.Now.Sub(in.IdleSince)
		if in.Context > int64(in.Threshold) && idle >= in.ShortIdle {
			return ResetThreshold
		}
		if in.Context > IdleContextFloor && idle >= in.LongIdle {
			return ResetIdle
		}
	}
	y, m, d := in.Now.Date()
	at := time.Date(y, m, d, DailyHour, 0, 0, 0, in.Now.Location())
	if !in.Now.Before(at) && in.LastReset.Before(at) && in.TurnSinceReset {
		return ResetDaily
	}
	return ResetNone
}

// ResetIdleWindows are the threshold rule's and the idle rule's idle times: 10 and 60 minutes, or with
// ADMIRAL_CHAT_RESET_IDLE set that value and six times it.
func ResetIdleWindows() (short, long time.Duration) {
	short = delay.From("ADMIRAL_CHAT_RESET_IDLE", 10*time.Minute)
	return short, 6 * short
}

// ContextTokens is the chat's context size in a kept status line: input, cache creation and cache read
// tokens of context_window.current_usage. ok is false without that reading.
func ContextTokens(kept []byte) (tokens int64, seen time.Time, ok bool) {
	var l struct {
		Time          time.Time `json:"time"`
		ContextWindow struct {
			CurrentUsage *struct {
				Input         int64 `json:"input_tokens"`
				CacheCreation int64 `json:"cache_creation_input_tokens"`
				CacheRead     int64 `json:"cache_read_input_tokens"`
			} `json:"current_usage"`
		} `json:"context_window"`
	}
	if json.Unmarshal(kept, &l) != nil || l.ContextWindow.CurrentUsage == nil {
		return 0, time.Time{}, false
	}
	u := l.ContextWindow.CurrentUsage
	return u.Input + u.CacheCreation + u.CacheRead, l.Time, true
}

// FlushWait bounds how long a reset waits for the flush turn to end: 3 minutes, or ADMIRAL_CHAT_FLUSH_WAIT.
func FlushWait() time.Duration { return delay.From("ADMIRAL_CHAT_FLUSH_WAIT", 3*time.Minute) }
