package crew

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

const casesDir = "testdata/crew-resolution/cases"

type caseFile struct {
	Admiral bool   `json:"admiral"`
	Run     string `json:"run"`
	Repo    string `json:"repo"`
}

// resolveJSON marshals Resolve's result and drops every message, as the fixtures README says to compare.
func resolveJSON(t *testing.T, in Input) map[string]any {
	t.Helper()
	raw, err := json.Marshal(Resolve(in))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	stripMessages(got)
	return got
}

func stripMessages(obj map[string]any) {
	for _, key := range []string{"errors", "warnings"} {
		items, _ := obj[key].([]any)
		for _, item := range items {
			if m, ok := item.(map[string]any); ok {
				delete(m, "message")
			}
		}
	}
}

func normalize(t *testing.T, v any) any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func pretty(v any) string {
	raw, _ := json.MarshalIndent(v, "", "  ")
	return string(raw)
}

func TestFixtureCases(t *testing.T) {
	entries, err := os.ReadDir(casesDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		t.Run(name, func(t *testing.T) {
			dir, err := filepath.Abs(filepath.Join(casesDir, name))
			if err != nil {
				t.Fatal(err)
			}
			var c caseFile
			readJSON(t, filepath.Join(dir, "case.json"), &c)
			var want map[string]any
			readJSON(t, filepath.Join(dir, "expected.json"), &want)

			in := Input{Admiral: c.Admiral, Layers: map[string]string{}, Run: c.Run, Repo: c.Repo}
			layers, err := os.ReadDir(filepath.Join(dir, "layers"))
			if err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			for _, l := range layers {
				in.Layers[l.Name()] = filepath.Join(dir, "layers", l.Name())
			}

			got := resolveJSON(t, in)
			if !reflect.DeepEqual(normalize(t, got), normalize(t, want)) {
				t.Errorf("result differs\ngot:\n%s\nwant:\n%s", pretty(got), pretty(want))
			}
		})
	}
}

func readJSON(t *testing.T, path string, into any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}
