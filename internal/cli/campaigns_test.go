// Tests for `admiral campaigns follow` and `admiral campaigns link` against a fake Admiral.
package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"unicode"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/home"
)

const followedIssue = "https://github.com/jbuffin/admiral/issues/900"

// campaignFake serves /api/ships for its home and answers POST /api/campaigns with status and body.
type campaignFake struct {
	port   string
	mu     sync.Mutex
	reqs   []seen
	status int
	body   string
}

func startCampaignFake(t *testing.T, dir string, status int, body string) *campaignFake {
	t.Helper()
	f := &campaignFake{status: status, body: body}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		path := strings.Replace(r.URL.Path, "/api/v1/", "/api/", 1)
		f.mu.Lock()
		f.reqs = append(f.reqs, seen{r.Method, path, r.Header.Get("Authorization"), string(b)})
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && path == "/api/ships":
			_ = json.NewEncoder(w).Encode(map[string]any{"ships": []any{}, "home": home.ID(dir)})
		case r.Method == http.MethodPost && path == "/api/campaigns":
			w.WriteHeader(f.status)
			_, _ = w.Write([]byte(f.body))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
		}
	}))
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	f.port = u.Port()
	return f
}

func (f *campaignFake) requests() []seen {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.reqs)
}

func (f *campaignFake) posts(t *testing.T) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, r := range f.requests() {
		if r.Method != http.MethodGet {
			if r.Method != http.MethodPost || r.Path != "/api/campaigns" {
				t.Errorf("request %s %s; want only POST /api/campaigns", r.Method, r.Path)
				continue
			}
			var body map[string]any
			if err := json.Unmarshal([]byte(r.Body), &body); err != nil {
				t.Fatalf("POST /api/campaigns body %q is not JSON: %v", r.Body, err)
			}
			out = append(out, body)
		}
	}
	return out
}

// campaignShip is the environment of a ship: no ADMIRAL_HOME or ADMIRAL_PORT, its home read from ADMIRAL_REPORT.
type campaignShip struct {
	name, home, user string
	lookups          [][2]string
}

func asCampaignShip(t *testing.T, fleetKey string) *campaignShip {
	t.Helper()
	s := &campaignShip{name: "saltpaw-ii", home: t.TempDir(), user: t.TempDir()}
	t.Setenv("HOME", s.user)
	t.Setenv("ADMIRAL_HOME", "")
	t.Setenv("ADMIRAL_PORT", "")
	t.Setenv("ADMIRAL_SCOUT", "")
	t.Setenv("ADMIRAL_SHIP", s.name)
	t.Setenv("ADMIRAL_REPORT", filepath.Join(s.home, "reports", s.name+".json"))
	old := shipFleet
	shipFleet = func(dir, ship string) (config.Fleet, bool) {
		s.lookups = append(s.lookups, [2]string{dir, ship})
		if fleetKey == "" {
			return config.Fleet{}, false
		}
		return config.Fleet{Key: fleetKey, Name: "Work"}, true
	}
	t.Cleanup(func() { shipFleet = old })
	return s
}

// outsideAShip is the engineer's own terminal, with Admiral's home in ADMIRAL_HOME.
func outsideAShip(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ADMIRAL_HOME", dir)
	t.Setenv("ADMIRAL_PORT", "")
	t.Setenv("ADMIRAL_SCOUT", "")
	t.Setenv("ADMIRAL_SHIP", "")
	t.Setenv("ADMIRAL_REPORT", "")
	old := shipFleet
	shipFleet = func(string, string) (config.Fleet, bool) { return config.Fleet{}, false }
	t.Cleanup(func() { shipFleet = old })
	return dir
}

func runCampaigns(args ...string) (error, string, string) {
	var out, errb bytes.Buffer
	err := Campaigns(args, IO{In: strings.NewReader(""), Out: &out, Err: &errb})
	return err, out.String(), errb.String()
}

