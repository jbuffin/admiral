package sessionhost

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Decoded entries are compared as JSON objects.
func decoded(t *testing.T, raw string) []map[string]string {
	t.Helper()
	out := []map[string]string{}
	for _, a := range DecodeSubagents(raw) {
		b, err := json.Marshal(a)
		if err != nil {
			t.Fatal(err)
		}
		m := map[string]string{}
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}

func TestDecodeSubagentsKindAndEnd(t *testing.T) {
	const s, e = "2026-10-24T10:00:00Z", "2026-10-24T10:05:00Z"
	cases := []struct {
		name, raw string
		want      []map[string]string
	}{
		{"a list written before kinds existed still decodes", `[{"id":"a1","type":"Explore","start":"` + s + `"}]`,
			[]map[string]string{{"id": "a1", "type": "Explore", "start": s}}},
		{"every kind is kept, with its end", `[{"id":"a","type":"","start":"` + s + `","kind":"subagent"},{"id":"b","type":"","start":"` + s + `","kind":"teammate","end":"` + e + `"},` +
			`{"id":"c","type":"","start":"` + s + `","kind":"shell","end":"` + e + `"},{"id":"d","type":"","start":"` + s + `","kind":"monitor"}]`,
			[]map[string]string{
				{"id": "a", "type": "", "start": s, "kind": "subagent"},
				{"id": "b", "type": "", "start": s, "kind": "teammate", "end": e},
				{"id": "c", "type": "", "start": s, "kind": "shell", "end": e},
				{"id": "d", "type": "", "start": s, "kind": "monitor"},
			}},
		{"an unknown kind drops the entry", `[{"id":"a","type":"","start":"` + s + `","kind":"daemon"},{"id":"b","type":"","start":"` + s + `","kind":"shell"}]`,
			[]map[string]string{{"id": "b", "type": "", "start": s, "kind": "shell"}}},
		{"an end that is not RFC 3339 drops the entry", `[{"id":"a","type":"","start":"` + s + `","end":"later"},{"id":"b","type":"","start":"` + s + `","end":"` + e + `"}]`,
			[]map[string]string{{"id": "b", "type": "", "start": s, "end": e}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := decoded(t, c.raw); !reflect.DeepEqual(got, c.want) {
				t.Errorf("DecodeSubagents = %v, want %v", got, c.want)
			}
		})
	}
}

// Entry i starts at minute i; a finished entry ends at the minute its test gives.
func capList(running, finishedEnds int, finishedFirst bool) string {
	base := time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC)
	var parts []string
	add := func(i int, end string) {
		e := ""
		if end != "" {
			e = `,"end":"` + end + `"`
		}
		parts = append(parts, fmt.Sprintf(`{"id":"x%d","type":"","start":"%s","kind":"subagent"%s}`, i, base.Add(time.Duration(i)*time.Minute).Format(time.RFC3339), e))
	}
	n := 0
	run := func() {
		for i := 0; i < running; i++ {
			add(n, "")
			n++
		}
	}
	fin := func() {
		for i := 0; i < finishedEnds; i++ {
			add(n, base.Add(time.Duration(2000+i*10)*time.Minute).Format(time.RFC3339))
			n++
		}
	}
	if finishedFirst {
		fin()
		run()
	} else {
		run()
		fin()
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func TestDecodeSubagentsTrimDropsOldestFinishedFirst(t *testing.T) {
	// 60 running + 6 finished = 66: the two finished entries with the earliest ends go, not the last two listed.
	got := decoded(t, capList(60, 6, false))
	if len(got) != MaxSubagents {
		t.Fatalf("kept %d entries, want %d", len(got), MaxSubagents)
	}
	have := map[string]bool{}
	for _, m := range got {
		have[m["id"]] = true
	}
	// Finished ids x60..x65 end in that order, so x60 and x61 ended first.
	for _, id := range []string{"x60", "x61"} {
		if have[id] {
			t.Errorf("%s (earliest end) was kept; the oldest finished entries go first", id)
		}
	}
	for i := 0; i < 60; i++ {
		if !have[fmt.Sprintf("x%d", i)] {
			t.Fatalf("running entry x%d was dropped while finished ones stayed", i)
		}
	}
	for _, id := range []string{"x62", "x63", "x64", "x65"} {
		if !have[id] {
			t.Errorf("%s (a later-ending finished entry) was dropped", id)
		}
	}
}

func TestDecodeSubagentsTrimNeverDropsARunningEntryForAFinishedOne(t *testing.T) {
	// The finished entries come first, so cutting the tail would lose both running ones (x70, x71).
	got := decoded(t, capList(2, 70, true))
	have := map[string]bool{}
	for _, m := range got {
		have[m["id"]] = true
	}
	if len(got) != MaxSubagents || !have["x70"] || !have["x71"] {
		t.Errorf("kept %d entries; want %d with running x70 and x71 among them", len(got), MaxSubagents)
	}
}

func TestDecodeSubagentsTrimsToTheCapInOnePass(t *testing.T) {
	entry := func(i int, end string) string {
		e := ""
		if end != "" {
			e = `,"end":"` + end + `"`
		}
		return fmt.Sprintf(`{"id":"x%d","type":"","start":"2026-10-24T10:00:00Z","kind":"subagent"%s}`, i, e)
	}
	list := func(running, finished int) string {
		var parts []string
		for i := 0; i < running; i++ {
			parts = append(parts, entry(i, ""))
		}
		for i := 0; i < finished; i++ {
			parts = append(parts, entry(running+i, time.Date(2026, 10, 24, 11, 0, 0, 0, time.UTC).Add(time.Duration(i)*time.Second).Format(time.RFC3339)))
		}
		return "[" + strings.Join(parts, ",") + "]"
	}
	t.Run("70 running entries keep 64", func(t *testing.T) {
		if got := DecodeSubagents(list(70, 0)); len(got) != MaxSubagents {
			t.Errorf("kept %d entries, want %d", len(got), MaxSubagents)
		}
	})
	t.Run("70 entries with 3 finished drop all 3 finished and 3 running", func(t *testing.T) {
		got := DecodeSubagents(list(67, 3))
		if len(got) != MaxSubagents {
			t.Fatalf("kept %d entries, want %d", len(got), MaxSubagents)
		}
		for _, a := range got {
			if a.End != "" {
				t.Errorf("finished entry %s was kept while running ones were over the cap", a.ID)
			}
		}
	})
	t.Run("a huge list of finished entries trims to 64 without quadratic work", func(t *testing.T) {
		start := time.Now()
		got := DecodeSubagents(list(0, 20000))
		if len(got) != MaxSubagents {
			t.Fatalf("kept %d entries, want %d", len(got), MaxSubagents)
		}
		if got[0].ID != "x19936" {
			t.Errorf("first kept entry is %s, want x19936 (the 64 latest ends)", got[0].ID)
		}
		if d := time.Since(start); d > 2*time.Second {
			t.Errorf("trim took %v", d)
		}
	})
}
