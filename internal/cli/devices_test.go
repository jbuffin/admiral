package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/home"
)

type fakeDevice struct{ ID, Name, Login string }

type seen struct{ Method, Path, Auth, Body string }

// fakeAdmiral answers the device API as the server does and records what it was asked.
type fakeAdmiral struct {
	Port string
	mu   sync.Mutex
	reqs []seen
}

func (f *fakeAdmiral) mutations() []seen {
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

func startFake(t *testing.T, devices []fakeDevice) *fakeAdmiral {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("ADMIRAL_HOME", dir)
	f := &fakeAdmiral{}
	paired := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	handler := func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		path := strings.Replace(r.URL.Path, "/api/v1/", "/api/", 1)
		f.mu.Lock()
		f.reqs = append(f.reqs, seen{r.Method, path, r.Header.Get("Authorization"), string(b)})
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case path == "/api/ships":
			_ = json.NewEncoder(w).Encode(map[string]any{"ships": []any{}, "home": home.ID(dir)})
		case path == "/api/pairing-codes" && r.Method == http.MethodPost:
			_, _ = w.Write([]byte(`{"code":"ABCD-EFGH","url":"http://192.0.2.7:7420/pair?code=ABCD-EFGH","expires":"2026-03-04T05:11:07Z"}`))
		case path == "/api/devices" && r.Method == http.MethodGet:
			out := []map[string]any{}
			for _, d := range devices {
				m := map[string]any{"id": d.ID, "name": d.Name, "pairedAt": paired, "lastSeen": paired.Add(time.Hour), "current": false}
				if d.Login != "" {
					m["login"] = d.Login
				}
				out = append(out, m)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"devices": out})
		case path == "/api/devices" && r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case strings.HasPrefix(path, "/api/devices/") && r.Method == http.MethodPatch:
			var in struct{ Name string }
			_ = json.Unmarshal(b, &in)
			_ = json.NewEncoder(w).Encode(map[string]string{"id": strings.TrimPrefix(path, "/api/devices/"), "name": in.Name})
		case strings.HasPrefix(path, "/api/devices/") && r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(handler))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	f.Port = u.Port()
	return f
}

func run(fn func([]string, IO) error, args ...string) (stdout string, err error) {
	var out bytes.Buffer
	err = fn(args, IO{In: strings.NewReader(""), Out: &out, Err: &bytes.Buffer{}})
	return out.String(), err
}

func isUsage(err error) bool {
	var u *UsageError
	return errors.As(err, &u)
}

var tablet = fakeDevice{"d-aaa111", "Tablet", ""}
var phone = fakeDevice{"d-bbb222", "Phone", "jason@example.test"}

func TestPairPrintsTheUrlCodeExpiryAndTheSingleUseRule(t *testing.T) {
	f := startFake(t, nil)
	out, err := run(Pair, "--port", f.Port)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"http://192.0.2.7:7420/pair?code=ABCD-EFGH", "ABCD-EFGH"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	low := strings.ToLower(out)
	if !strings.Contains(low, "expires") || !regexp.MustCompile(`\d{1,2}:\d\d`).MatchString(out) {
		t.Errorf("output must say when the code expires, as a clock time:\n%s", out)
	}
	if !strings.Contains(low, "single-use") || !strings.Contains(low, "another") || !strings.Contains(low, "void") {
		t.Errorf("output must say the code is single-use and that minting another voids it:\n%s", out)
	}
}

func TestPairMintsWithAPostAndSendsNoToken(t *testing.T) {
	f := startFake(t, nil)
	if _, err := run(Pair, "--port", f.Port); err != nil {
		t.Fatal(err)
	}
	m := f.mutations()
	if len(m) != 1 || m[0].Method != http.MethodPost || m[0].Path != "/api/pairing-codes" {
		t.Fatalf("requests that changed state = %+v, want one POST /api/pairing-codes", m)
	}
	if m[0].Auth != "" {
		t.Errorf("pair sent Authorization %q; the CLI is local and needs no token", m[0].Auth)
	}
}

func TestPairJSONPrintsTheServersBody(t *testing.T) {
	f := startFake(t, nil)
	out, err := run(Pair, "--json", "--port", f.Port)
	if err != nil {
		t.Fatal(err)
	}
	var got struct{ Code, URL, Expires string }
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("--json output is not JSON: %v\n%s", err, out)
	}
	if got.Code != "ABCD-EFGH" || got.Expires != "2026-03-04T05:11:07Z" || !strings.HasSuffix(got.URL, "/pair?code=ABCD-EFGH") {
		t.Errorf("--json body = %+v", got)
	}
}

func TestPairAndDevicesWithoutAServerForThisHomeFailWithExit1(t *testing.T) {
	t.Setenv("ADMIRAL_HOME", t.TempDir())
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(l.Addr().String())
	l.Close()
	for _, c := range []struct {
		fn   func([]string, IO) error
		args []string
	}{
		{Pair, []string{"--port", port}},
		{Devices, []string{"--port", port}},
		{Devices, []string{"revoke", "Tablet", "--port", port}},
		{Devices, []string{"rename", "Tablet", "Slate", "--port", port}},
	} {
		_, err := run(c.fn, c.args...)
		if err == nil || isUsage(err) || !strings.Contains(err.Error(), "no running Admiral for this home") {
			t.Errorf("%v: err = %v, want a plain error saying there is no running Admiral for this home", c.args, err)
		}
	}
}