func campaignBody(t *testing.T, fields map[string]any) string {
	t.Helper()
	view := map[string]any{"id": "c900", "url": followedIssue, "fleet": "work", "title": "Ship campaign commands", "ended": false}
	for k, v := range fields {
		view[k] = v
	}
	b, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func singleLine(t *testing.T, out string) string {
	t.Helper()
	if !strings.HasSuffix(out, "\n") || strings.Count(out, "\n") != 1 {
		t.Fatalf("stdout = %q; want exactly one line", out)
	}
	return strings.TrimSuffix(out, "\n")
}

func TestCampaignsFollowPostsTheReferenceAndTheShipsFleet(t *testing.T) {
	ship := asCampaignShip(t, "work")
	fake := startCampaignFake(t, ship.home, http.StatusCreated, campaignBody(t, nil))

	err, out, stderr := runCampaigns("follow", followedIssue, "--port", fake.port)
	if err != nil {
		t.Fatalf("follow: %v (stderr %q)", err, stderr)
	}
	posts := fake.posts(t)
	if want := []map[string]any{{"reference": followedIssue, "fleet": "work"}}; len(posts) != 1 || !jsonEqual(posts[0], want[0]) {
		t.Errorf("POST /api/campaigns bodies = %v; want %v", posts, want)
	}
	if want := [][2]string{{ship.home, ship.name}}; !slices.Equal(ship.lookups, want) {
		t.Errorf("shipFleet looked up %v; want the ship's home from ADMIRAL_REPORT, %v", ship.lookups, want)
	}
	line := singleLine(t, out)
	for _, want := range []string{followedIssue, "work", "Ship campaign commands"} {
		if !strings.Contains(line, want) {
			t.Errorf("stdout %q does not name %q", line, want)
		}
	}
	if _, err := os.Stat(filepath.Join(ship.user, ".admiral")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("follow in a ship touched ~/.admiral (stat: %v); a ship's home is the one ADMIRAL_REPORT names", err)
	}
}

func TestCampaignsFollowPrintsWhatTheServerAnswered(t *testing.T) {
	follow := func(status int, fields map[string]any) string {
		t.Helper()
		ship := asCampaignShip(t, "work")
		fake := startCampaignFake(t, ship.home, status, campaignBody(t, fields))
		err, out, stderr := runCampaigns("follow", followedIssue, "--port", fake.port)
		if err != nil {
			t.Fatalf("follow answered %d: %v (stderr %q)", status, err, stderr)
		}
		return singleLine(t, out)
	}
	created := follow(http.StatusCreated, nil)
	following := follow(http.StatusOK, nil)
	ended := follow(http.StatusOK, map[string]any{"ended": true, "end_reason": "unfollowed", "ended_at": "2026-11-18T10:00:00Z"})

	if !strings.Contains(following, followedIssue) {
		t.Errorf("200 line %q does not name the campaign %s", following, followedIssue)
	}
	if following == created {
		t.Errorf("200 and 201 print the same line %q; a 201 is newly followed", created)
	}
	// A 200 is either already followed or followed again, and the body cannot tell which.
	for _, claim := range []string{"already", "again", "newly", "ended"} {
		if strings.Contains(strings.ToLower(following), claim) {
			t.Errorf("200 line %q claims %q", following, claim)
		}
	}
	for _, want := range []string{followedIssue, "ended", "unfollowed"} {
		if !strings.Contains(ended, want) {
			t.Errorf("ended line %q does not say %q", ended, want)
		}
	}
}

func TestCampaignsFollowPrintsTheTitleAsOneLineOfData(t *testing.T) {
	ship := asCampaignShip(t, "work")
	title := "Ship\x1b[31m campaign\r\ncommands\x07now"
	fake := startCampaignFake(t, ship.home, http.StatusCreated, campaignBody(t, map[string]any{"title": title}))

	err, out, _ := runCampaigns("follow", followedIssue, "--port", fake.port)
	if err != nil {
		t.Fatal(err)
	}
	line := singleLine(t, out)
	for _, r := range line {
		if r < 0x20 || r == 0x7f {
			t.Errorf("stdout %q holds control character %U from the title", line, r)
		}
	}
	if !strings.Contains(line, "Ship") || !strings.Contains(line, "now") {
		t.Errorf("stdout %q dropped the title's text", line)
	}
}

func TestCampaignsFollowFleetFlagWins(t *testing.T) {
	ship := asCampaignShip(t, "work")
	fake := startCampaignFake(t, ship.home, http.StatusCreated, campaignBody(t, map[string]any{"fleet": "home"}))

	if err, _, stderr := runCampaigns("follow", followedIssue, "--fleet", "home", "--port", fake.port); err != nil {
		t.Fatalf("follow --fleet home: %v (stderr %q)", err, stderr)
	}
	if posts, want := fake.posts(t), map[string]any{"reference": followedIssue, "fleet": "home"}; len(posts) != 1 || !jsonEqual(posts[0], want) {
		t.Errorf("POST /api/campaigns bodies = %v; want %v", posts, want)
	}
}

func TestCampaignsFollowOutsideAShipLetsTheServerRouteTheIssue(t *testing.T) {
	dir := outsideAShip(t)
	fake := startCampaignFake(t, dir, http.StatusCreated, campaignBody(t, nil))
	t.Setenv("ADMIRAL_PORT", fake.port)

	if err, _, stderr := runCampaigns("follow", "jbuffin/admiral#900"); err != nil {
		t.Fatalf("follow with ADMIRAL_PORT and no --port: %v (stderr %q)", err, stderr)
	}
	if posts, want := fake.posts(t), map[string]any{"reference": "jbuffin/admiral#900"}; len(posts) != 1 || !jsonEqual(posts[0], want) {
		t.Errorf("POST /api/campaigns bodies = %v; want %v, with no fleet key", posts, want)
	}
}

func TestCampaignsFollowRefusalIsTheServersMessage(t *testing.T) {
	ship := asCampaignShip(t, "work")
	msg := "That campaign belongs to fleet home, not work"
	fake := startCampaignFake(t, ship.home, http.StatusUnprocessableEntity, `{"error":"`+msg+`"}`)

	err, out, _ := runCampaigns("follow", followedIssue, "--port", fake.port)
	var usage *UsageError
	switch {
	case err == nil:
		t.Fatal("follow refused with 422 returned no error; want exit 1")
	case errors.As(err, &usage):
		t.Errorf("follow refused with 422 returned a usage error %q; want a plain error (exit 1)", err)
	case err.Error() != msg:
		t.Errorf("error = %q; want the server's own text %q, which main prints as admiral: <msg>", err, msg)
	}
	if len(fake.posts(t)) != 1 {
		t.Errorf("follow sent %d POSTs; want 1", len(fake.posts(t)))
	}
	if out != "" {
		t.Errorf("stdout = %q on a refusal; want nothing", out)
	}
}

func TestCampaignsFollowRefusesInAScout(t *testing.T) {
	ship := asCampaignShip(t, "work")
	t.Setenv("ADMIRAL_SCOUT", filepath.Join(t.TempDir(), "checkout"))
	fake := startCampaignFake(t, ship.home, http.StatusCreated, campaignBody(t, nil))

	err, out, _ := runCampaigns("follow", followedIssue, "--port", fake.port)
	var usage *UsageError
	switch {
	case err == nil:
		t.Fatal("follow in a scout returned no error")
	case errors.As(err, &usage):
		t.Errorf("follow in a scout returned a usage error %q; want a plain error (exit 1)", err)
	case !strings.Contains(strings.ToLower(err.Error()), "scout"):
		t.Errorf("error = %q; want it to say a scout changes nothing", err)
	}
	if reqs := fake.requests(); len(reqs) != 0 {
		t.Errorf("follow in a scout made requests %v; want none", reqs)
	}
	if out != "" {
		t.Errorf("stdout = %q; want nothing", out)
	}
}

func TestCampaignsFollowOnlyTalksToItsOwnAdmiral(t *testing.T) {
	asCampaignShip(t, "work")
	other := startCampaignFake(t, t.TempDir(), http.StatusCreated, campaignBody(t, nil))

	err, _, _ := runCampaigns("follow", followedIssue, "--port", other.port)
	if want := "no running Admiral for this home on port " + other.port; err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("follow on another home's server: error %v; want %q", err, want)
	}
	if posts := other.posts(t); len(posts) != 0 {
		t.Errorf("follow posted %v to another home's server; want nothing", posts)
	}
}

