package cli

import (
	"encoding/json"
	"io"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
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
	now := time.Now().UTC()
	call := sessionhost.SetPaneCall(pane, sessionhost.ActivityOption, now.Format(time.RFC3339))
	if valid {
		w := hookDecision(args[0], p)
		mon, held := monitorCall(pane, args[0], p)
		if w == stopWrite && held {
			w = hookWrite{}
		}
		if w == stopWrite {
			w = leadDecision(args[0], p, readCrewFacts(pane, now))
		}
		if w.state != "" {
			call = append(append(call, ";"), hookCall(pane, w)...)
			call = sessionhost.Join(call, sessionhost.UnsetPaneCall(pane, crewHeldOption))
		}
		if w.crewHeld {
			call = sessionhost.Join(call, sessionhost.SetPaneCall(pane, crewHeldOption, "1"))
		}
		if w.flagLead != "" {
			call = append(append(call, ";"), hookCall(w.flagLead, stopWrite)...)
			call = sessionhost.Join(call, sessionhost.UnsetPaneCall(w.flagLead, crewHeldOption))
		}
		if mon != nil {
			call = append(append(call, ";"), mon...)
		}
		if sub := subagentCall(pane, args[0], p, now); sub != nil {
			call = append(append(call, ";"), sub...)
		}
		if ts := transcriptCall(pane, args[0], p, now); ts != nil {
			call = append(append(call, ";"), ts...)
		}
		// Last, so a failure here can't undo the writes before it.
		if ct := crewTranscriptCall(pane, args[0], p); ct != nil {
			call = append(append(call, ";"), ct...)
		}
	}
	_ = sessionhost.WriteCurrent(call)
	return nil
}

// tmuxOut is one tmux call's stdout without its trailing newline. -u keeps
// the unit separator in a format intact. It gives up after tmuxDeadline, so a
// stuck server can't stall the reads (the final write has no deadline); the caller takes its failed-read path.
func tmuxOut(args ...string) (string, error) { return sessionhost.ReadCurrent(args...) }

// hookCall is the one tmux call that writes the reason and the state, reason
// first (for working too, as Host.setPane does) so the poll never sees
// needs_you without it. A write with ifUnset (a notification's) sets the reason
// only if none is set (-o), so permission:Bash survives Claude's
// PermissionRequest and Notification in either order; -q keeps a set reason from
// making tmux skip the state write.
func hookCall(pane string, w hookWrite) []string {
	return sessionhost.StateCall(pane, w.reason, w.ifUnset, w.state)
}

// hookPayload holds the fields of Claude Code's, Codex's and Copilot's hook
// JSON that decide a state.
type hookPayload struct {
	ToolName          string `json:"tool_name"`         // Claude Code
	ToolNameCopilot   string `json:"toolName"`          // Copilot
	NotificationType  string `json:"notification_type"` // Claude Code, Copilot
	AgentID           string `json:"agentId"`           // Copilot: set inside a subagent
	AgentTypeCopilot  string `json:"agentType"`         // Copilot
	SubagentID        string `json:"agent_id"`          // Claude Code, Codex: SubagentStart and SubagentStop
	SubagentType      string `json:"agent_type"`        // Claude Code, Codex
	TranscriptPath    string `json:"transcript_path"`   // Claude Code, Codex: any event (null reads ""); the chat's SessionStart and a crew boat's (ADR 0021)
	TranscriptCopilot string `json:"transcriptPath"`    // Copilot: a lead agentStop's, for a crew boat
	Source            string `json:"source"`            // Claude Code: SessionStart (startup, resume, clear, compact)
	// Claude Code's PostToolUse; any JSON, read only for a Monitor (monitorID).
	ToolResponse json.RawMessage `json:"tool_response"`
	// Claude Code's Stop; Codex sends none. A nil slice is "absent" and an
	// empty one is `[]`, which tells Claude's Stop from Codex's.
	BackgroundTasks []backgroundTask `json:"background_tasks"`
}

