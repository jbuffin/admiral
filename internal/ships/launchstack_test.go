package ships

import (
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/jbuffin/admiral/internal/launcher"
)

const (
	stackSeven = "https://github.com/acme/widgets/stack/7"
	layerFor   = "https://github.com/acme/widgets/pull/"
)

// stackOrders serves layer orders l41 and l42 of GitHub stack 7 and l50, a PR in none; it records what it was told launched.
type stackOrders struct {
	mu       sync.Mutex
	launched []string
}

func (*stackOrders) Order(source, id string) (text, title, repo, url, panel string, err *launcher.Error) {
	return "LAYER-TEXT", "Layer title", "", layerFor + strings.TrimPrefix(id, "l"), "p", nil
}

func (*stackOrders) Stack(source, id string) (number int, url string, ok bool) {
	if id == "l41" || id == "l42" {
		return 7, stackSeven, true
	}
	return 0, "", false
}

func (o *stackOrders) Launched(source, id string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.launched = append(o.launched, source+"/"+id)
}

func stackLaunch(id string, anyway bool) func(*launcher.Request) {
	return func(q *launcher.Request) {
		q.Source, q.OrderID, q.Stack, q.Anyway = "s", id, true, anyway
		q.Prompt = ""
	}
}

func TestAStackLaunchTakesTheStackURLTitleAndTextFromTheLayersServedStack(t *testing.T) {
	r := newWakeRig(t, 0)
	r.s.Orders = &stackOrders{}
	got := r.mustLaunch(t, stackLaunch("l41", false))
	if got.URL != stackSeven || got.Title != "Stack #7" || got.OrderID != stackSeven || got.Source != "s" {
		t.Errorf("ship = url %q title %q order %q source %q, want the stack URL, Stack #7, the stack URL as its order, on s", got.URL, got.Title, got.OrderID, got.Source)
	}
	argv, _ := r.started(t, 1)
	if p := prompt(argv); !strings.Contains(p, stackSeven) || strings.Contains(p, "LAYER-TEXT") {
		t.Errorf("prompt = %q, want the stack URL as the ship's text and not the layer's", p)
	}
}

func TestAStackLaunchOnAPRInNoGitHubStackIsRefusedAndListsNoShip(t *testing.T) {
	r := newWakeRig(t, 0)
	r.s.Orders = &stackOrders{}
	body := `{"fleet":"work","harness":"claude","dir":"` + r.checkout + `","skill":{"kind":"none"},"source":"s","order_id":"l50","stack":true}`
	if rec := launchPost(t, r.s, body); rec.Code != http.StatusBadRequest {
		t.Errorf("POST /api/ships with stack on a layer with no GitHub stack = %d %s, want 400", rec.Code, rec.Body)
	}
	if got := len(r.s.List()); got != 0 {
		t.Errorf("%d ships after the refusal, want none", got)
	}
}

func TestASecondLaunchOnAGitHubStackIsRefusedFromAnyLayerUnlessAnyway(t *testing.T) {
	r := newWakeRig(t, 0)
	r.s.Orders = &stackOrders{}
	first := r.mustLaunch(t, stackLaunch("l41", false))
	r.wantHeld(t, stackLaunch("l42", false), first)
	second := r.mustLaunch(t, stackLaunch("l42", true))
	if second.Session == first.Session || second.URL != stackSeven {
		t.Errorf("second ship = %s on %q, want another session on the stack URL", second.Session, second.URL)
	}
}

func TestAStackLaunchDoesNotMarkTheLayerLaunched(t *testing.T) {
	r := newWakeRig(t, 0)
	o := &stackOrders{}
	r.s.Orders = o
	r.mustLaunch(t, stackLaunch("l41", false))
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.launched) != 0 {
		t.Errorf("Launched called for %v, want no call for a GitHub stack", o.launched)
	}
}
