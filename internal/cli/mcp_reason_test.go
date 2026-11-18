package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestALaunchWithoutAReasonIsRefusedNamingItAndNeverPosted(t *testing.T) {
	for name, args := range map[string]string{
		"missing": `{"dir":"/code/widget","harness":"claude","skill":"none","text":"fix the build"}`,
		"empty":   `{"dir":"/code/widget","harness":"claude","skill":"none","text":"fix the build","reason":""}`,
		"blank":   `{"dir":"/code/widget","harness":"claude","skill":"none","text":"fix the build","reason":"  "}`,
		"scout":   `{"dir":"/code/widget","harness":"claude","text":"find it","scout":true}`,
	} {
		t.Run(name, func(t *testing.T) {
			a := newStandIn(t, func(mcpReq) (int, string) { return 201, `{"ship":{"session":"saltpaw"}}` })
			a.env(t, mcpToken)
			replies, _, err := serve(t, call(1, "launch_ship", args))
			if err != nil {
				t.Fatal(err)
			}
			if tr := result(t, replies, "1"); !tr.IsError || !strings.Contains(tr.text(), "reason") {
				t.Errorf("result = %+v, want isError naming reason", tr)
			}
			if got := a.requests(); len(got) != 0 {
				t.Errorf("requests = %+v, want none", got)
			}
		})
	}
}

func TestALaunchesReasonTravelsToTheServerRequest(t *testing.T) {
	a := newStandIn(t, func(mcpReq) (int, string) { return 201, `{"ship":{"session":"saltpaw"}}` })
	a.env(t, mcpToken)
	if _, _, err := serve(t, call(1, "launch_ship", `{"dir":"/code/widget","harness":"claude","skill":"none","text":"fix the build","reason":"to read the AWS logs"}`)); err != nil {
		t.Fatal(err)
	}
	got := a.requests()
	if len(got) != 1 || got[0].Body["reason"] != "to read the AWS logs" {
		t.Fatalf("requests = %+v, want one POST whose body carries reason %q", got, "to read the AWS logs")
	}
}

func TestLaunchShipListsReasonAsARequiredDescribedInput(t *testing.T) {
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
				Required   []string
				Properties map[string]struct{ Type, Description string }
			}
		}
	}
	if err := json.Unmarshal(replies["1"].Result, &list); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tool := range list.Tools {
		if tool.Name != "launch_ship" {
			continue
		}
		found = true
		p, ok := tool.InputSchema.Properties["reason"]
		if !ok || p.Type != "string" || p.Description == "" {
			t.Errorf("launch_ship properties = %v, want a described string reason", tool.InputSchema.Properties)
		}
		req := false
		for _, r := range tool.InputSchema.Required {
			req = req || r == "reason"
		}
		if !req {
			t.Errorf("launch_ship required = %v, want reason in it", tool.InputSchema.Required)
		}
	}
	if !found {
		t.Fatal("launch_ship is not listed")
	}
}
