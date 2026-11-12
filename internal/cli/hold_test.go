package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/jbuffin/admiral/internal/home"
)

// holdFake answers Admiral's Hold fire routes from a switch per fleet and a global one.
type holdFake struct {
	Port string
	mu   sync.Mutex
	reqs []seen
	on   map[string]bool // "" is the global switch
}

func (f *holdFake) mutations() []seen {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []seen
	for _, r := range f.reqs {
		if r.Method != http.MethodGet {
			out = append(out, r)
		}
	}
	return out
}

func startHoldFake(t *testing.T) *holdFake {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("ADMIRAL_HOME", dir)
	f := &holdFake{on: map[string]bool{}}
	hold := regexp.MustCompile(`^/api/(?:fleets/([^/]+)/)?hold-fire(?:/(on|off))?$`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		path := strings.Replace(r.URL.Path, "/api/v1/", "/api/", 1)
		f.mu.Lock()
		defer f.mu.Unlock()
		f.reqs = append(f.reqs, seen{r.Method, path, r.Header.Get("Authorization"), string(b)})
		w.Header().Set("Content-Type", "application/json")
		if path == "/api/ships" {
			_ = json.NewEncoder(w).Encode(map[string]any{"ships": []any{}, "home": home.ID(dir)})
			return
		}
		m := hold.FindStringSubmatch(path)
		if m == nil {
			http.NotFound(w, r)
			return
		}
		fleet, verb := m[1], m[2]
		if fleet == "ghost" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"fleet ghost not found"}`))
			return
		}
		if (verb == "") != (r.Method == http.MethodGet) {
			http.Error(w, "wrong method", http.StatusMethodNotAllowed)
			return
		}
		if verb != "" {
			f.on[fleet] = verb == "on"
		}
		sw := func(on bool) map[string]any {
			return map[string]any{"on": on, "by": "engineer", "at": "2026-11-12T08:00:00Z"}
		}
		out := sw(f.on[""])
		if fleet != "" {
			out = sw(f.on[fleet] || f.on[""])
			out["fleet"], out["global"] = sw(f.on[fleet]), sw(f.on[""])
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	f.Port = u.Port()
	return f
}

func TestHoldOnAndOffForAFleetPostToItsOwnPaths(t *testing.T) {
	f := startHoldFake(t)
	out, err := run(Hold, "on", "--fleet", "work", "--port", f.Port)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "work") || !regexp.MustCompile(`\bon\b`).MatchString(out) {
		t.Errorf("output must say Hold fire is on for work:\n%s", out)
	}
	if _, err := run(Hold, "off", "--fleet", "work", "--port", f.Port); err != nil {
		t.Fatal(err)
	}
	m := f.mutations()
	if len(m) != 2 || m[0].Method != "POST" || m[0].Path != "/api/fleets/work/hold-fire/on" ||
		m[1].Method != "POST" || m[1].Path != "/api/fleets/work/hold-fire/off" {
		t.Fatalf("mutations = %+v", m)
	}
}

func TestHoldAllTurnsTheGlobalSwitch(t *testing.T) {
	f := startHoldFake(t)
	if _, err := run(Hold, "on", "--all", "--port", f.Port); err != nil {
		t.Fatal(err)
	}
	if _, err := run(Hold, "off", "--all", "--port", f.Port); err != nil {
		t.Fatal(err)
	}
	m := f.mutations()
	if len(m) != 2 || m[0].Path != "/api/hold-fire/on" || m[1].Path != "/api/hold-fire/off" {
		t.Fatalf("mutations = %+v", m)
	}
}

func TestHoldStatusReadsTheStateWithoutChangingIt(t *testing.T) {
	f := startHoldFake(t)
	off, err := run(Hold, "status", "--fleet", "work", "--port", f.Port)
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`\boff\b`).MatchString(off) {
		t.Errorf("status of a fleet that is not held must say off:\n%s", off)
	}
	if _, err := run(Hold, "on", "--all", "--port", f.Port); err != nil {
		t.Fatal(err)
	}
	on, err := run(Hold, "status", "--fleet", "work", "--port", f.Port)
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`\bon\b`).MatchString(on) || !strings.Contains(strings.ToLower(on), "all fleets") {
		t.Errorf("a fleet held by the all-fleets switch must say so:\n%s", on)
	}
	global, err := run(Hold, "status", "--port", f.Port)
	if err != nil || !regexp.MustCompile(`\bon\b`).MatchString(global) {
		t.Errorf("status with no fleet reads the all-fleets switch: %q, %v", global, err)
	}
	if got := len(f.mutations()); got != 1 {
		t.Errorf("status changed something: %d mutations, want only the one from `hold on --all`", got)
	}
}

func TestHoldRefusesBadArgumentsAsUsageErrors(t *testing.T) {
	f := startHoldFake(t)
	if _, err := run(Hold, "on", "--fleet", "work", "--port", f.Port); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	for _, args := range [][]string{
		{},
		{"on"},
		{"off"},
		{"on", "--fleet", "work", "--all"},
		{"pause", "--fleet", "work"},
	} {
		if _, err := run(Hold, append(args, "--port", f.Port)...); !isUsage(err) {
			t.Errorf("hold %v = %v, want a usage error", args, err)
		}
	}
	if got := len(f.mutations()); got != 1 {
		t.Errorf("a refused command posted: %d mutations, want only the baseline", got)
	}
}

func TestHoldSurfacesTheServersRefusal(t *testing.T) {
	f := startHoldFake(t)
	if _, err := run(Hold, "on", "--fleet", "work", "--port", f.Port); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	_, err := run(Hold, "on", "--fleet", "ghost", "--port", f.Port)
	if err == nil || !strings.Contains(err.Error(), "fleet ghost not found") {
		t.Fatalf("hold on an unknown fleet = %v, want the server's message", err)
	}
}

func TestHoldHasUsageText(t *testing.T) {
	if u := Usage("hold"); !strings.HasPrefix(u, "usage: admiral hold") {
		t.Fatalf("Usage(hold) = %q", u)
	}
}
