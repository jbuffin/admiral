// Package lockroute serves the route `admiral lock` holds a resource unit through. It is loopback only.
package lockroute

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jbuffin/admiral/internal/auth"
	"github.com/jbuffin/admiral/internal/resources"
	"github.com/jbuffin/admiral/internal/server"
)

// A ship that was only pending frees its unit when it expires, with no Changed wake.
const retryEvery = 15 * time.Second

const maxLabel = 64

// The request is the lock: the unit is held while the response stays open.
type Route struct {
	Set *resources.Set
	// Stopping closes on shutdown, so a held lock doesn't hold it up.
	Stopping <-chan struct{}
	// OnTake runs after a unit was taken; a release announces itself.
	OnTake func()
}

func (rt *Route) Routes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/resources/{name}/lock", rt.serve)
}

func (rt *Route) Connected(*server.Client) {}

var _ server.Module = (*Route)(nil)

func fail(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func (rt *Route) serve(w http.ResponseWriter, r *http.Request) {
	if !auth.IsLoopbackPeer(r) {
		fail(w, http.StatusForbidden, "locks are taken from this Mac only")
		return
	}
	// The context ends on a hang-up only once the body has been read.
	_, _ = io.Copy(io.Discard, r.Body)
	name, label, wait := r.PathValue("name"), CleanLabel(r.URL.Query().Get("label")), r.URL.Query().Get("wait") == "1"

	var release func()
	for {
		changed := rt.Set.Changed() // before the try, so a release in between wakes us
		rel, err := rt.Set.Lock(name, label)
		if err == nil {
			release = rel
			break
		}
		if errors.Is(err, resources.ErrUnknown) {
			fail(w, http.StatusNotFound, "no such resource")
			return
		}
		if !wait {
			fail(w, http.StatusConflict, "no unit of the resource is free")
			return
		}
		retry := time.NewTimer(retryEvery)
		select {
		case <-changed:
		case <-retry.C:
		case <-r.Context().Done():
			retry.Stop()
			return
		case <-rt.Stopping:
			retry.Stop()
			return
		}
		retry.Stop()
	}
	defer release()
	if rt.OnTake != nil {
		rt.OnTake()
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"held":true}` + "\n"))
	_ = http.NewResponseController(w).Flush()
	select {
	case <-r.Context().Done():
	case <-rt.Stopping:
	}
}

func CleanLabel(s string) string {
	var b strings.Builder
	n := 0
	for _, r := range strings.ToValidUTF8(s, "") {
		if r == utf8.RuneError || !unicode.IsPrint(r) {
			continue
		}
		if n++; n > maxLabel {
			break
		}
		b.WriteRune(r)
	}
	return b.String()
}