// backgroundTask is one entry of Claude Code's Stop background_tasks. A Monitor
// is type "shell", like a background Bash: only its id tells them apart.
type backgroundTask struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Status    string `json:"status"`
	AgentType string `json:"agent_type"`
}

// hookWrite is what a hook writes to the pane: the state, and for needs_you the
// short code @admiral-reason holds with it. ifUnset says the reason must not
// replace one already set: a notification is Claude's second word on a prompt
// that PermissionRequest named. crewHeld marks a lead whose Stop waits on its
// crew, and flagLead is the pane of such a lead that this event must flag.
type hookWrite struct {
	state, reason string
	ifUnset       bool
	crewHeld      bool
	flagLead      string
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
		// background shell doesn't. A running Monitor holds it too, but only
		// its recorded id tells it from a shell (monitorCall).
		for _, t := range p.BackgroundTasks {
			if t.Status == "running" && (t.Type == "subagent" || t.Type == "teammate") {
				return hookWrite{}
			}
		}
		return stopWrite
	case "preToolUse":
		if p.ToolNameCopilot == "ask_user" {
			return hookWrite{state: "needs_you", reason: "question"}
		}
	case "agentStop":
		if p.lead() {
			return stopWrite
		}
	case "userPromptSubmitted":
		if p.lead() {
			return hookWrite{state: "working"}
		}
	}
	return hookWrite{}
}

// withToken is prefix:token, or the bare prefix when token isn't short and made
// of characters tmux and a format leave alone.
func withToken(prefix, token string) string {
	if sessionhost.SafeToken.MatchString(token) {
		return prefix + ":" + token
	}
	return prefix
}

// stopWrite is what a Stop writes when nothing holds the turn open.
var stopWrite = hookWrite{state: "needs_you", reason: "stop"}

// launchWindow is how long a crew pane with no hook yet counts as working
// (ADR 0007's no-hook window): `crew launch` writes no state.
const launchWindow = 10 * time.Second

// tmuxDeadline bounds each of the hook's tmux reads.
const tmuxDeadline = 2 * time.Second

// safeID is id when it is a safe token, else "" (drop the entry).
func safeID(s string) string {
	if sessionhost.SafeToken.MatchString(s) {
		return s
	}
	return ""
}

// subagentList is @admiral-subagents after an event: the list before it, the
// harness ("claude", "codex", "copilot", or "" when tmux could not say) and
// the payload in, the new list out (ADR 0014). Ids and types come from the
// payload, so an id that is not a safe token is dropped and a type that is not
// one is stored as "".
func subagentList(event string, p hookPayload, harness string, cur []sessionhost.Subagent, now time.Time) []sessionhost.Subagent {
	listed := func(id string) int {
		for i, a := range cur {
			if a.ID == id {
				return i
			}
		}
		return -1
	}
	add := func(id, typ string) []sessionhost.Subagent {
		id = safeID(id)
		if id == "" || listed(id) >= 0 || len(cur) >= sessionhost.MaxSubagents {
			return cur
		}
		return append(append([]sessionhost.Subagent(nil), cur...),
			sessionhost.Subagent{ID: id, Type: safeID(typ), Start: now.Format(time.RFC3339)})
	}
	remove := func(id string) []sessionhost.Subagent {
		i := listed(id)
		if i < 0 {
			return cur
		}
		return append(append([]sessionhost.Subagent(nil), cur[:i]...), cur[i+1:]...)
	}
	switch event {
	case "SubagentStart":
		return add(p.SubagentID, p.SubagentType)
	case "SubagentStop":
		return remove(p.SubagentID)
	case "subagentStart":
		return add(p.AgentID, p.AgentTypeCopilot)
	case "subagentStop":
		return remove(p.AgentID)
	case "agentStop", "userPromptSubmitted":
		if p.lead() {
			return nil
		}
	case "Stop":
		// Codex's Stop has no background_tasks and must not clear the list.
		// Without a harness, a present background_tasks (even []) is Claude's.
		if harness == "claude" || (harness == "" && p.BackgroundTasks != nil) {
			var out []sessionhost.Subagent
			for _, t := range p.BackgroundTasks {
				id := safeID(t.ID)
				if t.Status != "running" || (t.Type != "subagent" && t.Type != "teammate") || id == "" || len(out) >= sessionhost.MaxSubagents {
					continue
				}
				if slices.ContainsFunc(out, func(a sessionhost.Subagent) bool { return a.ID == id }) {
					continue
				}
				if i := listed(id); i >= 0 {
					out = append(out, cur[i])
				} else {
					out = append(out, sessionhost.Subagent{ID: id, Type: safeID(t.AgentType), Start: now.Format(time.RFC3339)})
				}
			}
			return out
		}
	}
	return cur
}

