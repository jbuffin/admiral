package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

type cfgDock struct {
	Harness         string   `json:"harness"`
	Executable      string   `json:"executable"`
	ExecutableFound bool     `json:"executable_found"`
	Warnings        []string `json:"warnings"`
}

type cfgFleet struct {
	Key         string    `json:"key"`
	Name        string    `json:"name"`
	Color       string    `json:"color"`
	Ink         string    `json:"ink"`
	SearchRoots []string  `json:"search_roots"`
	CanLaunch   bool      `json:"can_launch"`
	Docks       []cfgDock `json:"docks"`
}

type cfgBody struct {
	Type           string     `json:"type"`
	Fleets         []cfgFleet `json:"fleets"`
	FavoriteSkills []string   `json:"favorite_skills"`
	Error          *struct {
		File    string `json:"file"`
		Message string `json:"message"`
	} `json:"error"`
}

func (b cfgBody) keys() []string {
	k := []string{}
	for _, f := range b.Fleets {
		k = append(k, f.Key)
	}
	return k
}

func (b cfgBody) fleet(key string) cfgFleet {
	for _, f := range b.Fleets {
		if f.Key == key {
			return f
		}
	}
	return cfgFleet{}
}

func (f cfgFleet) dock(harness string) (cfgDock, bool) {
	for _, d := range f.Docks {
		if d.Harness == harness {
			return d, true
		}
	}
	return cfgDock{}, false
}

func configPath(home string) string { return filepath.Join(home, "config.json") }

