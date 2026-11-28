package ships

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/sessionhost"
)

func shipJSON(t *testing.T, sh Ship) map[string]any {
	t.Helper()
	raw, err := json.Marshal(sh)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func (r *handRig) saltpaw(t *testing.T) map[string]any {
	t.Helper()
	for _, sh := range r.s.List() {
		if sh.Session == "saltpaw" {
			return shipJSON(t, sh)
		}
	}
	t.Fatal("saltpaw is not listed")
	return nil
}

func TestACodexShipCarriesWhyItCannotBeHandedAndAClaudeShipDoesNot(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	codex := shipJSON(t, FromSession(sessionhost.Session{Name: "c", Options: map[string]string{"harness": "codex"}}, now))
	if got, _ := codex["hand_refusal"].(string); got != "ship c runs on codex: only a Claude ship can be handed to Commodore" {
		t.Errorf("a Codex ship's hand_refusal = %q, want the route's 409 words", got)
	}
	claude := shipJSON(t, FromSession(sessionhost.Session{Name: "c", Options: map[string]string{"harness": "claude"}}, now))
	if v, ok := claude["hand_refusal"]; ok {
		t.Errorf("a Claude ship carries hand_refusal = %v", v)
	}
}

func TestAShipShowsHandingWhileItsPaneCarriesTheMark(t *testing.T) {
	t.Parallel()
	r := newHandRig(t)
	if v, ok := r.saltpaw(t)["handing"]; ok {
		t.Fatalf("a ship nobody handed carries handing = %v", v)
	}
	r.queue(t, `{"kind":"issue"}`)
	if got := r.saltpaw(t)["handing"]; got != true {
		t.Errorf("handing = %v while the hand is under way, want true", got)
	}
}

func TestAHandThatFailsOnALiveShipSaysWhyUntilTheNextHandStarts(t *testing.T) {
	t.Parallel()
	r := newHandRig(t)
	r.queue(t, `{"kind":"issue"}`)
	r.endTurn(t)
	waitUntil(t, "hand_failed on the live ship", func() bool {
		v, _ := r.saltpaw(t)["hand_failed"].(string)
		return strings.Contains(v, "no ref file")
	})
	if v, ok := r.saltpaw(t)["handing"]; ok {
		t.Errorf("handing = %v after the hand failed", v)
	}
	if rec := r.hand("saltpaw", `{"kind":"issue"}`); rec.Code != 202 {
		t.Fatalf("second hand = %d %s", rec.Code, rec.Body)
	}
	if v, ok := r.saltpaw(t)["hand_failed"]; ok {
		t.Errorf("hand_failed = %v once the next hand started, want none", v)
	}
}
