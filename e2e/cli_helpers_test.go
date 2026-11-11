package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type cliResult struct {
	stdout, stderr string
	code           int
}

func (r cliResult) both() string { return r.stdout + r.stderr }

// sub is the world bound to a subtest's own t, so a Fatalf in the subtest ends
// that subtest and not its parent.
func (w *world) sub(t *testing.T) *world {
	c := *w
	c.t = t
	return &c
}

type cliOpts struct {
	path  []string // directories in front of the fake harnesses on PATH
	env   []string // KEY=value, last wins
	stdin string   // standard input; none when empty
}

// admiral runs bin/admiral with the world's temporary HOME and ADMIRAL_HOME,
// the fake harnesses first on PATH, no stdin and no EDITOR.
func (w *world) admiral(args ...string) cliResult {
	w.t.Helper()
	return w.admiralWith(cliOpts{}, args...)
}

func (w *world) admiralWith(o cliOpts, args ...string) cliResult {
	w.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binaryPath(w.t), args...)
	cmd.Dir = w.userHom
	if o.stdin != "" {
		cmd.Stdin = strings.NewReader(o.stdin)
	}
	var env []string
	pathDirs := append(append([]string{}, o.path...), fakeDir(w.t))
	for _, kv := range w.baseEnv() {
		k, v, _ := strings.Cut(kv, "=")
		switch k {
		case "EDITOR", "VISUAL":
			continue
		case "PATH":
			pathDirs = append(pathDirs, v)
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "PATH="+strings.Join(pathDirs, ":"))
	cmd.Env = append(env, o.env...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	res := cliResult{stdout: out.String(), stderr: errb.String()}
	var ee *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &ee) && ctx.Err() == nil:
		res.code = ee.ExitCode()
	default:
		w.t.Fatalf("admiral %v: %v (a prompt waiting on stdin? stdout %q stderr %q)", args, err, res.stdout, res.stderr)
	}
	return res
}

func (w *world) expectExit(r cliResult, want int, what string) {
	w.t.Helper()
	if r.code != want {
		w.t.Fatalf("%s: exit %d, want %d\nstdout: %s\nstderr: %s", what, r.code, want, r.stdout, r.stderr)
	}
}

func linesWith(out, prefix string) []string {
	var got []string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, prefix) {
			got = append(got, l)
		}
	}
	return got
}

// tilde expands a leading ~ to the world's HOME.
func (w *world) tilde(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		return filepath.Join(w.userHom, p[1:])
	}
	return p
}

func (w *world) writeRaw(p, content string, mode os.FileMode) {
	w.t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		w.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), mode); err != nil {
		w.t.Fatal(err)
	}
}

func (w *world) configBytes() []byte {
	w.t.Helper()
	b, err := os.ReadFile(configPath(w.home))
	if err != nil {
		w.t.Fatalf("config.json: %v", err)
	}
	return b
}

func (w *world) requireNoConfig() {
	w.t.Helper()
	if _, err := os.Stat(configPath(w.home)); err == nil {
		w.t.Fatalf("config.json was written, but nothing should have been")
	}
}

// savedConfig is config.json read so that order survives: json.Unmarshal into
// a map would sort the fleets and hide an order bug.
type savedConfig struct {
	top        map[string]json.RawMessage
	topOrder   []string
	fleetOrder []string
	fleets     map[string]map[string]json.RawMessage
}

func (w *world) readConfig() savedConfig {
	w.t.Helper()
	return parseSaved(w.t, w.configBytes())
}

func parseSaved(t *testing.T, data []byte) savedConfig {
	t.Helper()
	c := savedConfig{fleets: map[string]map[string]json.RawMessage{}}
	if err := json.Unmarshal(data, &c.top); err != nil {
		t.Fatalf("config.json is not JSON: %v\n%s", err, data)
	}
	c.topOrder = orderedKeys(t, data)
	raw := c.top["fleets"]
	c.fleetOrder = orderedKeys(t, raw)
	if err := json.Unmarshal(raw, &c.fleets); err != nil {
		t.Fatalf("fleets: %v", err)
	}
	return c
}

