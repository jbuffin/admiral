package launcher

import (
	"encoding/json"
	"regexp"
	"testing"
)

// The bundle runs the guard for the tools a scout's guard decides on, and a hook that fails blocks the call.
func TestBundleRunsTheGuardForAScoutsEditAndMCPWriteTools(t *testing.T) {
	t.Parallel()
	raw, _ := json.Marshal(claudeSettings())
	var settings struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Command   string `json:"command"`
				Timeout   int    `json:"timeout"`
				OnFailure string `json:"onFailure"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatal(err)
	}
	var guarded []*regexp.Regexp
	for _, g := range settings.Hooks["PreToolUse"] {
		for _, h := range g.Hooks {
			if h.Command != "admiral guard pretooluse" {
				continue
			}
			re, err := regexp.Compile(g.Matcher)
			if err != nil {
				t.Fatalf("matcher %q is not a regular expression: %v", g.Matcher, err)
			}
			guarded = append(guarded, re)
			if h.Timeout != 10 || h.OnFailure != "block" {
				t.Errorf("the guard on %q has timeout %d onFailure %q, want 10 and block", g.Matcher, h.Timeout, h.OnFailure)
			}
		}
	}
	matched := func(tool string) bool {
		for _, re := range guarded {
			if re.MatchString(tool) {
				return true
			}
		}
		return false
	}
	for _, tool := range []string{
		"Edit", "Write", "MultiEdit", "NotebookEdit",
		"mcp__github__create_pull_request", "mcp__github__update_pull_request", "mcp__github__merge_pull_request",
		"mcp__github__update_pull_request_branch", "mcp__github__pull_request_review_write",
		"mcp__github__add_comment_to_pending_review", "mcp__github__request_copilot_review",
		"mcp__github__push_files", "mcp__github__create_or_update_file", "mcp__github__delete_file", "mcp__github__create_branch",
		"mcp__corp_git__create_pull_request", "Monitor",
		"mcp__github__add_issue_comment", "mcp__github__issue_write", "mcp__github__update_issue", "mcp__github__add_labels", "mcp__github__assign_copilot_to_issue",
		"mcp__plugin_github_github__sub_issue_write",
	} {
		if !matched(tool) {
			t.Errorf("the guard does not run for %s, so a scout could use it", tool)
		}
	}
	for _, tool := range []string{"Read", "Grep", "Glob", "WebFetch", "Editor", "MyWrite", "mcp__github__get_pull_request", "mcp__github__list_pull_requests", "mcp__github__get_file_contents", "mcp__github__get_issue", "mcp__github__list_issues", "mcp__github__search_issues", "mcp__slack__slack_send_message"} {
		if matched(tool) {
			t.Errorf("the guard runs for %s, which no scout rule decides on", tool)
		}
	}
}
