package cli

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"regexp"
	"time"

	"github.com/jbuffin/admiral/internal/sessionhost"
)

// Hook is `admiral hook <event>`, the command every harness hook runs (ADR
// 0013, 0014). It reads the harness's hook JSON on stdin and stamps
// @admiral-activity on $TMUX_PANE and sets @admiral-state there, with
// @admiral-reason for needs_you, when the event decides a state. It is
// internal, so no usage text names it, and it never fails: a harness treats a hook's exit code and output as its own signal.
func Hook(args []string, std IO) error {
	pane := os.Getenv("TMUX_PANE")
	if len(args) == 0 || pane == "" {
		return nil
	}
	raw, _ := io.ReadAll(io.LimitReader(std.In, 1<<20))
	var p hookPayload
	valid := json.Unmarshal(raw, &p) == nil
	// Every event stamps the pane's last activity, a crew boat's last_activity,
	// in the same tmux call as the state it may write.
	call := []string{"set", "-p", "-t", pane, sessionhost.ActivityOption, time.Now().UTC().Format(time.RFC3339)}
	if w := hookDecision(args[0], p); valid && w.state != "" {
		call = append(append(call, ";"), hookCall(pane, w)...)
	}
	_ = exec.Command("tmux", call...).Run()
	return nil
}

// hookCall is the one tmux call that writes the reason and the state, reason
// first (for working too, as Host.setPane does) so the poll never sees
// needs_you without it. A write with ifUnset (a notification's) sets the reason
// only if none is set (-o), so permission:Bash survives Claude's
// PermissionRequest and Notification in either order; -q keeps a set reason from
// making tmux skip the state write.
func hookCall(pane string, w hookWrite) []string {
	call := []string{"set", "-p"}
	switch {
	case w.reason == "":
		call = append(call, "-u", "-t", pane, "@admiral-reason")
	case w.ifUnset:
		call = append(call, "-o", "-q", "-t", pane, "@admiral-reason", w.reason)
	default:
		call = append(call, "-t", pane, "@admiral-reason", w.reason)
	}
	return append(call, ";", "set", "-p", "-t", pane, "@admiral-state", w.state)
}

// hookPayload holds the fields of Claude Code's, Codex's and Copilot's hook
// JSON that decide a state.
type hookPayload struct {
	ToolName         string `json:"tool_name"`         // Claude Code
	ToolNameCopilot  string `json:"toolName"`          // Copilot
	NotificationType string `json:"notification_type"` // Claude Code, Copilot
	AgentID          string `json:"agentId"`           // Copilot: set inside a subagent
	BackgroundTasks  []struct {
		Type   string `json:"type"`
		Status string `json:"status"`
	} `json:"background_tasks"` // Claude Code's Stop; Codex sends none
}

// hookWrite is what a hook writes to the pane: the state, and for needs_you the
// short code @admiral-reason holds with it. ifUnset says the reason must not
// replace one already set: a notification is Claude's second word on a prompt
// that PermissionRequest named.
type hookWrite struct {
	state, reason string
	ifUnset       bool
}

// hookDecision is what an event writes; a zero hookWrite is no state. Claude
// Code and Codex share event names, so one rule serves both. Only a tool name or
// notification type is ever kept in a reason, and only as a safe token: nothing
// else of the payload is stored.
func hookDecision(event string, p hookPayload) hookWrite {
	switch event {
	case "PermissionRequest":
		return hookWrite{state: "needs_you", reason: withToken("permission", p.ToolName)}
	case "PreToolUse":
		if p.ToolName == "AskUserQuestion" {
			return hookWrite{state: "needs_you", reason: "question"}
		}
		return hookWrite{state: "working"}
	case "UserPromptSubmit", "PostToolUse", "Interrupt", "postToolUse":
		return hookWrite{state: "working"}
	case "Notification", "notification":
		if p.NotificationType == "permission_prompt" || p.NotificationType == "elicitation_dialog" {
			return hookWrite{state: "needs_you", reason: withToken("notification", p.NotificationType), ifUnset: true}
		}
	case "Stop":
		// Only a subagent or an agent-team teammate holds the turn open; a
		// background shell (a Monitor) doesn't.
		for _, t := range p.BackgroundTasks {
			if t.Status == "running" && (t.Type == "subagent" || t.Type == "teammate") {
				return hookWrite{}
			}
		}
		return hookWrite{state: "needs_you", reason: "stop"}
	case "preToolUse":
		if p.ToolNameCopilot == "ask_user" {
			return hookWrite{state: "needs_you", reason: "question"}
		}
	case "agentStop":
		if p.AgentID == "" {
			return hookWrite{state: "needs_you", reason: "stop"}
		}
	case "userPromptSubmitted":
		if p.AgentID == "" {
			return hookWrite{state: "working"}
		}
	}
	return hookWrite{}
}

var safeToken = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,64}$`)

// withToken is prefix:token, or the bare prefix when token isn't short and made
// of characters tmux and a format leave alone.
func withToken(prefix, token string) string {
	if safeToken.MatchString(token) {
		return prefix + ":" + token
	}
	return prefix
}

// subagentList is @admiral-subagents after an event: the list before it, the
// harness ("claude", "codex", "copilot", or "" when tmux could not say) and
// the payload in, the new list out (ADR 0014).
func subagentList(event string, p hookPayload, harness string, cur []sessionhost.Subagent, now time.Time) []sessionhost.Subagent {
	return nil
}

// crewFacts is what the lead rule needs to know about the pane and its crew.
type crewFacts struct {
	ThisIsCrew     bool // $TMUX_PANE is in a crew window
	CrewWorking    bool // some live crew pane of the session is working
	CrewReadFailed bool // the list-panes read of the crew errored
}

// leadDecision is hookDecision with the lead rule on top: a lead's Stop waits
// while a crew boat works.
func leadDecision(event string, p hookPayload, f crewFacts) hookWrite {
	return hookWrite{}
}

// crewPaneFact is one pane of a crew window as the lead rule reads it.
type crewPaneFact struct {
	Dead        bool
	State       string // the pane's own @admiral-state
	HasActivity bool   // @admiral-activity is set
	Launched    string // the window's @admiral-launched, RFC 3339
}

// crewPaneWorking says whether a crew pane holds its lead's Stop.
func crewPaneWorking(f crewPaneFact, now time.Time) bool {
	return false
}
