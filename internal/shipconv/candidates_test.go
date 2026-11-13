package shipconv

// PRCandidates finds the PR URLs in tool results of a ship's transcript and its crew boats'.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/jbuffin/admiral/internal/sessionhost"
	"github.com/jbuffin/admiral/internal/ships"
)

const (
	shipPR  = "https://github.com/o/r/pull/1"
	claudeP = "https://github.com/o/r/pull/2"
	codexPR = "https://github.com/o/r/pull/3"
	readPR  = "https://github.com/o/r/pull/4"
)

// bashResult is a Claude Bash tool call and its result, as the transcript holds them.
func bashResult(id, output string) string {
	call, _ := json.Marshal(map[string]any{"type": "assistant", "uuid": "a-" + id, "message": map[string]any{"content": []map[string]any{
		{"type": "tool_use", "id": id, "name": "Bash", "input": map[string]any{"command": "gh pr create"}}}}})
	res, _ := json.Marshal(map[string]any{"type": "user", "uuid": "u-" + id, "message": map[string]any{"content": []map[string]any{
		{"type": "tool_result", "tool_use_id": id, "content": output}}}})
	return string(call) + "\n" + string(res) + "\n"
}

// codexResult is a Codex exec call and its output, as a rollout holds them.
func codexResult(id, output string) string {
	call, _ := json.Marshal(map[string]any{"timestamp": "2026-11-11T20:21:00.100Z", "ordinal": 1, "type": "response_item",
		"payload": map[string]any{"type": "custom_tool_call", "id": "ctc-" + id, "status": "completed", "call_id": id, "name": "exec", "input": "gh pr create"}})
	res, _ := json.Marshal(map[string]any{"timestamp": "2026-11-11T20:21:00.200Z", "ordinal": 2, "type": "response_item",
		"payload": map[string]any{"type": "custom_tool_call_output", "id": "ctco-" + id, "call_id": id,
			"output": []map[string]any{{"type": "input_text", "text": output}}}})
	return fmt.Sprintf("%s\n%s\n", call, res)
}

func TestCandidatesCoverTheShipAndEachCrewBoat(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.ship("alpha", f.path("zz"))
	f.boats["alpha"] = []string{"rev", "cx"}
	boats := map[string]sessionhost.Crew{
		"rev": {Name: "rev", Harness: "claude", Launched: "B1", Transcript: f.path("rev")},
		"cx":  {Name: "cx", Harness: "codex", Launched: "B2", Transcript: f.codexRollout("r1")},
	}
	f.svc.crew = func(sh ships.Ship, name string) (sessionhost.Crew, string, bool) {
		b, ok := boats[name]
		return b, f.homeOf(b.Harness), ok
	}
	if err := os.MkdirAll(filepath.Dir(f.path("rev")), 0o755); err != nil {
		t.Fatal(err)
	}
	appendTo(t, f.path("zz"), bashResult("t1", "created "+shipPR)+bashResult("t2", "Title: x\n"+readPR+"\n")) // the ship's own, then one it only viewed
	appendTo(t, f.path("rev"), bashResult("t3", claudeP))
	appendTo(t, f.codexRollout("r1"), codexResult("c1", codexPR+"\n"))

	got := f.svc.PRCandidates("alpha")
	want := []string{shipPR, readPR, claudeP, codexPR}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("PRCandidates = %q, want %q", got, want)
	}
}

func TestACrewBoatWithNoTranscriptYetDoesNotHideTheShipsCandidates(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.ship("alpha", f.path("zz"))
	f.boats["alpha"] = []string{"new"}
	f.svc.crew = func(ships.Ship, string) (sessionhost.Crew, string, bool) {
		return sessionhost.Crew{Name: "new", Harness: "claude", Launched: "B3"}, f.cfg, true // launched, transcript not found yet
	}
	appendTo(t, f.path("zz"), bashResult("t1", shipPR))
	if got, want := f.svc.PRCandidates("alpha"), []string{shipPR}; !reflect.DeepEqual(got, want) {
		t.Errorf("PRCandidates = %q, want %q", got, want)
	}
	if got := f.svc.PRCandidates("nobody"); len(got) != 0 {
		t.Errorf("PRCandidates of an unknown ship = %q, want none", got)
	}
}
