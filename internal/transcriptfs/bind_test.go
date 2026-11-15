package transcriptfs

import "testing"

const bindID = "0f8fad5b-d9cb-469f-a165-70867728950e"
const bindPath = "/cfg/projects/zz/" + bindID + ".jsonl"

func TestBindAcceptsOnlyAPathTheLaunchRecordedForItsOwnSession(t *testing.T) {
	path, id, ok := Bind("1700000000000000000 launch "+bindPath, "'/bin/claude' '--session-id' '"+bindID+"' '--' 'go'")
	if !ok || path != bindPath || id != bindID {
		t.Errorf("Bind = %q, %q, %v; want %q, %q, true", path, id, ok, bindPath, bindID)
	}

	cmd := "'/bin/claude' '--session-id' '" + bindID + "' '--' 'go'"
	for name, c := range map[string]struct{ raw, cmd string }{
		"another session than the start command gave": {"1 launch " + bindPath, "'/bin/claude' '--session-id' '11111111-2222-4333-8444-555555555555' '--' 'go'"},
		"a start command with no session id":          {"1 launch " + bindPath, "'/bin/claude' '--settings' 's.json' '--' 'go'"},
		"a hook's option, not a launch's":             {"1 hook " + bindPath, cmd},
		"an option without a source":                  {bindPath, cmd},
		"an empty option":                             {"", cmd},
		"a relative path":                             {"1 launch projects/zz/" + bindID + ".jsonl", cmd},
		"a path that is not a transcript":             {"1 launch /cfg/projects/zz/" + bindID + ".txt", cmd},
		"a file name that is not a session id":        {"1 launch /cfg/projects/zz/notes.jsonl", cmd},
	} {
		if path, id, ok := Bind(c.raw, c.cmd); ok || path != "" || id != "" {
			t.Errorf("Bind with %s = %q, %q, %v; want it refused", name, path, id, ok)
		}
	}
}

func TestBindForResumedClaudeShip(t *testing.T) {
	resumed := "'/bin/claude' '--resume' '" + bindID + "' '--settings' 's.json' '--' 'go'"
	if path, id, ok := BindFor("claude", "1 launch "+bindPath, resumed); !ok || path != bindPath || id != bindID {
		t.Errorf("BindFor(claude) = %q, %q, %v; want %q, %q, true", path, id, ok, bindPath, bindID)
	}
	other := "'/bin/claude' '--resume' '11111111-2222-4333-8444-555555555555' '--' 'go'"
	if _, _, ok := BindFor("claude", "1 launch "+bindPath, other); ok {
		t.Error("BindFor(claude) bound a path for another conversation than the one resumed")
	}
	copilotPath := "/cfg/session-state/" + bindID + "/events.jsonl"
	copilotResumed := "'/bin/copilot' '--resume' '" + bindID + "' '--' 'go'"
	if _, _, ok := BindFor("copilot", "1 launch "+copilotPath, copilotResumed); ok {
		t.Error("BindFor(copilot) bound a --resume launch, want it refused")
	}
}
