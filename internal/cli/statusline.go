package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jbuffin/admiral/internal/sessionhost"
)

// Claude Code documents no status-line timeout, so the bound is Admiral's. Claude's own
// cancel on the next update ends most runs sooner.
const statuslineTimeout = 10 * time.Second

// kept values stay far below tmux's command size limit.
const maxKeptStatusline = 4096

const managedSettingsPath = "/Library/Application Support/ClaudeCode/managed-settings.json"

// statuslineMarker is set on the engineer's line, so a line that reaches `admiral statusline`
// by any route (a script, `env`, a quoted path) ends the chain there.
const statuslineMarker = "ADMIRAL_STATUSLINE"

type statuslineEnv struct {
	Pane        string
	ConfigDir   string // $CLAUDE_CONFIG_DIR of the wrapper's own env
	Home        string
	ManagedPath string // the managed settings file
	Timeout     time.Duration
	Now         func() time.Time
	Write       func(call []string) error
}

// Statusline never fails: Claude blanks its status line on a non-zero exit.
func Statusline(args []string, std IO) error {
	home, _ := os.UserHomeDir()
	return runStatusline(args, std, statuslineEnv{
		Pane: os.Getenv("TMUX_PANE"), ConfigDir: os.Getenv("CLAUDE_CONFIG_DIR"), Home: home,
		ManagedPath: managedSettingsPath, Timeout: statuslineTimeout,
		Now: time.Now, Write: writeWithin,
	})
}

// A hung tmux server must not stop the engineer's line from running.
func writeWithin(call []string) error {
	return sessionhost.WriteCurrentWithin(call, sessionhost.CurrentDeadline)
}

func runStatusline(args []string, std IO, env statuslineEnv) error {
	if os.Getenv(statuslineMarker) != "" {
		return nil
	}
	raw, _ := io.ReadAll(io.LimitReader(std.In, 1<<20))
	// Kept before the engineer's line runs: Claude cancels a running line on the next update.
	if env.Pane != "" && env.Write != nil {
		if v := keptStatusline(raw, env.Now()); v != "" {
			_ = env.Write(sessionhost.SetPaneCall(env.Pane, sessionhost.StatuslineOption, v))
		}
	}
	// AdmiralBot loads project settings only, so Claude would show no user status line there.
	if len(args) > 0 && args[0] == "--chat" {
		return nil
	}
	command := engineerStatusline(env)
	if command == "" || runsAdmiralStatusline(command) {
		return nil
	}
	if out, ok := runEngineerStatusline(command, raw, env.Timeout, std.Err); ok {
		_, _ = std.Out.Write(out)
	}
	return nil
}

type slWindow struct {
	UsedPercentage json.Number `json:"used_percentage,omitempty"`
	ResetsAt       json.Number `json:"resets_at,omitempty"`
}

type slSpend struct {
	slWindow
	UsedUSD  json.Number `json:"used_usd,omitempty"`
	LimitUSD json.Number `json:"limit_usd,omitempty"`
	Period   string      `json:"period,omitempty"`
}