// orderedKeys are the keys of a JSON object in the order the text has them.
func orderedKeys(t *testing.T, raw []byte) []string {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		t.Fatalf("not a JSON object: %s", raw)
	}
	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, tok.(string))
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatal(err)
		}
	}
	return keys
}

func compactJSON(t *testing.T, raw []byte) string {
	t.Helper()
	var b bytes.Buffer
	if err := json.Compact(&b, raw); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, raw)
	}
	return b.String()
}

type dockJSON struct {
	Executable string            `json:"executable"`
	Args       []string          `json:"args"`
	Env        map[string]string `json:"env"`
}

func (c savedConfig) dock(t *testing.T, fleet, harness string) dockJSON {
	t.Helper()
	raw, ok := c.fleets[fleet][harness]
	if !ok {
		t.Fatalf("fleet %q has no %s dock; fleets: %v", fleet, harness, c.fleetOrder)
	}
	var d dockJSON
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatalf("fleets.%s.%s: %v", fleet, harness, err)
	}
	return d
}

func (c savedConfig) str(t *testing.T, fleet, field string) string {
	t.Helper()
	var s string
	if err := json.Unmarshal(c.fleets[fleet][field], &s); err != nil {
		t.Fatalf("fleets.%s.%s is not a string (%s)", fleet, field, c.fleets[fleet][field])
	}
	return s
}

func (c savedConfig) roots(t *testing.T, fleet string) []string {
	t.Helper()
	var r []string
	if err := json.Unmarshal(c.fleets[fleet]["search_roots"], &r); err != nil {
		t.Fatalf("fleets.%s.search_roots: %v", fleet, err)
	}
	return r
}

// fleetRaw is a fleet's text, compacted, to compare "left alone".
func (c savedConfig) fleetRaw(t *testing.T, key string) string {
	t.Helper()
	var all map[string]json.RawMessage
	_ = json.Unmarshal(c.top["fleets"], &all)
	return compactJSON(t, all[key])
}

// installFakeTmux records the argv of any attach call and runs the real tmux
// for everything else, so name resolution still reads the real socket.
func (w *world) installFakeTmux() (dir, rec string) {
	w.t.Helper()
	real, err := exec.LookPath("tmux")
	if err != nil {
		w.t.Fatal(err)
	}
	dir = filepath.Join(w.root, "faketmux")
	rec = filepath.Join(w.root, "attach.rec")
	_ = os.Remove(rec) // each install starts a fresh recording, as the subtests share one root
	w.writeRaw(filepath.Join(dir, "tmux"), `#!/bin/sh
for a in "$@"; do
	if [ "$a" = attach ] || [ "$a" = attach-session ]; then
		printf '%s\0' "$@" >>"$FAKE_TMUX_RECORD"
		printf '\n' >>"$FAKE_TMUX_RECORD"
		exit 0
	fi
done
exec `+real+` "$@"
`, 0o755)
	return dir, rec
}

// attachCalls are the argv of each recorded attach, without the program name.
func attachCalls(w *world, rec string) [][]string {
	w.t.Helper()
	data, err := os.ReadFile(rec)
	if err != nil {
		return nil
	}
	var calls [][]string
	for _, l := range strings.Split(string(data), "\n") {
		if l == "" {
			continue
		}
		calls = append(calls, strings.Split(strings.TrimSuffix(l, "\x00"), "\x00"))
	}
	return calls
}

// hasPair reports whether argv holds a followed immediately by b.
func hasPair(argv []string, a, b string) bool {
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == a && argv[i+1] == b {
			return true
		}
	}
	return false
}

func has(argv []string, a string) bool {
	for _, x := range argv {
		if x == a {
			return true
		}
	}
	return false
}
