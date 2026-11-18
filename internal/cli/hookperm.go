package cli

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jbuffin/admiral/internal/permlog"
	"github.com/jbuffin/admiral/internal/sessionhost"
)

// openAsk is a prompt the pane's hooks have seen asked and not yet answered.
type openAsk struct {
	ID      string `json:"id"`
	Ship    string `json:"ship"`
	Key     string `json:"key"`
	Harness string `json:"harness"`
}

// logHome is the home a ship's or crew boat's hooks log to: both keep the fleet's crew directory, which lies under it.
func logHome() (string, bool) {
	crew := os.Getenv("ADMIRAL_CREW_DIR")
	fleets := filepath.Dir(filepath.Dir(crew))
	if os.Getenv("ADMIRAL_CHAT_FLEET") != "" || !filepath.IsAbs(crew) || filepath.Base(crew) != "crew" || filepath.Base(fleets) != "fleets" {
		return "", false
	}
	return filepath.Dir(fleets), true
}

type boat struct{ harness, crew, ship string }

// boatOf reads the pane's harness, crew name and ship; a crew boat is a window of its ship's session.
func boatOf(pane string) (boat, bool) {
	out, err := tmuxOut(sessionhost.BoatCall(pane)...)
	if err != nil {
		return boat{}, false
	}
	parts := strings.SplitN(out, sessionhost.RowSeparator, 3)
	if len(parts) != 3 {
		return boat{}, false
	}
	b := boat{strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), strings.TrimSpace(parts[2])}
	return b, b.ship != "" && !sessionhost.IsChatSession(b.ship)
}

// recordPermission keeps the ship's permission log from its hooks. A Codex ship's own prompts come from its
// app-server instead, which alone says whether a person or the reviewer answered.
func recordPermission(pane, event string, p hookPayload) {
	home, ok := logHome()
	if !ok || !sessionhost.SafeToken.MatchString(strings.TrimPrefix(pane, "%")) {
		return
	}
	switch event {
	case "postToolUse", "agentStop", "notification":
		recordCopilot(home, pane, p)
		return
	}
	openPath := filepath.Join(home, permlog.Dir, ".open", strings.TrimPrefix(pane, "%"))
	open := readOpen(openPath)
	switch event {
	case "PermissionRequest":
		if p.ToolName == "" || p.ToolName == "AskUserQuestion" || p.ToolName == "ExitPlanMode" {
			return
		}
		b, ok := boatOf(pane)
		if !ok || b.harness == "codex" && b.crew == "" {
			return
		}
		id := newAskID()
		call := permlog.ClaudeCall(p.ToolName, p.ToolInput, p.PermissionSuggestions)
		if call.Cwd == "" {
			call.Cwd = p.Cwd
		}
		if permlog.Append(home, b.ship, permlog.Event{Ev: "ask", ID: id, Crew: b.crew, Harness: b.harness, Call: &call}) == nil {
			writeOpen(openPath, append(open, openAsk{ID: id, Ship: b.ship, Key: askKey(p.ToolName, p.ToolInput), Harness: b.harness}))
		}
	case "PostToolUse", "PostToolUseFailure":
		key := askKey(p.ToolName, p.ToolInput)
		for i, a := range open {
			if a.Key == key {
				_ = permlog.Append(home, a.Ship, permlog.Event{Ev: "answer", ID: a.ID, Answer: permlog.Allow, By: answerer(a)})
				writeOpen(openPath, append(open[:i:i], open[i+1:]...))
				return
			}
		}
	case "Stop", "UserPromptSubmit", "Interrupt", "SessionStart":
		if len(open) == 0 || event == "Stop" && subagentRunning(p) {
			return
		}
		// The turn moved on without running the call: it was refused.
		for _, a := range open {
			_ = permlog.Append(home, a.Ship, permlog.Event{Ev: "answer", ID: a.ID, Answer: permlog.Deny, By: answerer(a)})
		}
		writeOpen(openPath, nil)
	}
}

// answerer is a person on Claude Code, whose PermissionRequest fires only for a prompt shown. Codex runs the hook
// before its reviewer, so a crew boat's hooks can't say who answered.
func answerer(a openAsk) permlog.By {
	if a.Harness == "claude" {
		return permlog.Person
	}
	return permlog.Unknown
}