type slUsage struct {
	InputTokens              json.Number `json:"input_tokens,omitempty"`
	OutputTokens             json.Number `json:"output_tokens,omitempty"`
	CacheCreationInputTokens json.Number `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     json.Number `json:"cache_read_input_tokens,omitempty"`
}

type slCost struct {
	TotalCostUSD json.Number `json:"total_cost_usd,omitempty"`
}

type slContext struct {
	TotalInputTokens  json.Number `json:"total_input_tokens,omitempty"`
	TotalOutputTokens json.Number `json:"total_output_tokens,omitempty"`
	CurrentUsage      *slUsage    `json:"current_usage,omitempty"`
}

type slLimits struct {
	FiveHour   *slWindow `json:"five_hour,omitempty"`
	SevenDay   *slWindow `json:"seven_day,omitempty"`
	SpendLimit *slSpend  `json:"spend_limit,omitempty"`
}

type slKept struct {
	Time           string     `json:"time"`
	SessionID      string     `json:"session_id,omitempty"`
	TranscriptPath string     `json:"transcript_path,omitempty"`
	Cost           *slCost    `json:"cost,omitempty"`
	ContextWindow  *slContext `json:"context_window,omitempty"`
	RateLimits     *slLimits  `json:"rate_limits,omitempty"`
}

// decodeField fills dst from obj[key]; a missing, null or wrong-typed value leaves it zero
// and reports false, so one bad field drops only itself.
func decodeField(obj map[string]json.RawMessage, key string, dst any) bool {
	raw, ok := obj[key]
	if !ok || string(raw) == "null" {
		return false
	}
	return json.Unmarshal(raw, dst) == nil
}

func objectField(obj map[string]json.RawMessage, key string) map[string]json.RawMessage {
	var m map[string]json.RawMessage
	decodeField(obj, key, &m)
	return m
}

// keptStatusline returns "" for anything that isn't a JSON object or is over the bound.
func keptStatusline(raw []byte, now time.Time) string {
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil || obj == nil {
		return ""
	}
	k := slKept{Time: now.UTC().Format(time.RFC3339)}
	decodeField(obj, "session_id", &k.SessionID)
	decodeField(obj, "transcript_path", &k.TranscriptPath)
	var cost slCost
	if decodeField(obj, "cost", &cost) {
		k.Cost = &cost
	}
	if cw := objectField(obj, "context_window"); cw != nil {
		var c slContext
		decodeField(cw, "total_input_tokens", &c.TotalInputTokens)
		decodeField(cw, "total_output_tokens", &c.TotalOutputTokens)
		var u slUsage
		if decodeField(cw, "current_usage", &u) {
			c.CurrentUsage = &u
		}
		k.ContextWindow = &c
	}
	if rl := objectField(obj, "rate_limits"); rl != nil {
		var l slLimits
		var five, seven slWindow
		var spend slSpend
		if decodeField(rl, "five_hour", &five) {
			l.FiveHour = &five
		}
		if decodeField(rl, "seven_day", &seven) {
			l.SevenDay = &seven
		}
		if decodeField(rl, "spend_limit", &spend) {
			l.SpendLimit = &spend
		}
		k.RateLimits = &l
	}
	b, err := json.Marshal(k)
	if err != nil || len(b) > maxKeptStatusline {
		return ""
	}
	return string(b)
}

type claudeSettingsFile struct {
	DisableAllHooks bool `json:"disableAllHooks"`
	StatusLine      *struct {
		Command string `json:"command"`
	} `json:"statusLine"`
}

// engineerStatusline is the line Claude would have run without Admiral's --settings. Project
// and local settings are never read: a ship's checkout is untrusted and the wrapper can't
// apply Claude's workspace-trust check.
func engineerStatusline(env statuslineEnv) string {
	userDir := env.ConfigDir
	if userDir == "" {
		userDir = filepath.Join(env.Home, ".claude")
	}
	type settingsFile struct {
		path    string
		managed bool
	}
	files := []settingsFile{{env.ManagedPath, true}}
	// A relative directory would be the checkout's own .claude.
	if filepath.IsAbs(userDir) {
		files = append(files, settingsFile{filepath.Join(userDir, "settings.json"), false})
	}
	for _, f := range files {
		b, err := os.ReadFile(f.path)
		if err != nil {
			continue
		}
		var s claudeSettingsFile
		if json.Unmarshal(b, &s) != nil || (s.DisableAllHooks && !f.managed) {
			continue
		}
		if s.StatusLine != nil && strings.TrimSpace(s.StatusLine.Command) != "" {
			return s.StatusLine.Command
		}
	}
	return ""
}

func runsAdmiralStatusline(command string) bool {
	f := strings.Fields(command)
	for len(f) > 0 && (f[0] == "exec" || strings.Contains(f[0], "=")) {
		f = f[1:]
	}
	return len(f) >= 2 && filepath.Base(f[0]) == "admiral" && f[1] == "statusline"
}

// The line runs in its own process group so a timeout, or Claude cancelling the wrapper,
// kills the children that would otherwise keep stdout open or outlive it. After a normal exit
// the group is left alone: a line may deliberately leave a background refresher running.
func runEngineerStatusline(command string, stdin []byte, limit time.Duration, stderr io.Writer) ([]byte, bool) {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Stdin = bytes.NewReader(stdin)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = stderr
	cmd.Env = append(os.Environ(), statuslineMarker+"=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 200 * time.Millisecond
	err := cmd.Run()
	// A background child still holding stdout is not a failure of a line that exited 0.
	if err != nil && !(errors.Is(err, exec.ErrWaitDelay) && cmd.ProcessState != nil && cmd.ProcessState.Success()) {
		return nil, false
	}
	return out.Bytes(), true
}