func writeConfig(t *testing.T, home, content string) {
	t.Helper()
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath(home), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// replaceConfig is how most editors save: write a temp file, rename it over.
func replaceConfig(t *testing.T, home, content string) {
	t.Helper()
	tmp := filepath.Join(home, ".config.json.swp")
	if err := os.WriteFile(tmp, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, configPath(home)); err != nil {
		t.Fatal(err)
	}
}

func withVersion(fleets string) string {
	return fmt.Sprintf(`{"version":1,"fleets":{%s}}`, fleets)
}

func fetchConfig(t *testing.T, in *instance) (cfgBody, string) {
	t.Helper()
	resp, body := get(t, in.LocalURL+"/api/config", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("GET /api/config = %d, want 200: %s", resp.StatusCode, body)
	}
	var b cfgBody
	if err := json.Unmarshal([]byte(body), &b); err != nil {
		t.Fatalf("GET /api/config is not JSON: %v\n%s", err, body)
	}
	return b, body
}

// waitAPI polls GET /api/config until pred holds (a hand edit shows within
// about a second).
func waitAPI(t *testing.T, in *instance, what string, pred func(cfgBody) bool) cfgBody {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		b, raw := fetchConfig(t, in)
		if pred(b) {
			return b
		}
		if time.Now().After(deadline) {
			t.Fatalf("within 3s GET /api/config never showed %s; last body:\n%s", what, raw)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func dialWS(t *testing.T, in *instance) *websocket.Conn {
	t.Helper()
	c, _, err := wsDial(t, in.LocalURL, "", nil, "")
	if err != nil {
		t.Fatalf("websocket dial: %v", err)
	}
	return c
}

func waitWS(t *testing.T, c *websocket.Conn, d time.Duration, what string, pred func(cfgBody) bool) (cfgBody, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	var last string
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("within %s the websocket never delivered a `config` message with %s (last config message: %s): %v", d, what, last, err)
		}
		var b cfgBody
		if json.Unmarshal(data, &b) != nil || b.Type != "config" {
			continue
		}
		last = string(data)
		if pred(b) {
			return b, string(data)
		}
	}
}

func hasErr(b cfgBody) bool { return b.Error != nil }
func fleetKeys(keys ...string) func(cfgBody) bool {
	return func(b cfgBody) bool { return reflect.DeepEqual(b.keys(), keys) }
}

// A hand edit, an editor-style replace and a delete + recreate each reach
// a connected client within ~2 s, with no restart; deleting the file keeps the
// last good fleets and sets the error.
func TestConfigS2_LiveReload(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	writeConfig(t, home, withVersion(`"one":{"name":"One"}`))
	in := startAdmiral(t, home, "127.0.0.1:0")
	c := dialWS(t, in)
	waitWS(t, c, 3*time.Second, "the initial config", fleetKeys("one"))

	writeConfig(t, home, withVersion(`"one":{"name":"One"},"two":{"name":"Two"}`))
	waitWS(t, c, 2*time.Second, "fleets one, two after an in-place edit", fleetKeys("one", "two"))

	replaceConfig(t, home, withVersion(`"one":{"name":"One"},"two":{"name":"Two"},"three":{"name":"Three"}`))
	waitWS(t, c, 2*time.Second, "fleets one, two, three after a rename-over save", fleetKeys("one", "two", "three"))

	// Deleting keeps the last good fleets and sets the error; recreating clears it.
	if err := os.Remove(configPath(home)); err != nil {
		t.Fatal(err)
	}
	b, _ := waitWS(t, c, 2*time.Second, "an error after the file was deleted", hasErr)
	if !strings.Contains(b.Error.File, "config.json") || !strings.Contains(strings.ToLower(b.Error.Message), "not found") {
		t.Errorf("error = %+v, want the file named and `not found` in the message", b.Error)
	}
	if !reflect.DeepEqual(b.keys(), []string{"one", "two", "three"}) {
		t.Errorf("fleets after delete = %v, want the last good [one two three]", b.keys())
	}
	writeConfig(t, home, withVersion(`"four":{"name":"Four"}`))
	waitWS(t, c, 2*time.Second, "fleet four and no error after recreate", func(b cfgBody) bool {
		return reflect.DeepEqual(b.keys(), []string{"four"}) && b.Error == nil
	})
	if b, _ := fetchConfig(t, in); !reflect.DeepEqual(b.keys(), []string{"four"}) || b.Error != nil {
		t.Fatalf("GET /api/config fleets = %v error %+v, want [four] and null", b.keys(), b.Error)
	}
}

// A config.json that is a symlink follows edits to its target,
// including a rename-over in the target's directory and the target being
// deleted and recreated.
func TestConfigS2_SymlinkedConfigFollowsTarget(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	dotfiles := t.TempDir()
	target := filepath.Join(dotfiles, "admiral.json")
	if err := os.WriteFile(target, []byte(withVersion(`"one":{"name":"One"}`)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, configPath(home)); err != nil {
		t.Fatal(err)
	}
	in := startAdmiral(t, home, "127.0.0.1:0")
	waitAPI(t, in, "fleet one", func(b cfgBody) bool { return reflect.DeepEqual(b.keys(), []string{"one"}) })

	tmp := filepath.Join(dotfiles, "admiral.json.tmp")
	if err := os.WriteFile(tmp, []byte(withVersion(`"one":{"name":"One"},"two":{"name":"Two"}`)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, target); err != nil {
		t.Fatal(err)
	}
	waitAPI(t, in, "fleets one, two after the symlink target was replaced", func(b cfgBody) bool {
		return reflect.DeepEqual(b.keys(), []string{"one", "two"})
	})

	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	b := waitAPI(t, in, "an error after the symlink target was deleted", hasErr)
	if !reflect.DeepEqual(b.keys(), []string{"one", "two"}) {
		t.Errorf("fleets after the target was deleted = %v, want the last good [one two]", b.keys())
	}
	if err := os.WriteFile(target, []byte(withVersion(`"three":{"name":"Three"}`)), 0o600); err != nil {
		t.Fatal(err)
	}
	waitAPI(t, in, "fleet three after the symlink target was recreated", func(b cfgBody) bool {
		return reflect.DeepEqual(b.keys(), []string{"three"}) && b.Error == nil
	})
}
