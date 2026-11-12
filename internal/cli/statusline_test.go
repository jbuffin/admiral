package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

type slRig struct {
	t         *testing.T
	env       statuslineEnv
	cfg, home string
	managed   string
	writes    [][]string
	kept      string // the file the Write seam records the kept value in
}

func newSLRig(t *testing.T) *slRig {
	t.Helper()
	dir := t.TempDir()
	r := &slRig{t: t, cfg: filepath.Join(dir, "cfg"), home: filepath.Join(dir, "home"), managed: filepath.Join(dir, "managed.json"), kept: filepath.Join(dir, "kept")}
	for _, d := range []string{r.cfg, filepath.Join(r.home, ".claude")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	r.env = statuslineEnv{
		Pane: "%7", ConfigDir: r.cfg, Home: r.home, ManagedPath: r.managed, Timeout: 2 * time.Second,
		Now: func() time.Time { return time.Date(2026, 11, 11, 20, 0, 0, 0, time.UTC) },
		Write: func(call []string) error {
			r.writes = append(r.writes, call)
			return os.WriteFile(r.kept, []byte(call[len(call)-1]), 0o600)
		},
	}
	return r
}

func settingsWith(command string) string {
	b, _ := json.Marshal(map[string]any{"statusLine": map[string]any{"type": "command", "command": command, "padding": 2}})
	return string(b)
}

func (r *slRig) writeFile(path, content string) {
	r.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

// userStatusLine installs the engineer's own status line in $CLAUDE_CONFIG_DIR.
func (r *slRig) userStatusLine(command string) {
	r.writeFile(filepath.Join(r.cfg, "settings.json"), settingsWith(command))
}

func (r *slRig) run(stdin string, args ...string) (out string) {
	r.t.Helper()
	var o, e bytes.Buffer
	if err := runStatusline(args, IO{In: strings.NewReader(stdin), Out: &o, Err: &e}, r.env); err != nil {
		r.t.Fatalf("statusline returned %v, want nil: the session must never break", err)
	}
	return o.String()
}

func sample(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("testdata/claude-statusline.json")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func (r *slRig) keptValue() map[string]any {
	r.t.Helper()
	if len(r.writes) != 1 {
		r.t.Fatalf("the wrapper made %d writes, want 1: %v", len(r.writes), r.writes)
	}
	call := r.writes[0]
	if len(call) != 6 || call[0] != "set" || call[1] != "-p" || call[2] != "-t" || call[3] != "%7" || call[4] != "@admiral-statusline" {
		r.t.Fatalf("write = %q, want one set -p -t %%7 @admiral-statusline <value>", call)
	}
	v := call[5]
	if strings.HasSuffix(v, ";") {
		r.t.Errorf("the kept value ends in %q: tmux would read it as a command separator", ";")
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(v), &m); err != nil {
		r.t.Fatalf("the kept value is not JSON: %v\n%s", err, v)
	}
	return m
}

func TestStatuslinePassesTheEngineersOutputThrough(t *testing.T) {
	r := newSLRig(t)
	const want = "\x1b[32mshipshape\x1b[0m ✓\nsecond line\n\nthird, no newline at the end"
	r.userStatusLine(`printf '\033[32mshipshape\033[0m \342\234\223\nsecond line\n\nthird, no newline at the end'`)
	if got := r.run(sample(t)); got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
	r.userStatusLine(`printf 'one line\n'`)
	if got := r.run(sample(t)); got != "one line\n" {
		t.Errorf("stdout = %q, want the newline kept: \"one line\\n\"", got)
	}
}

func TestStatuslineGivesTheEngineersCommandTheSameJSON(t *testing.T) {
	r := newSLRig(t)
	r.userStatusLine("cat")
	in := sample(t)
	if got := r.run(in); got != in {
		t.Errorf("the engineer's command read %q, want the exact JSON it was sent", got)
	}
}

func TestStatuslineNoEngineerLinePrintsNothing(t *testing.T) {
	r := newSLRig(t)
	r.writeFile(filepath.Join(r.cfg, "settings.json"), `{"theme":"dark"}`)
	if got := r.run(sample(t)); got != "" {
		t.Errorf("stdout = %q, want nothing", got)
	}
	r.keptValue()
}

func TestStatuslineFindsTheEngineersSettings(t *testing.T) {
	r := newSLRig(t)
	home := filepath.Join(r.home, ".claude", "settings.json")
	r.writeFile(home, settingsWith("echo from-home"))
	r.userStatusLine("echo from-config-dir")
	if got := r.run(sample(t)); got != "from-config-dir\n" {
		t.Errorf("with CLAUDE_CONFIG_DIR set, stdout = %q, want the line in that directory", got)
	}
	r.env.ConfigDir = ""
	if got := r.run(sample(t)); got != "from-home\n" {
		t.Errorf("with CLAUDE_CONFIG_DIR unset, stdout = %q, want ~/.claude's line", got)
	}
	r.env.ConfigDir = r.cfg
	r.writeFile(filepath.Join(r.cfg, "settings.json"), `{"theme":"dark"}`)
	if got := r.run(sample(t)); got != "" {
		t.Errorf("with CLAUDE_CONFIG_DIR set and no line in it, stdout = %q, want nothing: Claude never reads ~/.claude then", got)
	}
	r.env.ConfigDir = ""
	r.writeFile(r.managed, settingsWith("echo from-managed"))
	if got := r.run(sample(t)); got != "from-managed\n" {
		t.Errorf("with a managed line, stdout = %q, want the managed one: it outranks the user's", got)
	}
}

func TestStatuslineNeverRunsACheckoutsLine(t *testing.T) {
	r := newSLRig(t)
	checkout := t.TempDir()
	marker := filepath.Join(checkout, "ran")
	evil := settingsWith("touch " + marker + "; echo from-checkout")
	r.writeFile(filepath.Join(checkout, ".claude", "settings.json"), evil)
	r.writeFile(filepath.Join(checkout, ".claude", "settings.local.json"), evil)
	status := strings.Replace(sample(t), "/work/widget", checkout, -1)
	t.Chdir(checkout)

	if got := r.run(status); got != "" {
		t.Errorf("with only a checkout's line, stdout = %q, want nothing", got)
	}
	r.keptValue()
	r.writes = nil
	r.userStatusLine("echo from-user")
	if got := r.run(status); got != "from-user\n" {
		t.Errorf("with a user line and a checkout's, stdout = %q, want the user's", got)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("the checkout's statusLine ran: an untrusted checkout must never run code through the wrapper")
	}
}

func TestStatuslineSkipsAUserFileThatDisablesHooks(t *testing.T) {
	r := newSLRig(t)
	r.writeFile(filepath.Join(r.cfg, "settings.json"), `{"disableAllHooks":true,"statusLine":{"type":"command","command":"echo should-not-run"}}`)
	if got := r.run(sample(t)); got != "" {
		t.Errorf("stdout = %q, want nothing from a file with disableAllHooks", got)
	}
	r.keptValue()
}

func TestStatuslineDoesNotRunItself(t *testing.T) {
	r := newSLRig(t)
	bin := t.TempDir()
	marker := filepath.Join(bin, "ran")
	r.writeFile(filepath.Join(bin, "admiral"), "#!/bin/sh\ntouch "+marker+"\necho recursed\n")
	if err := os.Chmod(filepath.Join(bin, "admiral"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	r.userStatusLine("admiral statusline")
	if got := r.run(sample(t)); got != "" {
		t.Errorf("stdout = %q, want nothing", got)
	}
	r.keptValue()
	if _, err := os.Stat(marker); err == nil {
		t.Error("the wrapper ran `admiral statusline` as the engineer's own line")
	}
}

func TestStatuslineKeepsTheJSONBeforeRunningTheEngineersLine(t *testing.T) {
	r := newSLRig(t)
	r.userStatusLine("if test -s " + r.kept + "; then echo kept-first; else echo too-early; fi")
	if got := r.run(sample(t)); got != "kept-first\n" {
		t.Errorf("stdout = %q, want kept-first: Claude cancels a running line on the next update, so the JSON is kept before it runs", got)
	}
}

func TestStatuslineSurvivesAnEngineerLineThatFails(t *testing.T) {
	r := newSLRig(t)
	r.userStatusLine("echo partial; exit 3")
	if got := r.run(sample(t)); got != "" {
		t.Errorf("a failing line printed %q, want nothing, as Claude blanks the line on a non-zero exit", got)
	}
	r.keptValue()

	r2 := newSLRig(t)
	r2.userStatusLine("exec /nonexistent/status-line")
	r2.run(sample(t))
	r2.keptValue()
}

func TestStatuslineBoundsAnEngineerLineThatHangs(t *testing.T) {
	r := newSLRig(t)
	r.env.Timeout = 300 * time.Millisecond
	// The shell stays alive behind its child, so killing only the shell leaves the child holding stdout.
	pidfile := filepath.Join(t.TempDir(), "pid")
	r.userStatusLine("sleep 30 & echo $! > " + pidfile + "; wait; echo late")
	start := time.Now()
	got := r.run(sample(t))
	if el := time.Since(start); el > 3*time.Second {
		t.Errorf("the wrapper took %s with a %s bound", el, r.env.Timeout)
	}
	if got != "" {
		t.Errorf("stdout = %q, want nothing from a line that timed out", got)
	}
	if b, err := os.ReadFile(pidfile); err == nil {
		pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
		t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
		if pid > 0 && syscall.Kill(pid, 0) == nil {
			t.Errorf("the line's child %d outlived the timeout", pid)
		}
	}
	r.keptValue()
}

func TestStatuslineGarbageInKeepsNothing(t *testing.T) {
	for name, in := range map[string]string{"empty": "", "not JSON": "not json {", "array": "[1,2]", "null": "null"} {
		t.Run(name, func(t *testing.T) {
			r := newSLRig(t)
			r.userStatusLine("echo mine")
			if got := r.run(in); got != "mine\n" {
				t.Errorf("stdout = %q, want the engineer's output", got)
			}
			if len(r.writes) != 0 {
				t.Errorf("kept %v from %q, want nothing", r.writes, in)
			}
		})
	}
}

func TestStatuslineOutsideTmuxKeepsNothingAndPassesThrough(t *testing.T) {
	r := newSLRig(t)
	r.env.Pane = ""
	r.userStatusLine("echo mine")
	if got := r.run(sample(t)); got != "mine\n" {
		t.Errorf("stdout = %q, want mine", got)
	}
	if len(r.writes) != 0 {
		t.Errorf("wrote %v with no $TMUX_PANE", r.writes)
	}
}

func TestStatuslineSurvivesAFailedWrite(t *testing.T) {
	r := newSLRig(t)
	r.env.Write = func([]string) error { return os.ErrPermission }
	r.userStatusLine("echo mine")
	if got := r.run(sample(t)); got != "mine\n" {
		t.Errorf("stdout = %q, want the engineer's output even when tmux refuses the write", got)
	}
}

func TestStatuslineKeepsOnlyWhatUsageNeeds(t *testing.T) {
	r := newSLRig(t)
	r.run(sample(t))
	m := r.keptValue()

	if got := m["session_id"]; got != "5b1f0c2e-7a44-4a1e-9d0a-1c2d3e4f5a6b" {
		t.Errorf("session_id = %v", got)
	}
	if got, _ := m["transcript_path"].(string); !strings.HasSuffix(got, "5b1f0c2e-7a44-4a1e-9d0a-1c2d3e4f5a6b.jsonl") {
		t.Errorf("transcript_path = %v", m["transcript_path"])
	}
	if got, _ := m["time"].(string); got != "2026-11-11T20:00:00Z" {
		t.Errorf("time = %v, want the RFC 3339 moment it was kept", m["time"])
	}
	cost, _ := m["cost"].(map[string]any)
	if cost["total_cost_usd"] != 1.2345 {
		t.Errorf("cost = %v, want total_cost_usd 1.2345", m["cost"])
	}
	cw, _ := m["context_window"].(map[string]any)
	if cw["total_input_tokens"] != 15234.0 || cw["total_output_tokens"] != 4521.0 {
		t.Errorf("context_window = %v, want the token totals", m["context_window"])
	}
	cu, _ := cw["current_usage"].(map[string]any)
	if cu["input_tokens"] != 8500.0 || cu["output_tokens"] != 1200.0 || cu["cache_creation_input_tokens"] != 5000.0 || cu["cache_read_input_tokens"] != 2000.0 {
		t.Errorf("current_usage = %v", cw["current_usage"])
	}
	rl, _ := m["rate_limits"].(map[string]any)
	five, _ := rl["five_hour"].(map[string]any)
	seven, _ := rl["seven_day"].(map[string]any)
	if five["used_percentage"] != 23.5 || five["resets_at"] != 1900000000.0 || seven["used_percentage"] != 41.2 || seven["resets_at"] != 1900500000.0 {
		t.Errorf("rate_limits = %v", m["rate_limits"])
	}

	raw := r.writes[0][5]
	for _, banned := range []string{"session_name", "prompt_id", "fake-prompt-text-xyzzy", "Fake Model Display", "claude-fake-model", "/work/widget", "\"cwd\"", "\"model\"", "\"workspace\""} {
		if strings.Contains(raw, banned) {
			t.Errorf("the kept value holds %q: %s", banned, raw)
		}
	}
	if strings.Contains(raw, "\n") {
		t.Errorf("the kept value is not compact: %q", raw)
	}
}

func TestStatuslineKeepsAMissingWindowMissing(t *testing.T) {
	r := newSLRig(t)
	in := `{"session_id":"s1","transcript_path":"/t/s1.jsonl","cost":{"total_cost_usd":0},"context_window":{"total_input_tokens":0,"total_output_tokens":0,"current_usage":null},"rate_limits":{"five_hour":{"used_percentage":10,"resets_at":1900000000}}}`
	r.run(in)
	m := r.keptValue()
	rl, _ := m["rate_limits"].(map[string]any)
	if _, ok := rl["seven_day"]; ok || rl["five_hour"] == nil {
		t.Errorf("rate_limits = %v, want only five_hour", m["rate_limits"])
	}

	r = newSLRig(t)
	r.run(`{"session_id":"s2","transcript_path":"/t/s2.jsonl","cost":{"total_cost_usd":0.5}}`)
	m = r.keptValue()
	if _, ok := m["rate_limits"]; ok {
		t.Errorf("rate_limits = %v, want none for a plan that reports none", m["rate_limits"])
	}
}

func TestStatuslineRefusesAValueOver4KB(t *testing.T) {
	r := newSLRig(t)
	r.userStatusLine("echo mine")
	in := `{"session_id":"` + strings.Repeat("s", 5000) + `","transcript_path":"/t/` + strings.Repeat("a", 3000) + `.jsonl","cost":{"total_cost_usd":1}}`
	if got := r.run(in); got != "mine\n" {
		t.Errorf("stdout = %q, want the engineer's output", got)
	}
	for _, w := range r.writes {
		if n := len(w[len(w)-1]); n > 4096 {
			t.Errorf("kept a %d-byte value, want nothing over 4 KB", n)
		}
	}
}

// AdmiralBot runs with --setting-sources project, so Claude would load no user status line there.
func TestStatuslineChatKeepsTheJSONAndPrintsNothing(t *testing.T) {
	r := newSLRig(t)
	marker := filepath.Join(t.TempDir(), "ran")
	r.userStatusLine("touch " + marker + "; echo from-user")
	if got := r.run(sample(t), "--chat"); got != "" {
		t.Errorf("chat stdout = %q, want nothing", got)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("the chat wrapper ran the engineer's user status line")
	}
	if got, _ := r.keptValue()["session_id"].(string); got != "5b1f0c2e-7a44-4a1e-9d0a-1c2d3e4f5a6b" {
		t.Errorf("chat kept session_id %q", got)
	}
}

// The helper process runs the wrapper for real, so a signal reaches the process Claude would signal.
func TestStatuslineHelperProcess(t *testing.T) {
	if os.Getenv("ADMIRAL_TEST_STATUSLINE_HELPER") == "" {
		t.Skip("runs only as the subprocess of TestStatuslineCancelKillsTheEngineersLine")
	}
	env := statuslineEnv{ConfigDir: os.Getenv("CLAUDE_CONFIG_DIR"), Home: os.Getenv("HOME"), Timeout: time.Minute, Now: time.Now}
	_ = runStatusline(nil, IO{In: strings.NewReader(`{}`), Out: os.Stdout, Err: os.Stderr}, env)
}

func TestStatuslineCancelKillsTheEngineersLine(t *testing.T) {
	r := newSLRig(t)
	pidfile := filepath.Join(t.TempDir(), "pid")
	r.userStatusLine("sleep 60 & echo $! > " + pidfile + "; wait")
	cmd := exec.Command(os.Args[0], "-test.run=^TestStatuslineHelperProcess$")
	cmd.Env = append(os.Environ(), "ADMIRAL_TEST_STATUSLINE_HELPER=1", "CLAUDE_CONFIG_DIR="+r.cfg, "HOME="+r.home)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var pid int
	deadline := time.Now().Add(10 * time.Second)
	for pid == 0 && time.Now().Before(deadline) {
		if b, err := os.ReadFile(pidfile); err == nil {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		}
		time.Sleep(20 * time.Millisecond)
	}
	if pid == 0 {
		_ = cmd.Process.Kill()
		t.Fatal("the engineer's line never started")
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	_ = cmd.Process.Signal(syscall.SIGTERM)
	_ = cmd.Wait()
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("the engineer's child %d outlived the wrapper that Claude cancelled", pid)
}

func TestStatuslineEndsARecursionByAnySpelling(t *testing.T) {
	r := newSLRig(t)
	r.userStatusLine(`test -n "$ADMIRAL_STATUSLINE" && printf marked`)
	if got := r.run(sample(t)); got != "marked" {
		t.Errorf("stdout = %q, want the engineer's line to see ADMIRAL_STATUSLINE", got)
	}

	r = newSLRig(t)
	t.Setenv("ADMIRAL_STATUSLINE", "1")
	r.userStatusLine("echo should-not-run")
	if got := r.run(sample(t)); got != "" || len(r.writes) != 0 {
		t.Errorf("inside a status line, stdout = %q with %d writes, want nothing kept and nothing run", got, len(r.writes))
	}
	t.Setenv("ADMIRAL_STATUSLINE", "")

	// Not even the spelling check runs the line, so a fake admiral on PATH is never reached.
	bin := t.TempDir()
	marker := filepath.Join(bin, "ran")
	r.writeFile(filepath.Join(bin, "admiral"), "#!/bin/sh\ntouch "+marker+"\n")
	if err := os.Chmod(filepath.Join(bin, "admiral"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, line := range []string{"exec admiral statusline", "ADMIRAL_HOME=/x admiral statusline --chat"} {
		r.userStatusLine(line)
		r.run(sample(t))
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("the wrapper ran `admiral statusline` through exec or an env prefix")
	}
}

func TestStatuslineLeavesABackgroundChildOfALineThatExitedZero(t *testing.T) {
	r := newSLRig(t)
	pidfile := filepath.Join(t.TempDir(), "pid")
	r.userStatusLine("sleep 30 & echo $! > " + pidfile + "; printf cached")
	start := time.Now()
	if got := r.run(sample(t)); got != "cached" {
		t.Errorf("stdout = %q, want cached: the shell exited 0", got)
	}
	if time.Since(start) > 5*time.Second {
		t.Error("the wrapper waited on the background child")
	}
	b, err := os.ReadFile(pidfile)
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	if pid == 0 {
		t.Fatalf("no pid in %q", b)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	if err := syscall.Kill(pid, 0); err != nil {
		t.Errorf("the background child %d of a line that exited 0 was killed: %v", pid, err)
	}
}

func TestStatuslinePassesTheEngineersStderrThrough(t *testing.T) {
	r := newSLRig(t)
	r.userStatusLine("echo out; echo oops >&2")
	var o, e bytes.Buffer
	_ = runStatusline(nil, IO{In: strings.NewReader(sample(t)), Out: &o, Err: &e}, r.env)
	if o.String() != "out\n" || e.String() != "oops\n" {
		t.Errorf("stdout = %q, stderr = %q, want out and oops", o.String(), e.String())
	}
}

func TestStatuslineKeepsTheSpendLimit(t *testing.T) {
	r := newSLRig(t)
	r.run(`{"session_id":"s","rate_limits":{"spend_limit":{"used_usd":1.5,"limit_usd":20,"period":"month"}}}`)
	rl, _ := r.keptValue()["rate_limits"].(map[string]any)
	sp, _ := rl["spend_limit"].(map[string]any)
	if sp["used_usd"] != 1.5 || sp["limit_usd"] != 20.0 || sp["period"] != "month" {
		t.Errorf("rate_limits = %v, want the spend_limit", rl)
	}
	if _, ok := rl["five_hour"]; ok {
		t.Errorf("rate_limits = %v, want five_hour missing", rl)
	}
}

func TestStatuslineOneWrongTypedFieldDropsOnlyItself(t *testing.T) {
	r := newSLRig(t)
	r.run(`{"session_id":7,"transcript_path":"/t/s.jsonl","cost":{"total_cost_usd":"lots"},"context_window":{"total_input_tokens":10,"total_output_tokens":4},"rate_limits":{"five_hour":{"used_percentage":5,"resets_at":"soon"},"seven_day":{"used_percentage":9,"resets_at":1900000000}}}`)
	m := r.keptValue()
	if _, ok := m["session_id"]; ok {
		t.Errorf("kept session_id %v, want it dropped", m["session_id"])
	}
	if _, ok := m["cost"]; ok {
		t.Errorf("kept cost %v, want it dropped", m["cost"])
	}
	cw, _ := m["context_window"].(map[string]any)
	rl, _ := m["rate_limits"].(map[string]any)
	seven, _ := rl["seven_day"].(map[string]any)
	if m["transcript_path"] != "/t/s.jsonl" || cw["total_input_tokens"] != 10.0 || seven["resets_at"] != 1900000000.0 {
		t.Errorf("kept %v, want the good fields", m)
	}
	if _, ok := rl["five_hour"]; ok {
		t.Errorf("kept five_hour %v, want it dropped", rl["five_hour"])
	}
}

func TestStatuslineNeverReadsARelativeUserDirectory(t *testing.T) {
	r := newSLRig(t)
	checkout := t.TempDir()
	r.writeFile(filepath.Join(checkout, ".claude", "settings.json"), settingsWith("echo from-checkout"))
	status := sample(t)
	t.Chdir(checkout)
	r.env.ConfigDir, r.env.Home = "", ""
	if got := r.run(status); got != "" {
		t.Errorf("with no $HOME, stdout = %q, want nothing", got)
	}
	r.env.ConfigDir = ".claude"
	if got := r.run(status); got != "" {
		t.Errorf("with a relative $CLAUDE_CONFIG_DIR, stdout = %q, want nothing", got)
	}
}

func TestStatuslineAHungTmuxDoesNotStopTheEngineersLine(t *testing.T) {
	r := newSLRig(t)
	r.userStatusLine("echo mine")
	bin := t.TempDir()
	r.writeFile(filepath.Join(bin, "tmux"), "#!/bin/sh\nexec sleep 30\n")
	if err := os.Chmod(filepath.Join(bin, "tmux"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX_PANE", "%7")
	t.Setenv("CLAUDE_CONFIG_DIR", r.cfg)
	t.Setenv("HOME", r.home)
	var o bytes.Buffer
	start := time.Now()
	_ = Statusline(nil, IO{In: strings.NewReader(sample(t)), Out: &o, Err: &bytes.Buffer{}})
	if o.String() != "mine\n" || time.Since(start) > 8*time.Second {
		t.Errorf("stdout = %q after %s, want mine within the write's deadline", o.String(), time.Since(start))
	}
}
