package crewapi_test

import (
	"bytes"
	"io"
	"log"
	"reflect"
	"strings"
	"sync"
	"testing"
)

const fakeToken = "fake-token-work-2"

func noAccessText(full string) string {
	return "You need access to " + full + ". Ask its owner to add you."
}

// wantNoAccess checks the whole answer: 403 with the no-access text and its code, and nothing else.
func wantNoAccess(t *testing.T, what string, status int, out map[string]any, full string) {
	t.Helper()
	want := map[string]any{"error": noAccessText(full), "code": "no_access"}
	if status != 403 || !reflect.DeepEqual(out, want) {
		t.Errorf("%s: %d %v, want 403 %v", what, status, out, want)
	}
}

// wantNoCode checks an answer that is not no_access: today's answer, with no code key.
func wantNoCode(t *testing.T, what string, out map[string]any) {
	t.Helper()
	if _, has := out["code"]; has {
		t.Errorf("%s: answer carries code %v, want today's answer with none", what, out["code"])
	}
}

// privateRepo is GET /repos/{owner}/{repo} for a private repo the account can push to.
func privateRepo(full string, push bool) map[string]any {
	return map[string]any{"full_name": full, "visibility": "private", "private": true, "archived": false,
		"permissions": map[string]any{"push": push}, "default_branch": "main"}
}

// privateShareRepo cans the login and the share repo as private and pushable.
func (g *ghFake) privateShareRepo() {
	g.ok("GET", "user", map[string]any{"login": "octocat"})
	g.ok("GET", "repos/"+shareRepo, privateRepo(shareRepo, true))
}

// noAccessTo cans a repo, and its crew/ listing, as answering 404.
func (g *ghFake) noAccessTo(full string) {
	g.answer("GET", "repos/"+full, 404, nil, ghNotFound)
	g.answer("GET", "repos/"+full+"/contents/crew", 404, nil, ghNotFound)
}

// readyPrivate is ready() against a private share repo.
func (p *pubFixture) readyPrivate(name, body string) {
	p.member(name, body)
	p.gh.privateShareRepo()
	p.gh.answer("GET", listingPath, 404, nil, ghNotFound)
}

// syncBuf is a log sink the server's goroutines can write while the test reads.
type syncBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// captureLog sends the global log to a buffer until the test ends; a test that uses it must not run in parallel.
func captureLog(t *testing.T) *syncBuf {
	t.Helper()
	b := &syncBuf{}
	old := log.Writer()
	log.SetOutput(io.MultiWriter(b, io.Discard))
	t.Cleanup(func() { log.SetOutput(old) })
	return b
}

// wantNoToken fails when any text holds the fake token.
func wantNoToken(t *testing.T, what string, texts ...string) {
	t.Helper()
	for _, text := range texts {
		if strings.Contains(text, fakeToken) {
			t.Errorf("%s holds the GitHub token: %.200q", what, text)
		}
	}
}
