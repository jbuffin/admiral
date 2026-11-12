package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jbuffin/admiral/internal/sessionhost"
)

// Claude Code documents no status-line timeout, so the bound is Admiral's.
const statuslineTimeout = 2 * time.Second

// kept values stay far below tmux's command size limit.
const maxKeptStatusline = 4096

const managedSettingsPath = "/Library/Application Support/ClaudeCode/managed-settings.json"

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
		Now: time.Now, Write: sessionhost.WriteCurrent,
	})
}

func runStatusline(args []string, std IO, env statuslineEnv) error {
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
	if out, ok := runEngineerStatusline(command, raw, env.Timeout); ok {
		_, _ = std.Out.Write(out)
	}
	return nil
}

type slWindow struct {
	UsedPercentage json.Number `json:"used_percentage,omitempty"`
	ResetsAt       json.Number `json:"resets_at,omitempty"`
}

type slUsage struct {
	InputTokens              json.Number `json:"input_tokens,omitempty"`
	OutputTokens             json.Number `json:"output_tokens,omitempty"`
	CacheCreationInputTokens json.Number `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     json.Number `json:"cache_read_input_tokens,omitempty"`
}

type slStatus struct {
	SessionID      string `json:"session_id,omitempty"`
	TranscriptPath string `json:"transcript_path,omitempty"`
	Cost           *struct {
		TotalCostUSD json.Number `json:"total_cost_usd,omitempty"`
	} `json:"cost,omitempty"`
	ContextWindow *struct {
		TotalInputTokens  json.Number `json:"total_input_tokens,omitempty"`
		TotalOutputTokens json.Number `json:"total_output_tokens,omitempty"`
		CurrentUsage      *slUsage    `json:"current_usage,omitempty"`
	} `json:"context_window,omitempty"`
	RateLimits *struct {
		FiveHour *slWindow `json:"five_hour,omitempty"`
		SevenDay *slWindow `json:"seven_day,omitempty"`
	} `json:"rate_limits,omitempty"`
}

type slKept struct {
	Time string `json:"time"`
	slStatus
}

// keptStatusline returns "" for anything that isn't a JSON object or is over the bound.
func keptStatusline(raw []byte, now time.Time) string {
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil || obj == nil {
		return ""
	}
	var s slStatus
	if json.Unmarshal(raw, &s) != nil {
		return ""
	}
	b, err := json.Marshal(slKept{Time: now.UTC().Format(time.RFC3339), slStatus: s})
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
	for _, f := range []struct {
		path    string
		managed bool
	}{{env.ManagedPath, true}, {filepath.Join(userDir, "settings.json"), false}} {
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
	return len(f) >= 2 && filepath.Base(f[0]) == "admiral" && f[1] == "statusline"
}

// The line runs in its own process group so a timeout kills the children that would
// otherwise keep stdout open.
func runEngineerStatusline(command string, stdin []byte, limit time.Duration) ([]byte, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Stdin = bytes.NewReader(stdin)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 200 * time.Millisecond
	if err := cmd.Run(); err != nil {
		return nil, false
	}
	return out.Bytes(), true
}
