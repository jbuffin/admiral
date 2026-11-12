package probes_test

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// runFakeClaude execs the fake claude with args and stdin, returning what it printed.
func runFakeClaude(t *testing.T, stdin string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, realFake(t, "claude"), args...)
	cmd.Dir = t.TempDir()
	cmd.Env = append(cmd.Environ(), "FAKE_RECORD_DIR="+filepath.Join(t.TempDir(), "rec"), "FAKE_CUE=")
	cmd.Stdin = strings.NewReader(stdin)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	cmd.WaitDelay = time.Second
	_ = cmd.Run()
	return out.String()
}

func TestTheFakeClaudeTakesAJudgeCallByItsOwnArgsNotByAnEmptyToolList(t *testing.T) {
	judge := runFakeClaude(t, "hello", "-p", "--model", "haiku", "--tools", "", "--strict-mcp-config",
		"--output-format", "json", "--no-session-persistence", "--setting-sources", "")
	if !strings.Contains(judge, `"type":"result"`) {
		t.Errorf("a judge call got %q, want its result record", judge)
	}
	chat := runFakeClaude(t, "", "--model", "sonnet", "--permission-mode", "dontAsk", "--tools", "",
		"--strict-mcp-config", "--mcp-config", "/x/mcp.json", "--setting-sources", "project")
	if strings.Contains(chat, `"type":"result"`) {
		t.Errorf("a chat with --tools \"\" was answered as a judge call: %q", chat)
	}
	probe := runFakeClaude(t, "", "-p", "Use the shell tool: touch /x/y", "--tools", "",
		"--strict-mcp-config", "--mcp-config", "/x/mcp.json", "--permission-mode", "dontAsk")
	if strings.Contains(probe, `"type":"result"`) {
		t.Errorf("a fence probe run with --tools \"\" and -p was answered as a judge call: %q", probe)
	}
}