func subagentRunning(p hookPayload) bool {
	for _, t := range p.BackgroundTasks {
		if t.Status == "running" && (t.Type == "subagent" || t.Type == "teammate") {
			return true
		}
	}
	return false
}

// askKey matches a PostToolUse to its prompt. Codex's PostToolUse drops the description its prompt carried, so a
// command is matched on its command alone.
func askKey(tool string, input json.RawMessage) string {
	var in struct {
		Command *string `json:"command"`
	}
	if json.Unmarshal(input, &in) == nil && in.Command != nil {
		return tool + "\x00" + *in.Command
	}
	var v any
	if json.Unmarshal(input, &v) != nil {
		return tool
	}
	b, _ := json.Marshal(v)
	return tool + "\x00" + string(b)
}

func newAskID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func readOpen(path string) []openAsk {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []openAsk
	_ = json.Unmarshal(raw, &out)
	return out
}

func writeOpen(path string, open []openAsk) {
	if len(open) == 0 {
		_ = os.Remove(path)
		return
	}
	// A pane that never answers keeps only its newest prompts.
	if len(open) > 16 {
		open = open[len(open)-16:]
	}
	raw, err := json.Marshal(open)
	if err != nil || os.MkdirAll(filepath.Dir(path), 0o700) != nil {
		return
	}
	tmp := path + ".new"
	if os.WriteFile(tmp, raw, 0o600) == nil {
		_ = os.Rename(tmp, path)
	}
}

// recordCopilot reads the permission events Copilot wrote to the session's events.jsonl since the last read.
func recordCopilot(home, pane string, p hookPayload) {
	id := p.SessionIDCopilot
	copilotHome := os.Getenv("COPILOT_HOME")
	if copilotHome == "" {
		if h, err := os.UserHomeDir(); err == nil {
			copilotHome = filepath.Join(h, ".copilot")
		}
	}
	if !copilotSessionID.MatchString(id) || !filepath.IsAbs(copilotHome) {
		return
	}
	offPath := filepath.Join(home, permlog.Dir, ".read", id)
	off := int64(0)
	if raw, err := os.ReadFile(offPath); err == nil {
		off, _ = strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
	}
	evs, next, err := copilotPermissionEvents(filepath.Join(copilotHome, "session-state", id, "events.jsonl"), off)
	if err != nil || next == off {
		return
	}
	if len(evs) > 0 {
		b, ok := boatOf(pane)
		if !ok {
			return
		}
		for i := range evs {
			evs[i].Crew = b.crew
		}
		if permlog.Append(home, b.ship, evs...) != nil {
			return
		}
	}
	if os.MkdirAll(filepath.Dir(offPath), 0o700) == nil {
		_ = os.WriteFile(offPath, []byte(strconv.FormatInt(next, 10)), 0o600)
	}
}

// copilotPermissionEvents reads whole lines from off and gives the offset after the last one.
func copilotPermissionEvents(path string, off int64) ([]permlog.Event, int64, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, off, nil
	}
	if err != nil {
		return nil, off, err
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil || st.Size() < off {
		off = 0 // a shorter file is another one
	}
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return nil, off, err
	}
	var out []permlog.Event
	br := bufio.NewReaderSize(f, 64<<10)
	for {
		line, err := br.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			// A line too long for the buffer is no permission event; skip to its end.
			n := int64(len(line))
			for errors.Is(err, bufio.ErrBufferFull) {
				line, err = br.ReadSlice('\n')
				n += int64(len(line))
			}
			if err != nil {
				return out, off, nil
			}
			off += n
			continue
		}
		if err != nil {
			return out, off, nil // a partial last line is read next time
		}
		off += int64(len(line))
		if !bytes.Contains(line, []byte(`"permission.`)) {
			continue
		}
		var l struct {
			Type      string          `json:"type"`
			Timestamp string          `json:"timestamp"`
			Data      json.RawMessage `json:"data"`
		}
		if json.Unmarshal(line, &l) != nil {
			continue
		}
		if ev, ok := permlog.CopilotEvent(l.Type, l.Timestamp, l.Data); ok {
			out = append(out, ev)
		}
	}
}