func TestPairTakesNoPositionals(t *testing.T) {
	f := startFake(t, nil)
	if _, err := run(Pair, "extra", "--port", f.Port); !isUsage(err) {
		t.Errorf("pair extra: err = %v, want a usage error (exit 2)", err)
	}
	if len(f.mutations()) != 0 {
		t.Error("a code was minted although the arguments were wrong")
	}
}

func TestDevicesListsOneTabSeparatedLineEach(t *testing.T) {
	f := startFake(t, []fakeDevice{tablet, phone})
	out, err := run(Devices, "--port", f.Port)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %d:\n%q", len(lines), out)
	}
	for i, want := range []fakeDevice{tablet, phone} {
		cols := strings.Split(lines[i], "\t")
		if len(cols) != 5 {
			t.Fatalf("line %d has %d columns, want NAME LOGIN PAIRED LAST-SEEN ID: %q", i, len(cols), lines[i])
		}
		if cols[0] != want.Name || cols[1] != want.Login || cols[4] != want.ID {
			t.Errorf("line %d = %q, want name %q, login %q, id %q", i, cols, want.Name, want.Login, want.ID)
		}
		for _, c := range cols[2:4] {
			if c == "" || strings.Contains(c, "T05:06:07Z") {
				t.Errorf("line %d: time column %q must be readable, not the raw RFC 3339 stamp", i, c)
			}
		}
	}
}

func TestDevicesWithNoneListedPrintsNothingAndSucceeds(t *testing.T) {
	f := startFake(t, nil)
	out, err := run(Devices, "--port", f.Port)
	if err != nil || out != "" {
		t.Errorf("out = %q, err = %v, want nothing and no error", out, err)
	}
}

func TestDevicesJSONPrintsTheServersBody(t *testing.T) {
	f := startFake(t, []fakeDevice{tablet, phone})
	out, err := run(Devices, "--json", "--port", f.Port)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Devices []struct{ ID, Name, Login string }
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil || len(got.Devices) != 2 || got.Devices[1].ID != phone.ID || got.Devices[1].Login != phone.Login {
		t.Errorf("--json output = %q (err %v), want the server's {\"devices\":[…]}", out, err)
	}
}

func TestDevicesListKeepsAControlCharacterOutOfTheColumns(t *testing.T) {
	f := startFake(t, []fakeDevice{{"d-ccc333", "Ev\til\nname", ""}})
	out, err := run(Devices, "--port", f.Port)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(out, "\n") != 1 || len(strings.Split(strings.TrimSuffix(out, "\n"), "\t")) != 5 {
		t.Errorf("a name with a tab and a newline broke the row: %q", out)
	}
}

func TestDevicesRenameByNameAndById(t *testing.T) {
	for _, target := range []string{"Tablet", "d-aaa111"} {
		f := startFake(t, []fakeDevice{tablet, phone})
		out, err := run(Devices, "rename", target, "Slate", "--port", f.Port)
		if err != nil {
			t.Fatalf("rename %s: %v", target, err)
		}
		m := f.mutations()
		if len(m) != 1 || m[0].Method != http.MethodPatch || m[0].Path != "/api/devices/d-aaa111" {
			t.Fatalf("rename %s sent %+v, want one PATCH /api/devices/d-aaa111", target, m)
		}
		var body struct{ Name string }
		if json.Unmarshal([]byte(m[0].Body), &body) != nil || body.Name != "Slate" {
			t.Errorf("PATCH body = %q, want {\"name\":\"Slate\"}", m[0].Body)
		}
		if !strings.Contains(out, "Slate") {
			t.Errorf("output %q does not say what it did", out)
		}
	}
}

func TestDevicesFlagsComeBeforeOrAfterThePositionals(t *testing.T) {
	for _, order := range []string{"before", "after"} {
		f := startFake(t, []fakeDevice{tablet, phone})
		var rename, revoke []string
		if order == "before" {
			rename = []string{"rename", "--port", f.Port, "Tablet", "Slate"}
			revoke = []string{"revoke", "--port", f.Port, "Phone"}
		} else {
			rename = []string{"rename", "Tablet", "Slate", "--port", f.Port}
			revoke = []string{"revoke", "Phone", "--port", f.Port}
		}
		if _, err := run(Devices, rename...); err != nil {
			t.Errorf("rename, port %s: %v", order, err)
		}
		if _, err := run(Devices, revoke...); err != nil {
			t.Errorf("revoke, port %s: %v", order, err)
		}
		if n := len(f.mutations()); n != 2 {
			t.Errorf("port %s: %d changes sent, want 2", order, n)
		}
	}
}

