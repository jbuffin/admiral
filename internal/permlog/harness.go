package permlog

import (
	"encoding/json"
	"strings"
)

// ClaudeCall is the call a Claude Code PermissionRequest names. Codex's hooks send the same shape, without suggestions.
func ClaudeCall(tool string, input, suggestions json.RawMessage) Call {
	var in struct {
		Command  string `json:"command"`
		FilePath string `json:"file_path"`
		Path     string `json:"path"`
		Notebook string `json:"notebook_path"`
		URL      string `json:"url"`
	}
	_ = json.Unmarshal(input, &in)
	c := Call{Tool: tool, Kind: "other"}
	if len(suggestions) > 0 && string(suggestions) != "null" {
		c.Suggestions = suggestions
	}
	switch {
	case tool == "Bash":
		c.Kind, c.Command = "shell", in.Command
	case tool == "apply_patch":
		c.Kind, c.Command = "write", in.Command
	case tool == "Write" || tool == "Edit" || tool == "MultiEdit" || tool == "NotebookEdit":
		c.Kind, c.Paths = "write", nonEmpty(in.FilePath, in.Notebook)
	case tool == "Read" || tool == "Glob" || tool == "Grep" || tool == "LS":
		c.Kind, c.Paths = "read", nonEmpty(in.FilePath, in.Path)
	case tool == "WebFetch":
		c.Kind, c.URLs = "url", nonEmpty(in.URL)
	case strings.HasPrefix(tool, "mcp__"):
		c.Kind, c.Command = "mcp", compact(input)
	default:
		c.Command = compact(input)
	}
	return c
}

func nonEmpty(ss ...string) []string {
	var out []string
	for _, s := range ss {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func compact(raw json.RawMessage) string {
	var v any
	if json.Unmarshal(raw, &v) != nil || v == nil {
		return ""
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// CopilotEvent reads one line of a Copilot session's events (events.jsonl, or the --ui-server's event): an ask for
// permission.requested, an answer for permission.completed, and false for anything else.
func CopilotEvent(typ, at string, data json.RawMessage) (Event, bool) {
	switch typ {
	case "permission.requested":
		var d struct {
			RequestID string `json:"requestId"`
			Req       struct {
				Kind        string          `json:"kind"`
				FullCommand string          `json:"fullCommandText"`
				FileName    string          `json:"fileName"`
				Path        string          `json:"path"`
				URL         string          `json:"url"`
				Paths       []string        `json:"possiblePaths"`
				URLs        []string        `json:"possibleUrls"`
				ServerName  string          `json:"serverName"`
				ToolName    string          `json:"toolName"`
				Args        json.RawMessage `json:"args"`
				Commands    []struct {
					Identifier string `json:"identifier"`
				} `json:"commands"`
				Segments []struct {
					Cwd string `json:"workingDirectory"`
				} `json:"commandSegments"`
			} `json:"permissionRequest"`
		}
		if json.Unmarshal(data, &d) != nil || d.RequestID == "" || d.Req.Kind == "" {
			return Event{}, false
		}
		r := d.Req
		c := Call{Tool: r.Kind, Kind: r.Kind, Command: r.FullCommand, Paths: r.Paths, URLs: r.URLs}
		for _, cmd := range r.Commands {
			c.Identifiers = append(c.Identifiers, cmd.Identifier)
		}
		if len(r.Segments) > 0 {
			c.Cwd = r.Segments[0].Cwd
		}
		switch r.Kind {
		case "shell", "read", "write", "url", "mcp":
		default:
			c.Kind = "other"
		}
		switch r.Kind {
		case "write":
			c.Paths = nonEmpty(r.FileName)
		case "read":
			c.Paths = nonEmpty(r.Path)
		case "url":
			if r.URL != "" {
				c.URLs = []string{r.URL}
			}
		case "mcp":
			c.Tool, c.Command = r.ServerName+"/"+r.ToolName, compact(r.Args)
		}
		return Event{Ev: "ask", At: at, ID: d.RequestID, Harness: "copilot", Call: &c}, true
	case "permission.completed":
		var d struct {
			RequestID string `json:"requestId"`
			Result    struct {
				Kind string `json:"kind"`
			} `json:"result"`
			Source string `json:"decisionSource"`
		}
		if json.Unmarshal(data, &d) != nil || d.RequestID == "" || d.Result.Kind == "" {
			return Event{}, false
		}
		ans := Deny
		if strings.HasPrefix(d.Result.Kind, "approved") {
			ans = Allow
		}
		var by By
		switch d.Source {
		case "human_response":
			by = Person
		case "assisted_approval":
			by = Reviewer
		case "unattended_fallback":
			by = Unattended
		default:
			// Before Copilot named its sources, only a person's menu picks said so in the result.
			switch d.Result.Kind {
			case "approved-for-session", "approved-for-location", "approved-read-only-for-session", "denied-interactively-by-user":
				by = Person
			default:
				by = Unknown
			}
		}
		return Event{Ev: "answer", At: at, ID: d.RequestID, Answer: ans, By: by}, true
	}
	return Event{}, false
}

// CodexApproval is the call a Codex app-server's approval request names.
func CodexApproval(method string, params json.RawMessage) (Call, bool) {
	var p struct {
		Command   *string  `json:"command"`
		Cwd       *string  `json:"cwd"`
		Amendment []string `json:"proposedExecpolicyAmendment"`
		Reason    *string  `json:"reason"`
		GrantRoot *string  `json:"grantRoot"`
	}
	if json.Unmarshal(params, &p) != nil {
		return Call{}, false
	}
	switch method {
	case "item/commandExecution/requestApproval":
		c := Call{Tool: "Bash", Kind: "shell", Amendment: p.Amendment}
		if p.Command != nil {
			c.Command = *p.Command
		}
		if p.Cwd != nil {
			c.Cwd = *p.Cwd
		}
		return c, true
	case "item/fileChange/requestApproval":
		c := Call{Tool: "apply_patch", Kind: "write"}
		if p.GrantRoot != nil {
			c.Paths = nonEmpty(*p.GrantRoot)
		}
		return c, true
	}
	return Call{}, false
}
