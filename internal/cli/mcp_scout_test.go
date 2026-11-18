package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

func scoutArgs(extra string) string {
	return `{"dir":"/code/widget","harness":"claude","text":"find the flaky test","scout":true,"reason":"to find the flaky test"` + extra + `}`
}

func TestAScoutLaunchPostsAnAutopilotScoutAndSaysSo(t *testing.T) {
	a := newStandIn(t, func(mcpReq) (int, string) { return 201, `{"ship":{"session":"saltpaw"}}` })
	a.env(t, mcpToken)
	replies, _, err := serve(t, call(1, "launch_ship", scoutArgs("")))
	if err != nil {
		t.Fatal(err)
	}
	got := a.requests()
	if len(got) != 1 || got[0].Method != "POST" || got[0].Path != "/api/ships" {
		t.Fatalf("requests = %+v, want one POST /api/ships", got)
	}
	b := got[0].Body
	if ap, _ := b["autopilot"].(bool); !ap {
		t.Errorf("autopilot = %v, want true (body %v)", b["autopilot"], b)
	}
	if sc, _ := b["scout"].(bool); !sc {
		t.Errorf("scout = %v, want true (body %v)", b["scout"], b)
	}
	if b["fleet"] != "work" || b["dir"] != "/code/widget" || b["harness"] != "claude" || b["text"] != "find the flaky test" {
		t.Errorf("body = %v, want fleet work, the dir, harness and text", b)
	}
	if sk, _ := b["skill"].(map[string]any); sk["kind"] != "none" {
		t.Errorf("skill = %v, want kind none", b["skill"])
	}
	var out struct{ Mode string }
	tr := result(t, replies, "1")
	if err := json.Unmarshal([]byte(tr.text()), &out); err != nil || out.Mode != "scout" || tr.IsError {
		t.Errorf("result = %q, want a JSON object with mode scout", tr.text())
	}
}

func TestAScoutLaunchRefusesWhatAScoutNeverTakes(t *testing.T) {
	for _, c := range []struct{ name, extra, param string }{
		{"with me", `,"with_me":true`, "with_me"},
		{"clone", `,"clone":true`, "clone"},
		{"clone repo", `,"clone_repo":"github.com/o/r"`, "clone_repo"},
		{"skill", `,"skill":"commodore"`, "skill"},
		{"another skill", `,"skill":"tdd"`, "skill"},
		{"crew", `,"crew":["scribe"]`, "crew"},
		{"issues", `,"issues":["https://github.com/o/r/issues/1"]`, "issues"},
		{"source", `,"source":"github"`, "source"},
		{"order id", `,"order_id":"o1"`, "order_id"},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := newStandIn(t, func(mcpReq) (int, string) { return 201, `{"ship":{"session":"saltpaw"}}` })
			a.env(t, mcpToken)
			replies, _, err := serve(t, call(1, "launch_ship", scoutArgs(c.extra)))
			if err != nil {
				t.Fatal(err)
			}
			if n := len(a.requests()); n != 0 {
				t.Errorf("POSTs = %d, want none: the refusal is the tool's own", n)
			}
			tr := result(t, replies, "1")
			if !tr.IsError || !strings.Contains(tr.text(), c.param) {
				t.Errorf("result = %+v, want isError naming %s", tr, c.param)
			}
			if c.param == "with_me" && !strings.Contains(strings.ToLower(tr.text()), "engineer") {
				t.Errorf("result = %q, want it to say a scout never runs with the engineer", tr.text())
			}
		})
	}
}

func TestAScoutLaunchWithSkillNoneIsAccepted(t *testing.T) {
	a := newStandIn(t, func(mcpReq) (int, string) { return 201, `{"ship":{"session":"saltpaw"}}` })
	a.env(t, mcpToken)
	replies, _, err := serve(t, call(1, "launch_ship", scoutArgs(`,"skill":"none"`)))
	if err != nil {
		t.Fatal(err)
	}
	got := a.requests()
	if tr := result(t, replies, "1"); tr.IsError || len(got) != 1 {
		t.Fatalf("result = %+v with %d POSTs, want a launch: skill none is the scout's own skill", tr, len(got))
	}
	if sc, _ := got[0].Body["scout"].(bool); !sc {
		t.Errorf("POST = %v, want scout true", got[0].Body)
	}
}

func TestAScoutAtTheCapIsNeverRelaunchedInteractive(t *testing.T) {
	a := newStandIn(t, func(mcpReq) (int, string) {
		return 409, `{"error":"autopilot is at its cap of 3 ships"}`
	})
	a.env(t, mcpToken)
	replies, _, err := serve(t, call(1, "launch_ship", scoutArgs("")))
	if err != nil {
		t.Fatal(err)
	}
	got := a.requests()
	if len(got) != 1 {
		t.Fatalf("POSTs = %d, want 1: a scout is never relaunched with the engineer", len(got))
	}
	if sc, _ := got[0].Body["scout"].(bool); !sc {
		t.Errorf("POST = %v, want the scout request", got[0].Body)
	}
	if tr := result(t, replies, "1"); !tr.IsError || !strings.Contains(tr.text(), "cap") {
		t.Errorf("result = %+v, want isError with the server's message", tr)
	}
}

func TestLaunchShipDescribesScout(t *testing.T) {
	a := newStandIn(t, nil)
	a.env(t, mcpToken)
	replies, _, err := serve(t, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if err != nil {
		t.Fatal(err)
	}
	var list struct {
		Tools []struct {
			Name        string
			InputSchema struct {
				Properties map[string]struct{ Type, Description string }
			}
		}
	}
	if err := json.Unmarshal(replies["1"].Result, &list); err != nil {
		t.Fatal(err)
	}
	for _, tool := range list.Tools {
		if tool.Name != "launch_ship" {
			continue
		}
		p, ok := tool.InputSchema.Properties["scout"]
		if !ok || p.Description == "" {
			t.Fatalf("launch_ship properties = %v, want a described scout param", tool.InputSchema.Properties)
		}
		return
	}
	t.Fatal("launch_ship is not listed")
}