func TestDevicesRevokeByNameAndById(t *testing.T) {
	for _, target := range []string{"Phone", "d-bbb222"} {
		f := startFake(t, []fakeDevice{tablet, phone})
		out, err := run(Devices, "revoke", target, "--port", f.Port)
		if err != nil {
			t.Fatalf("revoke %s: %v", target, err)
		}
		m := f.mutations()
		if len(m) != 1 || m[0].Method != http.MethodDelete || m[0].Path != "/api/devices/d-bbb222" {
			t.Fatalf("revoke %s sent %+v, want one DELETE /api/devices/d-bbb222", target, m)
		}
		if !strings.Contains(out, "Phone") {
			t.Errorf("output %q does not name the device it revoked", out)
		}
	}
}

func TestDevicesRevokeAllRevokesEveryDevice(t *testing.T) {
	f := startFake(t, []fakeDevice{tablet, phone})
	out, err := run(Devices, "revoke", "--all", "--port", f.Port)
	if err != nil {
		t.Fatal(err)
	}
	m := f.mutations()
	if len(m) != 1 || m[0].Method != http.MethodDelete || m[0].Path != "/api/devices" {
		t.Fatalf("revoke --all sent %+v, want one DELETE /api/devices", m)
	}
	if strings.TrimSpace(out) == "" {
		t.Error("revoke --all printed nothing; it should say what it did")
	}
}

func TestDevicesNameMatchesExactlyNotAsAPrefix(t *testing.T) {
	f := startFake(t, []fakeDevice{{"d-1", "Safari", ""}, {"d-2", "Safari on Mac", ""}})
	if _, err := run(Devices, "revoke", "Safari", "--port", f.Port); err != nil {
		t.Fatal(err)
	}
	if m := f.mutations(); len(m) != 1 || m[0].Path != "/api/devices/d-1" {
		t.Errorf("revoke Safari sent %+v, want only d-1", m)
	}
}

func TestDevicesUsageErrorsExit2AndChangeNothing(t *testing.T) {
	f := startFake(t, []fakeDevice{tablet, phone})
	p := []string{"--port", f.Port}
	for name, args := range map[string][]string{
		"an unknown subcommand":                   {"frobnicate"},
		"rename with no names":                    {"rename"},
		"rename with one name":                    {"rename", "Tablet"},
		"rename with three":                       {"rename", "Tablet", "Slate", "extra"},
		"revoke with neither a name nor --all":    {"revoke"},
		"revoke with a name and --all":            {"revoke", "Tablet", "--all"},
		"revoke with two names":                   {"revoke", "Tablet", "Phone"},
		"revoke --all with a name after the flag": {"revoke", "--all", "Tablet"},
	} {
		if _, err := run(Devices, append(args, p...)...); !isUsage(err) {
			t.Errorf("%s: err = %v, want a usage error (exit 2)", name, err)
		}
	}
	if n := len(f.mutations()); n != 0 {
		t.Errorf("%d changes were sent although every call was malformed", n)
	}
}

func TestDevicesUnknownNameOrIdExits1AndChangesNothing(t *testing.T) {
	f := startFake(t, []fakeDevice{tablet, phone})
	for _, args := range [][]string{{"revoke", "Nope"}, {"rename", "Nope", "Slate"}} {
		_, err := run(Devices, append(args, "--port", f.Port)...)
		if err == nil || isUsage(err) || !strings.Contains(err.Error(), "Nope") {
			t.Errorf("%v: err = %v, want a plain error naming %q", args, err, "Nope")
		}
	}
	if n := len(f.mutations()); n != 0 {
		t.Errorf("%d changes were sent for a device that does not exist", n)
	}
}

func TestDevicesAmbiguousNameExits1ListsTheIdsAndChangesNothing(t *testing.T) {
	f := startFake(t, []fakeDevice{{"d-111", "Safari on Mac", ""}, {"d-222", "Safari on Mac", ""}, tablet})
	for _, args := range [][]string{{"revoke", "Safari on Mac"}, {"rename", "Safari on Mac", "Slate"}} {
		_, err := run(Devices, append(args, "--port", f.Port)...)
		if err == nil || isUsage(err) {
			t.Fatalf("%v: err = %v, want a plain error (exit 1)", args, err)
		}
		for _, id := range []string{"d-111", "d-222"} {
			if !strings.Contains(err.Error(), id) {
				t.Errorf("%v: error %q does not list the id %s to use instead", args, err, id)
			}
		}
	}
	if n := len(f.mutations()); n != 0 {
		t.Errorf("%d changes were sent for an ambiguous name", n)
	}
}

func TestPairAndDevicesHaveUsageText(t *testing.T) {
	if u := Usage("pair"); !strings.HasPrefix(u, "usage: admiral pair") || !strings.Contains(u, "--json") {
		t.Errorf("Usage(pair) = %q", u)
	}
	u := Usage("devices")
	for _, want := range []string{"usage: admiral devices", "rename", "revoke", "--all", "--json"} {
		if !strings.Contains(u, want) {
			t.Errorf("Usage(devices) lacks %q: %q", want, u)
		}
	}
	top := Usage("")
	for _, want := range []string{"pair", "devices"} {
		if !strings.Contains(top, want) {
			t.Errorf("the top-level usage lacks %q: %q", want, top)
		}
	}
}
