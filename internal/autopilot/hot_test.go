package autopilot

import (
	"net/http"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/sources"
)

func TestEngineSaysWhenAnOrderTurnsHotAndCools(t *testing.T) {
	r := newRig(t, "o1")
	r.failWith = &launcher.Error{Status: http.StatusInternalServerError, Msg: "boom"}
	hot := 0
	r.engine.OnHot = func() { hot++ }
	for _, wait := range []time.Duration{0, 10 * time.Minute, time.Hour} {
		r.now = r.now.Add(wait)
		r.pass()
	}
	if hot != 1 || !r.engine.Hot("s", "o1") {
		t.Fatalf("after the third failure OnHot ran %d times and Hot = %v, want 1 and true", hot, r.engine.Hot("s", "o1"))
	}
	r.src[0].Panels = []sources.Panel{panel("p")}
	r.pass()
	if hot != 2 {
		t.Errorf("OnHot ran %d times after the order left its listing, want 2", hot)
	}
}
