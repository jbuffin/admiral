package e2e

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

// urlOf is the `url` the ships API reports for one ship; present is false when
// the ship has no such field.
func (in *fleetInst) urlOf(t *testing.T, session string) (string, bool) {
	t.Helper()
	code, body := send(t, http.MethodGet, in.LocalURL+"/api/ships", nil)
	if code != 200 {
		t.Fatalf("GET /api/ships = %d: %s", code, body)
	}
	var wrap struct {
		Ships []struct {
			Session string  `json:"session"`
			URL     *string `json:"url"`
		} `json:"ships"`
	}
	if err := json.Unmarshal([]byte(body), &wrap); err != nil {
		t.Fatalf("ship list is not JSON: %v\n%s", err, body)
	}
	for _, s := range wrap.Ships {
		if s.Session == session {
			if s.URL == nil {
				return "", false
			}
			return *s.URL, true
		}
	}
	t.Fatalf("ship %s is not listed: %s", session, body)
	return "", false
}

// A ship keeps its order's web URL, across a restart; an order with no url or a
// non-web one, or a launch with no order, leaves it empty. The URL comes only
// from the order lookup, never from the launch request.
func TestShipKeepsItsOrdersURL(t *testing.T) {
	w := newWorld(t)
	p := w.newTP("a")
	p.mainOrders(
		map[string]any{"id": "web", "title": "Web order", "url": "https://example.com/issues/106"},
		map[string]any{"id": "none", "title": "No url order"},
		map[string]any{"id": "js", "title": "Script url order", "url": "javascript:alert(1)"},
		map[string]any{"id": "ftp", "title": "Ftp url order", "url": "ftp://x/y"},
	)
	w.startSources(tpSource("a", p, nil))
	w.waitSource("a", 10*time.Second, "the four orders", func(s sourceJSON) bool { return isOK(s) && len(s.orderIDs()) == 4 })

	const webURL = "https://example.com/issues/106"
	cases := []struct{ name, order, want string }{
		{"a web url", "web", webURL},
		{"no url", "none", ""},
		{"a javascript url", "js", ""},
		{"an ftp url", "ftp", ""},
	}
	ships := map[string]shipJSON{}
	for _, c := range cases {
		ships[c.order] = w.mustLaunchFrom("stay", "a", c.order)
	}

	// a launch with no order whose request nevertheless carries a url
	code, body := send(t, http.MethodPost, w.in.LocalURL+"/api/ships", map[string]any{
		"fleet": "stay", "harness": "claude", "dir": w.checkout("proj-nosource"), "skill": noSkill,
		"url": "https://example.com/x"})
	if code != http.StatusCreated {
		t.Fatalf("POST /api/ships with no order = %d, want 201: %s", code, body)
	}
	freeform := decodeShip(t, body)
	cases = append(cases, struct{ name, order, want string }{"no order but a url in the request", "", ""})
	ships[""] = freeform

	check := func(when string) {
		t.Helper()
		for _, c := range cases {
			got, present := w.in.urlOf(t, ships[c.order].Session)
			if got != c.want {
				t.Errorf("%s: ship from %s has url %q (field present %v), want %q", when, c.name, got, present, c.want)
			}
			if c.want == "" && !present {
				t.Errorf("%s: ship from %s has no url field, want an empty string", when, c.name)
			}
		}
	}
	w.in.waitShipsList(t, 10*time.Second, "all five ships", func(l []shipJSON) bool { return len(l) == 5 })
	check("after launch")

	w.restart()
	w.in.waitShipsList(t, 10*time.Second, "all five ships after the restart", func(l []shipJSON) bool { return len(l) == 5 })
	check("after a restart")
}
