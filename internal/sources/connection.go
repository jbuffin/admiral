package sources

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/jbuffin/admiral/internal/github"
)

// The states of a GitHub source's connection.
const (
	ConnChecking    = "checking"
	ConnConnected   = "connected"
	ConnSignedOut   = "signed_out"
	ConnGHMissing   = "gh_missing"
	ConnUnreachable = "unreachable"
)

// Connection is what the last check found about a GitHub source's sign-in.
type Connection struct {
	State     string   `json:"state"`
	Host      string   `json:"host"`
	Account   string   `json:"account,omitempty"`
	Login     string   `json:"login,omitempty"`
	CheckedAt string   `json:"checked_at,omitempty"`
	Fix       []string `json:"fix"`
	Message   string   `json:"message,omitempty"`
}

// connState is the part of a Connection a check records; the rest follows the source's current settings.
type connState struct {
	state     string
	login     string
	message   string
	checkedAt time.Time
}

// connectionOf is the source's connection as its current settings read it; nil for a source that isn't GitHub.
func connectionOf(st sourceState) *Connection {
	if st.def.Plugin != builtinGitHub {
		return nil
	}
	s := githubSettings(st.def.Settings)
	c := &Connection{State: ConnChecking, Host: s.Host, Account: s.Account, Fix: []string{}}
	if cs := st.conn; cs != nil {
		c.State, c.Login, c.Message = cs.state, cs.login, cs.message
		c.CheckedAt = cs.checkedAt.UTC().Format(time.RFC3339Nano)
	}
	switch c.State {
	case ConnSignedOut:
		c.Fix = []string{"gh auth login -h " + s.Host}
	case ConnGHMissing:
		c.Fix = []string{"brew install gh", "gh auth login -h " + s.Host}
	}
	return c
}

// record keeps a check's outcome. Who gh is signed in as is kept across a check that can't tell, and dropped on a sign-out.
func (g *githubConn) record(state, login, message string) {
	cs := connState{state: state, login: login, message: message, checkedAt: time.Now()}
	g.mu.Lock()
	switch {
	case state == ConnSignedOut || state == ConnGHMissing:
		cs.login = ""
	case login == "":
		cs.login = g.conn.login
	}
	g.conn = cs
	g.mu.Unlock()
	g.setConn(cs)
}

// signedOut records a sign-in error and tells the runner, once per message.
func (g *githubConn) signedOut(si *github.SignInError) {
	state := ConnSignedOut
	if si.Kind == github.KindGHMissing {
		state = ConnGHMissing
	}
	g.record(state, "", si.Message)
	g.mu.Lock()
	fresh := g.authMsg != si.Message
	g.authMsg = si.Message
	g.mu.Unlock()
	if fresh {
		g.notify("auth.required", map[string]string{"message": si.Message})
	}
}

// signedIn clears the sign-in card a sign-out raised.
func (g *githubConn) signedIn() {
	g.mu.Lock()
	wasOut := g.authMsg != ""
	g.authMsg = ""
	g.mu.Unlock()
	if wasOut {
		g.notify("auth.ok", nil)
	}
}

// check runs the token check and a viewer query now and records what they found.
func (g *githubConn) check(ctx context.Context) connState {
	g.mu.Lock()
	client := g.client
	g.mu.Unlock()
	token, err := client.Token(ctx)
	var si *github.SignInError
	switch {
	case errors.As(err, &si):
		g.signedOut(si)
	case err != nil:
		g.record(ConnUnreachable, "", err.Error())
	default:
		login, err := client.Viewer(ctx, token)
		if github.IsUnreachable(err) {
			g.record(ConnUnreachable, "", err.Error())
			break
		}
		g.signedIn()
		g.record(ConnConnected, login, "")
		g.refreshNow()
	}
	return g.currentConn()
}

func (g *githubConn) currentConn() connState {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.conn
}

// maxCountQuery bounds a search sent for a count.
const maxCountQuery = 1024

// githubSource finds a live GitHub source for a route, answering the refusal itself when there is none.
func (s *Service) githubSource(w http.ResponseWriter, key string) (*runner, *githubConn, bool) {
	def, ok := s.Config.Current().Source(key)
	switch {
	case !ok || s.Sup.runner(key) == nil:
		writeError(w, http.StatusNotFound, "source "+key+" not found")
		return nil, nil, false
	case def.Plugin != builtinGitHub:
		writeError(w, http.StatusBadRequest, "source "+key+" is not a GitHub source")
		return nil, nil, false
	}
	rn, p, fail := s.mutableFor(key, "refresh")
	if fail != nil {
		writeError(w, fail.Status, fail.Msg)
		return nil, nil, false
	}
	g, ok := p.(*githubConn)
	if !ok {
		writeError(w, http.StatusConflict, "The source is not running yet")
		return nil, nil, false
	}
	return rn, g, true
}

func (s *Service) serveConnectionCheck(w http.ResponseWriter, r *http.Request) {
	rn, g, ok := s.githubSource(w, r.PathValue("key"))
	if !ok {
		return
	}
	g.check(r.Context())
	writeJSON(w, http.StatusOK, connectionOf(rn.snapshot()))
}

func (s *Service) serveSearchCount(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Query string `json:"query"`
		View  string `json:"view"`
	}
	if !decode(w, r, &req) {
		return
	}
	rn, g, ok := s.githubSource(w, r.PathValue("key"))
	if !ok {
		return
	}
	query := strings.TrimSpace(req.Query)
	if req.View != "" {
		i := slices.IndexFunc(githubSettings(rn.snapshot().def.Settings).Views, func(v github.View) bool { return v.ID == req.View })
		if i < 0 {
			writeError(w, http.StatusBadRequest, "no such view: "+req.View)
			return
		}
		if query = githubSettings(rn.snapshot().def.Settings).Views[i].Query; query == "" {
			writeError(w, http.StatusBadRequest, "The view "+req.View+" is not a search")
			return
		}
	}
	switch {
	case query == "":
		writeError(w, http.StatusBadRequest, "Name a query or a view to count")
		return
	case len(query) > maxCountQuery:
		writeError(w, http.StatusBadRequest, "A query can hold at most 1024 bytes")
		return
	}
	g.mu.Lock()
	client := g.client
	g.mu.Unlock()
	token, err := client.Token(r.Context())
	var si *github.SignInError
	if errors.As(err, &si) {
		g.signedOut(si)
		writeJSON(w, http.StatusConflict, map[string]any{"error": si.Message, "connection": connectionOf(rn.snapshot())})
		return
	}
	var n int
	if err == nil {
		n, err = client.Count(r.Context(), token, query)
	}
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]int{"count": n})
	case github.IsUnreachable(err) || r.Context().Err() != nil:
		writeError(w, http.StatusBadGateway, err.Error())
	default:
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	}
}