func TestCampaignsFollowWithoutAnIssueIsAUsageError(t *testing.T) {
	ship := asCampaignShip(t, "work")
	fake := startCampaignFake(t, ship.home, http.StatusCreated, campaignBody(t, nil))

	err, _, _ := runCampaigns("follow", "--port", fake.port)
	var usage *UsageError
	if !errors.As(err, &usage) {
		t.Errorf("follow with no issue: error %v (%T); want a usage error", err, err)
	}
	if posts := fake.posts(t); len(posts) != 0 {
		t.Errorf("follow with no issue posted %v", posts)
	}
}

// newCampaignShape is the issue URL web/src/app/ordersView.ts newCampaignRefs keeps.
var newCampaignShape = regexp.MustCompile(`^https://[^/\s?#]+/[^/\s?#]+/[^/\s?#]+/issues/\d+$`)

func wantMakeCampaignLink(t *testing.T, out, fleet string, urls []string) {
	t.Helper()
	line := singleLine(t, out)
	q := url.Values{"fleet": {fleet}, "newCampaign": {strings.Join(urls, ",")}}
	if want := "/app/orders?" + q.Encode(); line != want {
		t.Errorf("link = %q; want %q", line, want)
	}
	path, raw, _ := strings.Cut(line, "?")
	if path != "/app/orders" {
		t.Fatalf("link path = %q; want /app/orders, a path and not a full URL", path)
	}
	got, err := url.ParseQuery(raw)
	if err != nil {
		t.Fatalf("link query %q: %v", raw, err)
	}
	if got.Get("fleet") != fleet {
		t.Errorf("link fleet = %q; want %q", got.Get("fleet"), fleet)
	}
	back := strings.Split(got.Get("newCampaign"), ",")
	if !slices.Equal(back, urls) {
		t.Errorf("newCampaign decodes to %q; want %q", back, urls)
	}
	for _, u := range back {
		if !newCampaignShape.MatchString(u) {
			t.Errorf("newCampaign holds %q, which the Make campaign sheet drops", u)
		}
	}
}

