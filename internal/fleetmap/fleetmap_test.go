package fleetmap_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/capabilities"
	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/fleetmap"
)

// rig holds two fleets whose roots, clone roots, owners, source hosts, docks and probed versions all differ.
type rig struct {
	home                     string
	workRoot, workClone      string
	homeRoot                 string
	mux                      *http.ServeMux
	workMarkers, homeMarkers []string
}

func checkout(t *testing.T, root, rel, remote string) {
	t.Helper()
	d := filepath.Join(root, rel)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"remote", "add", "origin", remote}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = d
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

func newRig(t *testing.T) *rig {
	t.Helper()
	base, _ := filepath.EvalSymlinks(t.TempDir())
	r := &rig{home: filepath.Join(base, "admiral"), workRoot: filepath.Join(base, "work-src"), workClone: filepath.Join(base, "work-clones"), homeRoot: filepath.Join(base, "home-src")}
	if err := os.MkdirAll(r.home, 0o755); err != nil {
		t.Fatal(err)
	}
	checkout(t, r.workRoot, "widgets", "https://github.com/acme/widgets.git")
	checkout(t, r.workClone, "gadget", "https://github.com/acme/gadget.git")
	checkout(t, r.homeRoot, "garden", "https://github.com/hobby/garden.git")
	cfg := fmt.Sprintf(`{"version":1,"fleets":{
	  "work":{"name":"Work","search_roots":[%q],"clone_root":%q,"owners":["acme"],"claude":{},"codex":{}},
	  "home":{"name":"Home","search_roots":[%q],"owners":["hobby"],"claude":{}}},
	  "sources":{
	    "gh-work":{"plugin":"github","name":"GitHub","fleet":"work","settings":{"host":"ghe.work.example"}},
	    "gh-home":{"plugin":"github","name":"GitHub","fleet":"home","settings":{"host":"ghe.home.example"}}}}`,
		r.workRoot, r.workClone, r.homeRoot)
	if err := os.WriteFile(filepath.Join(r.home, config.FileName), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := config.Open(r.home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)

	store := capabilities.Open(r.home)
	at := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	for _, c := range []struct{ fleet, harness, version string }{{"work", "claude", "2.1.300"}, {"work", "codex", "0.77.0"}, {"home", "claude", "9.9.9"}} {
		if err := store.RecordVersion(c.fleet, c.harness, c.version, at); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Record("work", "claude", "2.1.300", capabilities.AutopilotGuard, capabilities.Result{Passed: true, At: at, Detail: "denied the sentinel"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Record("home", "claude", "9.9.9", capabilities.AutopilotGuard, capabilities.Result{Passed: false, At: at, Detail: "the hook never ran"}); err != nil {
		t.Fatal(err)
	}

	r.mux = http.NewServeMux()
	(&fleetmap.Service{Home: r.home, Config: m.Current}).Routes(r.mux)
	r.workMarkers = []string{r.workRoot, r.workClone, "widgets", "gadget", "acme", "ghe.work.example", "2.1.300", "0.77.0", "denied the sentinel"}
	r.homeMarkers = []string{r.homeRoot, "garden", "hobby", "ghe.home.example", "9.9.9", "the hook never ran"}
	return r
}

func (r *rig) get(t *testing.T, path string) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	r.mux.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	return rec.Code, rec.Body.String()
}

func (r *rig) ok(t *testing.T, path string, into any) string {
	t.Helper()
	code, body := r.get(t, path)
	if code != http.StatusOK {
		t.Fatalf("GET %s = %d %s, want 200", path, code, body)
	}
	if err := json.Unmarshal([]byte(body), into); err != nil {
		t.Fatalf("GET %s is not the expected JSON: %v\n%s", path, err, body)
	}
	return body
}

type repoEntry struct {
	Path    string   `json:"path"`
	Name    string   `json:"name"`
	Kind    string   `json:"kind"`
	Remotes []string `json:"remotes"`
}

func (r *rig) repos(t *testing.T, fleet string) ([]repoEntry, string) {
	var got struct {
		Repos []repoEntry `json:"repos"`
	}
	body := r.ok(t, "/api/fleets/"+fleet+"/repos", &got)
	return got.Repos, body
}

func TestReposListAFleetsCheckoutsFromItsSearchRootsAndCloneRootWithTheirRemotes(t *testing.T) {
	r := newRig(t)
	got, _ := r.repos(t, "work")
	var widgets, gadget *repoEntry
	for i, e := range got {
		switch e.Path {
		case filepath.Join(r.workRoot, "widgets"):
			widgets = &got[i]
		case filepath.Join(r.workClone, "gadget"):
			gadget = &got[i]
		}
	}
	if widgets == nil || widgets.Name != "widgets" || widgets.Kind != "checkout" || !slices.Equal(widgets.Remotes, []string{"github.com/acme/widgets"}) {
		t.Errorf("work's repos = %+v, want the widgets checkout of its search root with its remote", got)
	}
	if gadget == nil || gadget.Kind != "checkout" || !slices.Equal(gadget.Remotes, []string{"github.com/acme/gadget"}) {
		t.Errorf("work's repos = %+v, want the gadget checkout of its clone root with its remote", got)
	}
	roots := 0
	for _, e := range got {
		if e.Kind == "search_root" {
			roots++
		}
	}
	if roots == 0 {
		t.Errorf("work's repos = %+v, want its search root listed too, as a launch can use it", got)
	}
}

func TestCloneableAnswersTheRuleNotAListHostsCloneRootAndOwners(t *testing.T) {
	r := newRig(t)
	var work, home struct {
		Hosts     []string `json:"hosts"`
		CloneRoot string   `json:"clone_root"`
		Owners    []string `json:"owners"`
	}
	r.ok(t, "/api/fleets/work/cloneable", &work)
	r.ok(t, "/api/fleets/home/cloneable", &home)
	if !slices.Contains(work.Hosts, "github.com") || !slices.Contains(work.Hosts, "ghe.work.example") || len(work.Hosts) != 2 {
		t.Errorf("work's cloneable hosts = %v, want github.com and its own GitHub source's host", work.Hosts)
	}
	if work.CloneRoot != r.workClone || !slices.Equal(work.Owners, []string{"acme"}) {
		t.Errorf("work's cloneable = %+v, want clone root %s and owners [acme]", work, r.workClone)
	}
	if home.CloneRoot != r.homeRoot {
		t.Errorf("home's clone root = %q, want its first search root %s when it sets none", home.CloneRoot, r.homeRoot)
	}
	if !slices.Contains(home.Hosts, "ghe.home.example") || slices.Contains(home.Hosts, "ghe.work.example") {
		t.Errorf("home's cloneable hosts = %v, want github.com and only its own source's host", home.Hosts)
	}
}

func TestDocksListEachHarnessWithItsProbedVersionAndCapabilities(t *testing.T) {
	r := newRig(t)
	var got struct {
		Docks []struct {
			Harness  string `json:"harness"`
			Version  string `json:"version"`
			Features []struct {
				Capabilities []struct {
					ID     string `json:"id"`
					Status string `json:"status"`
					Detail string `json:"detail"`
				} `json:"capabilities"`
			} `json:"features"`
		} `json:"docks"`
	}
	r.ok(t, "/api/fleets/work/docks", &got)
	if len(got.Docks) != 2 || got.Docks[0].Harness != "claude" || got.Docks[1].Harness != "codex" {
		t.Fatalf("work's docks = %+v, want claude then codex in the config's order", got.Docks)
	}
	if got.Docks[0].Version != "2.1.300" || got.Docks[1].Version != "0.77.0" {
		t.Errorf("work's dock versions = %q, %q; want 2.1.300 and 0.77.0", got.Docks[0].Version, got.Docks[1].Version)
	}
	found := false
	for _, f := range got.Docks[0].Features {
		for _, c := range f.Capabilities {
			if c.ID == "autopilot.guard_hook" {
				found = c.Status == "passed" && c.Detail == "denied the sentinel"
			}
		}
	}
	if !found {
		t.Errorf("work's claude dock = %+v, want autopilot.guard_hook passed with the probe's detail", got.Docks[0])
	}
}

func TestNoReadOfOneFleetCarriesAnythingOfTheOther(t *testing.T) {
	r := newRig(t)
	for _, c := range []struct {
		fleet string
		other []string
	}{{"work", r.homeMarkers}, {"home", r.workMarkers}} {
		for _, read := range []string{"repos", "cloneable", "docks"} {
			code, body := r.get(t, "/api/fleets/"+c.fleet+"/"+read)
			if code != http.StatusOK {
				t.Fatalf("GET %s %s = %d %s, want 200", c.fleet, read, code, body)
			}
			for _, m := range c.other {
				if strings.Contains(body, m) {
					t.Errorf("fleet %s's %s read holds %q of the other fleet:\n%s", c.fleet, read, m, body)
				}
			}
		}
	}
}

func TestEachReadOfAnUnknownFleetIs404(t *testing.T) {
	r := newRig(t)
	for _, read := range []string{"repos", "cloneable", "docks"} {
		if code, body := r.get(t, "/api/fleets/ghost/"+read); code != http.StatusNotFound {
			t.Errorf("GET ghost %s = %d %s, want 404", read, code, body)
		}
	}
}
