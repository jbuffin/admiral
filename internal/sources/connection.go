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
		c.settled(*cs)
	}
	return c
}

// settled puts a recorded check into the connection; a check that has recorded nothing reads as checking, with no time.
func (c *Connection) settled(cs connState) {
	c.State, c.Login, c.Message, c.CheckedAt = cs.state, cs.login, cs.message, ""
	if c.State == "" {
		c.State = ConnChecking
	}
	if !cs.checkedAt.IsZero() {
		c.CheckedAt = cs.checkedAt.UTC().Format(time.RFC3339Nano)
	}
	c.Fix = fixFor(c.State, c.Host)
}

// fixFor is the commands that turn a sign-in state into a connected one.
func fixFor(state, host string) []string {
	switch state {
	case ConnSignedOut:
		return []string{"gh auth login -h " + host}
	case ConnGHMissing:
		return []string{"brew install gh", "gh auth login -h " + host}
	}
	return []string{}
}

// record keeps a check's outcome. Who gh is signed in as is kept across a check that can't tell, and dropped on a sign-out.
func (g *githubConn) record(state, login, message string) connState {
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
	return cs
}

// signedOut records a sign-in error and tells the runner, once per message.
func (g *githubConn) signedOut(si *github.SignInError) connState {
	state := ConnSignedOut
	if si.Kind == github.KindGHMissing {
		state = ConnGHMissing
	}
	cs := g.record(state, "", si.Message)
	g.mu.Lock()
	fresh := g.authMsg != si.Message
	g.authMsg = si.Message
	g.mu.Unlock()
	if fresh {
		g.notify("auth.required", map[string]string{"message": si.Message})
	}
	return cs
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

// settle turns a token result and a query result into the connection state and records it; it reports false, recording nothing, when ctx ended.
// A query that answered is the only thing that says connected. A refusal that isn't about the token leaves the state as it was,
// and note puts its text in the message for a Check again to show.
func (g *githubConn) settle(ctx context.Context, tokenErr, queryErr error, login string, note bool) (connState, bool) {
	if ctx.Err() != nil {
		return g.currentConn(), false
	}
	var si *github.SignInError
	switch {
	case errors.As(tokenErr, &si):
		return g.signedOut(si), true
	case github.IsSlow(tokenErr):
		if note {
			return g.noteMessage(tokenErr.Error()), true
		}
		return g.currentConn(), true
	case tokenErr != nil:
		return g.record(ConnUnreachable, "", tokenErr.Error()), true
	case queryErr == nil:
		g.signedIn()
		return g.record(ConnConnected, login, ""), true
	case github.IsUnreachable(queryErr):
		return g.record(ConnUnreachable, "", queryErr.Error()), true
	case github.IsBadCredentials(queryErr):
		return g.signedOut(&github.SignInError{Message: queryErr.Error(), Kind: github.KindSignedOut}), true
	case note:
		return g.noteMessage(queryErr.Error()), true
	}
	return g.currentConn(), true
}

// noteMessage keeps the last state and says why a check found nothing better.
func (g *githubConn) noteMessage(msg string) connState {
	g.mu.Lock()
	if g.conn.state == "" {
		g.mu.Unlock()
		return connState{state: ConnChecking}
	}
	g.conn.message = msg
	cs := g.conn
	g.mu.Unlock()
	g.setConn(cs)
	return cs
}

// check runs the token check and a viewer query now and records what they found.
func (g *githubConn) check(ctx context.Context) (connState, bool) {
	g.mu.Lock()
	client := g.client
	g.mu.Unlock()
	token, err := client.Token(ctx)
	var login string
	var qerr error
	if err == nil {
		login, qerr = client.Viewer(ctx, token)
	}
	cs, ok := g.settle(ctx, err, qerr, login, true)
	if ok && cs.state == ConnConnected {
		g.refreshNow()
	}
	return cs, ok
}

// count asks GitHub how many results a query has. A good count says the token works; a sign-in problem is the error.
func (g *githubConn) count(ctx context.Context, query string) (int, connState, error) {
	g.mu.Lock()
	client := g.client
	g.mu.Unlock()
	token, err := client.Token(ctx)
	var n int
	var qerr error
	if err == nil {
		n, qerr = client.Count(ctx, token, query)
	}
	cs, _ := g.settle(ctx, err, qerr, "", false)
	if err != nil {
		return 0, cs, err
	}
	return n, cs, qerr
}

func (g *githubConn) currentConn() connState {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.conn
}

// maxCountQuery bounds a search sent for a count.
const maxCountQuery = 1024

// startGrace bounds how long a request waits for a source that a settings save restarted.
const startGrace = 5 * time.Second

// awaitStart holds a request that lands while the source restarts, because the page asks for counts right after its own save and a 409 there only logs a console error.
func awaitStart(ctx context.Context, rn *runner) {
	timeout := time.NewTimer(startGrace)
	defer timeout.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		rn.mu.Lock()
		starting := rn.state == StateStarting
		rn.mu.Unlock()
		if !starting {
			return
		}
		select {
		case <-tick.C:
		case <-timeout.C:
			return
		case <-ctx.Done():
			return
		}
	}
}

// githubSource finds a live GitHub source for a route, answering the refusal itself when there is none.
func (s *Service) githubSource(ctx context.Context, w http.ResponseWriter, key string) (*runner, *githubConn, bool) {
	def, ok := s.Config.Current().Source(key)
	switch {
	case !ok || s.Sup.runner(key) == nil:
		writeError(w, http.StatusNotFound, "source "+key+" not found")
		return nil, nil, false
	case def.Plugin != builtinGitHub:
		writeError(w, http.StatusBadRequest, "source "+key+" is not a GitHub source")
		return nil, nil, false
	}
	awaitStart(ctx, s.Sup.runner(key))
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
	rn, g, ok := s.githubSource(r.Context(), w, r.PathValue("key"))
	if !ok {
		return
	}
	cs, ok := g.check(r.Context())
	if !ok {
		return
	}
	c := connectionOf(rn.snapshot())
	c.settled(cs)
	writeJSON(w, http.StatusOK, c)
}

func (s *Service) serveSearchCount(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Query string `json:"query"`
		View  string `json:"view"`
	}
	if !decode(w, r, &req) {
		return
	}
	rn, g, ok := s.githubSource(r.Context(), w, r.PathValue("key"))
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
	n, cs, err := g.count(r.Context(), query)
	var si *github.SignInError
	switch {
	case r.Context().Err() != nil:
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]int{"count": n})
	case errors.As(err, &si), github.IsBadCredentials(err):
		c := connectionOf(rn.snapshot())
		c.settled(cs)
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "connection": c})
	case github.IsUnreachable(err):
		writeError(w, http.StatusBadGateway, err.Error())
	default:
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	}
}