func TestCampaignsLinkPrintsTheMakeCampaignLink(t *testing.T) {
	ship := asCampaignShip(t, "work")
	fake := startCampaignFake(t, ship.home, http.StatusCreated, campaignBody(t, nil))
	t.Setenv("ADMIRAL_PORT", fake.port)

	err, out, stderr := runCampaigns("link",
		"https://GitHub.com/jbuffin/admiral/issues/12",
		"jbuffin/admiral#13",
		"https://GHE.Example.com:8443/Team/Tool/issues/4")
	if err != nil {
		t.Fatalf("link: %v (stderr %q)", err, stderr)
	}
	wantMakeCampaignLink(t, out, "work", []string{
		"https://github.com/jbuffin/admiral/issues/12",
		"https://github.com/jbuffin/admiral/issues/13",
		"https://ghe.example.com:8443/Team/Tool/issues/4",
	})
	if reqs := fake.requests(); len(reqs) != 0 {
		t.Errorf("link made requests %v; want none", reqs)
	}
}

func TestCampaignsLinkFleetFlagWins(t *testing.T) {
	asCampaignShip(t, "work")
	err, out, stderr := runCampaigns("link", "jbuffin/admiral#13", "--fleet", "home")
	if err != nil {
		t.Fatalf("link --fleet home: %v (stderr %q)", err, stderr)
	}
	wantMakeCampaignLink(t, out, "home", []string{"https://github.com/jbuffin/admiral/issues/13"})
}

func TestCampaignsLinkWorksInAScout(t *testing.T) {
	asCampaignShip(t, "work")
	t.Setenv("ADMIRAL_SCOUT", filepath.Join(t.TempDir(), "checkout"))
	err, out, stderr := runCampaigns("link", "jbuffin/admiral#13")
	if err != nil {
		t.Fatalf("link in a scout: %v (stderr %q)", err, stderr)
	}
	wantMakeCampaignLink(t, out, "work", []string{"https://github.com/jbuffin/admiral/issues/13"})
}

func TestCampaignsLinkWithNoFleetIsAUsageError(t *testing.T) {
	for name, setup := range map[string]func(*testing.T){
		"outside a ship":                     func(t *testing.T) { outsideAShip(t) },
		"in a ship whose fleet is not found": func(t *testing.T) { asCampaignShip(t, "") },
	} {
		t.Run(name, func(t *testing.T) {
			setup(t)
			err, out, _ := runCampaigns("link", "jbuffin/admiral#13")
			var usage *UsageError
			if !errors.As(err, &usage) || !strings.Contains(err.Error(), "--fleet") {
				t.Errorf("link with no fleet: error %v (%T); want a usage error saying to pass --fleet", err, err)
			}
			if out != "" {
				t.Errorf("stdout = %q; want nothing", out)
			}
		})
	}
}