// lead says the Copilot payload comes from the lead, not from inside a subagent.
func (p hookPayload) lead() bool { return p.AgentID == "" }

// subagentCall is the tmux call that writes @admiral-subagents for an event, or
// nil when it doesn't change. It reads tmux only on the events that can change
// the list: the harness for a Stop, the current list for all but a Copilot
// lead's clear-all, which unsets it whatever it holds. A failed read writes
// nothing.
func subagentCall(pane, event string, p hookPayload, now time.Time) []string {
	var cur []sessionhost.Subagent
	harness, read := "", true
	switch event {
	case "agentStop", "userPromptSubmitted":
		if !p.lead() {
			return nil
		}
		read = false
	case "SubagentStart", "SubagentStop", "subagentStart", "subagentStop", "Stop":
		if event == "Stop" {
			harness, _ = tmuxOut(sessionhost.CurrentHarnessCall(pane)...)
			if harness == "codex" {
				return nil
			}
		}
		raw, err := tmuxOut(sessionhost.PaneOptionCall(pane, sessionhost.SubagentsOption)...)
		if err != nil {
			return nil
		}
		cur = sessionhost.DecodeSubagents(raw)
	default:
		return nil
	}
	next := sessionhost.EncodeSubagents(subagentList(event, p, harness, cur, now))
	switch {
	case read && next == sessionhost.EncodeSubagents(cur):
		return nil
	case next == "":
		return sessionhost.UnsetPaneCall(pane, sessionhost.SubagentsOption)
	}
	return sessionhost.SetPaneCall(pane, sessionhost.SubagentsOption, next)
}

// monitorsOption is the pane option that holds the task ids of the Monitors a
// Claude session armed and that may still run, space-separated (#110).
const monitorsOption = sessionhost.MonitorsOption

// maxMonitors caps the list, as sessionhost.MaxSubagents does the subagents.
const maxMonitors = 64

// monitorID is the task id a PostToolUse of Claude's Monitor reports, or "" for
// any other event or tool, an id that is not a safe token, and a persistent
// Monitor: only one with a timeout is sure to wake the session again.
func monitorID(event string, p hookPayload) string {
	if event != "PostToolUse" || p.ToolName != "Monitor" {
		return ""
	}
	var r struct {
		TaskID     string `json:"taskId"`
		Persistent bool   `json:"persistent"`
	}
	if json.Unmarshal(p.ToolResponse, &r) != nil || r.Persistent {
		return ""
	}
	return safeID(r.TaskID)
}

// monitorList is @admiral-monitors after an event: a Monitor's PostToolUse adds
// its id, and a Claude Stop keeps only the ids its background_tasks lists as
// running. held says the Stop waits on one of them: the session ended its turn
// to wait for the Monitor's event, which wakes it, not for the engineer.
func monitorList(event string, p hookPayload, cur []string) (next []string, held bool) {
	if id := monitorID(event, p); id != "" {
		if slices.Contains(cur, id) {
			return cur, false
		}
		// A full list drops its oldest id, so a new Monitor is always kept.
		if len(cur) >= maxMonitors {
			cur = cur[len(cur)-maxMonitors+1:]
		}
		return append(append([]string(nil), cur...), id), false
	}
	if event != "Stop" || p.BackgroundTasks == nil {
		return cur, false
	}
	for _, id := range cur {
		if slices.ContainsFunc(p.BackgroundTasks, func(t backgroundTask) bool { return t.ID == id && t.Status == "running" }) {
			next = append(next, id)
		}
	}
	return next, len(next) > 0
}

