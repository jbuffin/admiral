package ships

// The ship answer route checks the stop an answer names, and marks every answer AdmiralBot gives via admiralbot.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/actor"
	"github.com/jbuffin/admiral/internal/conversation"
	"github.com/jbuffin/admiral/internal/sessionhost"
)

func (p *answerPane) postAs(fleet, path, body string) (int, string) {
	req := httptest.NewRequest(http.MethodPost, "/api/ships/alpha"+path, strings.NewReader(body))
	if fleet != "" {
		req = req.WithContext(actor.WithFleet(context.Background(), fleet))
	}
	rec := httptest.NewRecorder()
	p.mux.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

func permissionBody(stopID string) string {
	body := `{"card":"permission","reason":"notification:permission_prompt","choice":"allow"`
	if stopID != "" {
		body += `,"stop_id":"` + stopID + `"`
	}
	return body + `}`
}

func TestTheAnswerRouteTakesAnAnswerNamingThePanesLiveStopAndSendsItsKeys(t *testing.T) {
	t.Parallel()
	p := newAnswerPane(t, "copilot", nil)
	p.needsYou("notification:permission_prompt")
	id := p.stopID()
	if code, msg := p.post(permissionBody(id)); code != http.StatusOK {
		t.Fatalf("answer naming the live stop = %d %s, want 200", code, msg)
	}
	if got := p.sent("1"); got != "1" {
		t.Errorf("Allow sent %q, want %q", got, "1")
	}
}

func TestTheAnswerRouteRefusesAStaleStopIdWith409AndSendsNoKeys(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ name, reason, body string }{
		{"a permission prompt", "notification:permission_prompt", `{"card":"permission","reason":"notification:permission_prompt","choice":"allow","stop_id":"%s"}`},
		{"a question menu", "permission:AskUserQuestion", `{"card":"question","reason":"permission:AskUserQuestion","item":"q-1","option":1,"stop_id":"%s"}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := newAnswerPane(t, "copilot", []conversation.Item{askItem("q-1", true, "A", "B")})
			p.needsYou(c.reason)
			old := p.stopID()
			p.needsYou(c.reason) // the same prompt again is a new stop
			if cur := p.stopID(); cur == old {
				t.Fatalf("a second identical prompt kept stop id %q", old)
			}
			for what, stop := range map[string]string{"an earlier stop": old, "an unknown stop": "never-minted"} {
				code, msg := p.post(strings.Replace(c.body, "%s", stop, 1))
				if code != http.StatusConflict || !strings.Contains(msg, "already answered") {
					t.Errorf("answer naming %s = %d %s, want 409 already answered", what, code, msg)
				}
			}
			if got := p.sent(""); got != "" {
				t.Errorf("refused answers sent %q, want no keys", got)
			}
			if got := p.option(sessionhost.OptStopAnswered); got != "" {
				t.Errorf("@admiral-stop-answered = %q after refused answers, want none", got)
			}
		})
	}
}

func TestTheAnswerRouteAnswersAStopOnceAndRefusesTheSecondWith409(t *testing.T) {
	t.Parallel()
	for name, reason := range map[string]string{"a permission prompt": "notification:permission_prompt", "a question menu": "permission:AskUserQuestion"} {
		t.Run(name, func(t *testing.T) {
			p := newAnswerPane(t, "copilot", []conversation.Item{askItem("q-1", true, "A", "B")})
			p.needsYou(reason)
			id := p.stopID()
			body := permissionBody(id)
			if reason != "notification:permission_prompt" {
				body = `{"card":"question","reason":"` + reason + `","item":"q-1","option":1,"stop_id":"` + id + `"}`
			}
			if code, msg := p.post(body); code != http.StatusOK {
				t.Fatalf("first answer = %d %s, want 200", code, msg)
			}
			sent := p.sentAny()
			code, msg := p.post(body)
			if code != http.StatusConflict || !strings.Contains(msg, "already answered") {
				t.Errorf("second answer to the same stop = %d %s, want 409 already answered", code, msg)
			}
			p.holdSent(sent)
		})
	}
}

func TestTheAnswerRouteWithNoStopIdBehavesAsItAlwaysDid(t *testing.T) {
	t.Parallel()
	p := newAnswerPane(t, "copilot", nil)
	p.needsYou("notification:permission_prompt")
	if code, msg := p.post(permissionBody("")); code != http.StatusOK {
		t.Fatalf("answer with no stop_id = %d %s, want 200", code, msg)
	}
	if got := p.sent("1"); got != "1" {
		t.Errorf("Allow sent %q, want %q", got, "1")
	}
}

func TestAQuestionMenuAnsweredUnderTheAdmiralBotTokenIsMarkedViaAdmiralBot(t *testing.T) {
	t.Parallel()
	p := newAnswerPane(t, "copilot", []conversation.Item{askItem("q-1", true, "A", "B")})
	p.needsYou("permission:AskUserQuestion")
	id := p.stopID()
	body := `{"card":"question","reason":"permission:AskUserQuestion","item":"q-1","option":2,"stop_id":"` + id + `"}`
	if code, msg := p.postAs("work", "/answer", body); code != http.StatusOK {
		t.Fatalf("answer under the actor token = %d %s, want 200", code, msg)
	}
	if got, want := p.option(sessionhost.OptStopAnswered), id+" admiralbot"; got != want {
		t.Errorf("@admiral-stop-answered = %q, want %q", got, want)
	}
}

func TestAQuestionMenuAnsweredByTheEngineerIsMarkedWithTheStopIdAlone(t *testing.T) {
	t.Parallel()
	p := newAnswerPane(t, "copilot", []conversation.Item{askItem("q-1", true, "A", "B")})
	p.needsYou("permission:AskUserQuestion")
	id := p.stopID()
	if code, msg := p.post(`{"card":"question","reason":"permission:AskUserQuestion","item":"q-1","option":2}`); code != http.StatusOK {
		t.Fatalf("answer = %d %s, want 200", code, msg)
	}
	if got := p.option(sessionhost.OptStopAnswered); got != id {
		t.Errorf("@admiral-stop-answered = %q, want %q", got, id)
	}
}

func TestARefusedQuestionAnswerMarksNoStopAnswered(t *testing.T) {
	t.Parallel()
	p := newAnswerPane(t, "copilot", []conversation.Item{askItem("q-1", true, "A", "B")})
	p.needsYou("permission:AskUserQuestion")
	if code, _ := p.postAs("work", "/answer", `{"card":"question","reason":"permission:AskUserQuestion","item":"elsewhere","option":1}`); code != http.StatusConflict {
		t.Fatalf("answer to another item = %d, want 409", code)
	}
	if got := p.option(sessionhost.OptStopAnswered); got != "" {
		t.Errorf("@admiral-stop-answered = %q after a refused answer, want none", got)
	}
}

func TestNextUnderTheAdmiralBotTokenMarksTheStopAnsweredViaAdmiralBot(t *testing.T) {
	t.Parallel()
	p := newAnswerPane(t, "copilot", nil)
	p.needsYou("stop")
	id := p.stopID()
	if code, msg := p.postAs("work", "/next", `{"text":"now the docs"}`); code != http.StatusOK {
		t.Fatalf("next under the actor token = %d %s, want 200", code, msg)
	}
	if got, want := p.option(sessionhost.OptStopAnswered), id+" admiralbot"; got != want {
		t.Errorf("@admiral-stop-answered = %q, want %q", got, want)
	}
}
