package actor

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/jbuffin/admiral/internal/auth"
	"github.com/jbuffin/admiral/internal/sessionhost"
)

// Guard is the middleware that recognises an actor token and fences the request it carries.
// Each lookup answers the fleet of what the request names, and false when that fleet can't be found.
type Guard struct {
	Tokens        func(token string) (fleet string, ok bool)
	ShipFleet     func(session string) (fleet string, known bool)
	ItemFleet     func(session, id string) (fleet string, known bool)
	SourceFleet   func(key string) (fleet string, known bool)
	CampaignFleet func(id string) (fleet string, known bool)
}

type kind int

const (
	plain kind = iota
	ship
	item
	source
	campaign
	launch
	fleetKey // the first "*" is a fleet key, which must be the actor's own
)

// allowed is every route AdmiralBot may call, as the method and the path's segments after /api; "*" matches one segment.
var allowed = []struct {
	method string
	segs   []string
	kind   kind
}{
	{"GET", []string{"ships"}, plain},
	{"GET", []string{"ships", "*", "conversation"}, ship},
	{"GET", []string{"inbox"}, plain},
	{"GET", []string{"sources"}, plain},
	{"GET", []string{"campaigns"}, plain},
	{"GET", []string{"campaigns", "*"}, campaign},
	{"POST", []string{"ships"}, launch},
	{"POST", []string{"sources", "*", "orders"}, source},
	{"POST", []string{"inbox", "*", "*", "answer"}, item},
	{"POST", []string{"ships", "*", "stop"}, ship},
	{"POST", []string{"ships", "*", "dismiss"}, ship},
	{"POST", []string{"ships", "*", "next"}, ship},
	{"POST", []string{"ships", "*", "wake"}, ship},
	{"GET", []string{"outcomes"}, plain},
	{"POST", []string{"chat", "questions"}, plain},
	{"GET", []string{"chat", "questions", "*", "answer"}, plain},
	{"GET", []string{"fleets", "*", "routines"}, fleetKey},
	{"GET", []string{"fleets", "*", "standing-orders"}, fleetKey},
	{"GET", []string{"fleets", "*", "hold-fire"}, fleetKey},
	{"POST", []string{"fleets", "*", "routines", "*", "off"}, fleetKey},
	{"POST", []string{"fleets", "*", "hold-fire", "on"}, fleetKey},
	// every fleet's Hold fire can only be turned on from here: it makes Admiral quieter, never louder
	{"GET", []string{"hold-fire"}, plain},
	{"POST", []string{"hold-fire", "on"}, plain},
	{"POST", []string{"chat", "notes"}, plain},
	{"POST", []string{"chat", "commitments"}, plain},
}

func (g *Guard) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get(Header)
		if token == "" {
			next.ServeHTTP(w, r)
			return
		}
		if !auth.IsLocal(r) {
			refuse(w, http.StatusForbidden, "an actor token is honoured only on this Mac")
			return
		}
		fleet, ok := g.Tokens(token)
		if !ok {
			refuse(w, http.StatusUnauthorized, "the actor token is not valid")
			return
		}
		args, ok := match(r)
		if !ok {
			refuse(w, http.StatusForbidden, "AdmiralBot can't do that: "+r.Method+" "+r.URL.Path+" is not one of its routes")
			return
		}
		if status, msg := g.fence(r, fleet, args); msg != "" {
			refuse(w, status, msg)
			return
		}
		r.Header.Del(Header)
		next.ServeHTTP(w, r.WithContext(WithFleet(r.Context(), fleet)))
	})
}

type matched struct {
	kind kind
	ids  []string // the "*" segments, in order
}

// match finds the route an actor request calls, after the same /api/v1 normalisation the mux applies, or false.
// A path the mux would clean or decode differently is refused rather than matched.
func match(r *http.Request) (matched, bool) {
	p := r.URL.Path
	if r.URL.RawPath != "" || strings.Contains(p, "//") || strings.Contains(p, "/.") || strings.HasSuffix(p, "/") {
		return matched{}, false
	}
	rest, ok := strings.CutPrefix(p, "/api")
	if !ok || rest == "" || rest[0] != '/' {
		return matched{}, false
	}
	if v, ok := strings.CutPrefix(rest, "/v1"); ok && (v == "" || v[0] == '/') {
		rest = v
	}
	segs := strings.Split(strings.TrimPrefix(rest, "/"), "/")
	for _, route := range allowed {
		if route.method != r.Method || len(route.segs) != len(segs) {
			continue
		}
		var ids []string
		fit := true
		for i, want := range route.segs {
			if segs[i] == "" {
				fit = false
				break
			}
			if want == "*" {
				ids = append(ids, segs[i])
			} else if want != segs[i] {
				fit = false
				break
			}
		}
		if fit {
			return matched{kind: route.kind, ids: ids}, true
		}
	}
	return matched{}, false
}

// fence is why the request touches another fleet's thing, with the status to refuse it by, or "".
func (g *Guard) fence(r *http.Request, fleet string, m matched) (int, string) {
	switch m.kind {
	case ship:
		return forbid(check("ship", m.ids[0], fleet)(g.ShipFleet(m.ids[0])))
	case item:
		if sessionhost.IsChatSession(m.ids[0]) {
			return forbid("a question from the chat is the engineer's to answer, not AdmiralBot's")
		}
		return forbid(check("inbox item "+m.ids[1]+" of", m.ids[0], fleet)(g.ItemFleet(m.ids[0], m.ids[1])))
	case source:
		return forbid(check("source", m.ids[0], fleet)(g.SourceFleet(m.ids[0])))
	case fleetKey:
		if m.ids[0] != fleet {
			return forbid("fleet " + m.ids[0] + " is not fleet " + fleet + ", so AdmiralBot can't touch that fleet's routines or standing orders")
		}
	case campaign:
		return forbid(check("campaign", m.ids[0], fleet)(g.CampaignFleet(m.ids[0])))
	case launch:
		body, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, 4<<20))
		if err != nil {
			return http.StatusBadRequest, "the launch request could not be read"
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		var req struct {
			Fleet  string `json:"fleet"`
			Source string `json:"source"`
		}
		// read the first value as the handler does, and refuse what follows it, so both see one request
		dec := json.NewDecoder(bytes.NewReader(body))
		if dec.Decode(&req) != nil || dec.Decode(&struct{}{}) != io.EOF {
			return http.StatusBadRequest, "the launch body must be one JSON object"
		}
		if req.Fleet != fleet {
			return forbid("a launch from this chat must name fleet " + fleet + ", not " + quote(req.Fleet))
		}
		if req.Source != "" {
			return forbid(check("source", req.Source, fleet)(g.SourceFleet(req.Source)))
		}
	}
	return 0, ""
}

func forbid(msg string) (int, string) { return http.StatusForbidden, msg }

func quote(s string) string {
	if s == "" {
		return "no fleet"
	}
	return "fleet " + s
}

func check(what, name, mine string) func(string, bool) string {
	return func(theirs string, known bool) string {
		switch {
		case !known:
			return what + " " + name + " is in an unknown fleet, so AdmiralBot can't touch it"
		case theirs != mine:
			return what + " " + name + " is in fleet " + theirs + ", not " + mine
		}
		return ""
	}
}

func refuse(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	b, _ := json.Marshal(map[string]string{"error": msg})
	_, _ = w.Write(append(b, '\n'))
}