// monitorCall is the tmux call that writes @admiral-monitors for an event, or
// nil when it doesn't change, and whether a Stop waits on a running Monitor. It
// reads tmux only for a Monitor's PostToolUse and a Claude Stop with a running
// task; a Claude Stop with none clears the list unread. A failed read writes
// nothing and holds nothing.
func monitorCall(pane, event string, p hookPayload) (call []string, held bool) {
	switch {
	case monitorID(event, p) != "":
	case event == "Stop" && slices.ContainsFunc(p.BackgroundTasks, func(t backgroundTask) bool { return t.Status == "running" }):
	case event == "Stop" && p.BackgroundTasks != nil:
		return sessionhost.UnsetPaneCall(pane, monitorsOption), false
	default:
		return nil, false
	}
	raw, err := tmuxOut(sessionhost.PaneOptionCall(pane, monitorsOption)...)
	if err != nil {
		return nil, false
	}
	cur := strings.Fields(raw)
	next, held := monitorList(event, p, cur)
	switch {
	case slices.Equal(next, cur):
		return nil, held
	case len(next) == 0:
		return sessionhost.UnsetPaneCall(pane, monitorsOption), held
	}
	return sessionhost.SetPaneCall(pane, monitorsOption, strings.Join(next, " ")), held
}

// transcriptCall is the tmux call that records a SessionStart for the chat
// (ADR 0019): the option holds "<unix nanoseconds> <source> <transcript path>"
// in one write, so a reader never sees a torn pair, and a second SessionStart on
// the same path still changes it. Only the chat's bundle registers SessionStart.
// A path sessionhost.ValidTranscriptPath refuses is not recorded (the .jsonl
// ending also keeps the argument from ending in the ";" tmux reads as a separator).
func transcriptCall(pane, event string, p hookPayload, now time.Time) []string {
	if event != "SessionStart" {
		return nil
	}
	path := p.TranscriptPath
	if !sessionhost.ValidTranscriptPath(path) {
		return nil
	}
	source := safeID(p.Source)
	if source == "" {
		source = "unknown"
	}
	return sessionhost.SetPaneCall(pane, sessionhost.TranscriptOption, strconv.FormatInt(now.UnixNano(), 10)+" "+source+" "+path)
}

// crewHeldOption marks a lead pane whose Stop waits on working crew (#189). Any
// state the lead writes next clears it. A lead held by a Monitor isn't marked:
// the Monitor wakes it.
const crewHeldOption = sessionhost.CrewHeldOption

// crewFacts is what the lead rule needs to know about the pane and its crew.
type crewFacts struct {
	ThisIsCrew     bool   // $TMUX_PANE is in a crew window
	CrewWorking    bool   // some live crew pane of the session, other than this one, is working
	CrewReadFailed bool   // the list-panes read of the crew errored
	RestingLead    string // for a crew pane: the lead whose Stop waited on the crew, once none works
}

// leadDecision is hookDecision with the lead rule on top. A crew boat's own
// Stop is idle: it waits for its lead, not the engineer (ADR 0014). A lead's
// Stop waits while a crew boat works, and the last boat to go idle then flags
// that lead, which nothing else would wake. A Stop whose crew read failed keeps
// the plain rule: a missed chime is worse than an extra one.
func leadDecision(event string, p hookPayload, f crewFacts) hookWrite {
	w := hookDecision(event, p)
	if w != stopWrite || f.CrewReadFailed {
		return w
	}
	switch {
	case f.ThisIsCrew:
		return hookWrite{state: sessionhost.CrewIdle, flagLead: f.RestingLead}
	case f.CrewWorking:
		return hookWrite{crewHeld: true}
	}
	return w
}