func TestCampaignsLinkBadReferenceIsAUsageError(t *testing.T) {
	for _, bad := range []string{"not-an-issue", "https://github.com/jbuffin/admiral/pull/7", "jbuffin/admiral#0"} {
		t.Run(bad, func(t *testing.T) {
			asCampaignShip(t, "work")
			err, out, _ := runCampaigns("link", "jbuffin/admiral#13", bad)
			var usage *UsageError
			if !errors.As(err, &usage) || !strings.Contains(err.Error(), bad) {
				t.Errorf("link %s: error %v (%T); want a usage error naming it", bad, err, err)
			}
			if out != "" {
				t.Errorf("stdout = %q; want nothing", out)
			}
		})
	}
}

func TestCampaignsLinkWithoutAnIssueIsAUsageError(t *testing.T) {
	asCampaignShip(t, "work")
	err, _, _ := runCampaigns("link")
	var usage *UsageError
	if !errors.As(err, &usage) {
		t.Errorf("link with no issue: error %v (%T); want a usage error", err, err)
	}
}

func jsonEqual(a, b map[string]any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}

// wantPrintable fails on an ESC, BEL or other control or format character the server sent.
func wantPrintable(t *testing.T, where, s string) {
	t.Helper()
	for _, r := range s {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			t.Errorf("%s %q holds %U from the server", where, s, r)
		}
	}
}

func TestCampaignsFollowRefusalPrintsTheServersTextAsData(t *testing.T) {
	ship := asCampaignShip(t, "work")
	raw, _ := json.Marshal(map[string]string{"error": "Not \x1b]52;c;ZXZpbA==\x07allowed‮ here"})
	fake := startCampaignFake(t, ship.home, http.StatusUnprocessableEntity, string(raw))

	err, _, _ := runCampaigns("follow", followedIssue, "--port", fake.port)
	var usage *UsageError
	if err == nil || errors.As(err, &usage) {
		t.Fatalf("follow refused: error %v (%T); want a plain error (exit 1)", err, err)
	}
	wantPrintable(t, "error", err.Error())
	if !strings.Contains(err.Error(), "Not") || !strings.Contains(err.Error(), "allowed") {
		t.Errorf("error %q dropped the server's text", err)
	}
}

func TestCampaignsFollowRefusalWithNoTextNamesTheStatus(t *testing.T) {
	ship := asCampaignShip(t, "work")
	fake := startCampaignFake(t, ship.home, http.StatusBadGateway, "")

	err, _, _ := runCampaigns("follow", followedIssue, "--port", fake.port)
	if err == nil || !strings.Contains(err.Error(), "502") {
		t.Errorf("follow answered 502 with no body: error %v; want it to name the status", err)
	}
}

func TestCampaignsFollowPrintsTheURLAndFleetAsData(t *testing.T) {
	for _, status := range []int{http.StatusCreated, http.StatusOK} {
		ship := asCampaignShip(t, "work")
		fake := startCampaignFake(t, ship.home, status, campaignBody(t, map[string]any{"url": followedIssue + "\x1b[2J", "fleet": "wo\x1brk"}))
		err, out, _ := runCampaigns("follow", followedIssue, "--port", fake.port)
		if err != nil {
			t.Fatal(err)
		}
		wantPrintable(t, "stdout", singleLine(t, out))
	}
}

func TestCampaignsFollowEndedWithoutAReasonSaysSoOnce(t *testing.T) {
	for _, reason := range []string{"ended", "Ended", ""} {
		ship := asCampaignShip(t, "work")
		fake := startCampaignFake(t, ship.home, http.StatusOK, campaignBody(t, map[string]any{"ended": true, "end_reason": reason}))
		err, out, _ := runCampaigns("follow", followedIssue, "--port", fake.port)
		if err != nil {
			t.Fatal(err)
		}
		line := singleLine(t, out)
		if want := followedIssue + " has ended: "; !strings.HasPrefix(line, want) {
			t.Errorf("end_reason %q: line %q; want it to start %q", reason, line, want)
		}
	}
}