// crewPaneFact is one pane of a crew window as the lead rule reads it.
type crewPaneFact struct {
	Dead        bool
	State       string // the pane's own @admiral-state
	HasActivity bool   // @admiral-activity is set
	Launched    string // the window's @admiral-launched, RFC 3339
}

// crewPaneWorking says whether a crew pane holds its lead's Stop: a live pane
// whose own state is working, or one with no hook yet inside its launch window.
// A dead pane keeps its state and never counts.
func crewPaneWorking(f crewPaneFact, now time.Time) bool {
	switch {
	case f.Dead:
		return false
	case f.State == "working":
		return true
	case f.HasActivity || f.State != "":
		return false
	}
	t, err := time.Parse(time.RFC3339, f.Launched)
	return err == nil && now.Sub(t) < launchWindow
}

// readCrewFacts reads the session of pane with one list-panes call, then the
// pane's own @admiral-state (show-options -p: the format would fall back to the
// window's and the session's, which nothing sets, so it agrees with
// sessionhost.ReadCrew) of each live crew pane until one works. A crew pane reads
// the other boats only when a lead waits on the crew, and writes its own idle
// first: of two boats that stop together, the later read sees both idle.
func readCrewFacts(pane string, now time.Time) crewFacts {
	const sep = sessionhost.RowSeparator
	out, err := tmuxOut(sessionhost.SessionPanesCall(pane)...)
	if err != nil {
		return crewFacts{CrewReadFailed: true}
	}
	var f crewFacts
	own, lead := false, ""
	type row struct {
		id   string
		fact crewPaneFact
	}
	var crew []row
	for _, line := range strings.Split(out, "\n") {
		c := strings.Split(line, sep)
		if len(c) != 6 {
			continue
		}
		if c[2] == "" && c[5] != "" {
			lead = c[0]
		}
		if c[0] == pane {
			own = true
			f.ThisIsCrew = c[2] != ""
		} else if c[2] != "" {
			crew = append(crew, row{c[0], crewPaneFact{Dead: c[1] == "1", HasActivity: c[4] != "", Launched: c[3]}})
		}
	}
	if !own {
		return crewFacts{CrewReadFailed: true}
	}
	if f.ThisIsCrew {
		if lead == "" {
			return f
		}
		_, _ = tmuxOut(sessionhost.SetPaneCall(pane, sessionhost.StateOption, sessionhost.CrewIdle)...)
	}
	for _, r := range crew {
		if !r.fact.Dead {
			r.fact.State, _ = tmuxOut(sessionhost.PaneOptionCall(r.id, sessionhost.StateOption)...)
		}
		if crewPaneWorking(r.fact, now) {
			f.CrewWorking = true
			break
		}
	}
	if f.ThisIsCrew && !f.CrewWorking {
		f.RestingLead = lead
	}
	return f
}

// paneID is a tmux pane id, the only $TMUX_PANE the nested command takes.
var paneID = regexp.MustCompile(`^%[0-9]+$`)

// crewTranscriptCall is the tmux call that records a crew boat's transcript
// path in its pane option (#175, ADR 0021), or nil when the payload has none
// that sessionhost.ValidCrewTranscriptPath accepts. Claude and Codex send
// transcript_path on any event (Codex may send null); Copilot's transcriptPath
// counts only on a lead agentStop. The `if -F` runs the write only where the
// pane's window has @admiral-crew-name, so a ship's pane and the chat never get
// the option, and the path needs no escaping: the crew check refused every
// character that would need it.
func crewTranscriptCall(pane, event string, p hookPayload) []string {
	path := p.TranscriptPath
	if event == "agentStop" && p.lead() && p.TranscriptCopilot != "" {
		path = p.TranscriptCopilot
	}
	if !paneID.MatchString(pane) || !sessionhost.ValidCrewTranscriptPath(path) {
		return nil
	}
	return sessionhost.CrewTranscriptCall(pane, path)
}
