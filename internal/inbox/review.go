package inbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/jbuffin/admiral/internal/outcomes"
)

// ghTimeout bounds one gh call. A call is never retried.
const ghTimeout = 30 * time.Second

// Caps on what is read from gh: its stderr reaches the UI, so it is short.
const (
	ghStderrCap = 4 << 10
	ghStdoutCap = 1 << 20
)

// ErrBusy is an action on an item that another action is still running on.
var ErrBusy = errors.New("another action on this item is still running")

// GhError is a failed gh call. Msg is what the engineer sees. A review that GitHub
// reports gone is not an error to the caller: see gone.
type GhError struct {
	Msg string
	// Missing is a gh that is not on PATH.
	Missing bool
	status  int    // the HTTP status gh printed, 0 if none
	detail  string // what gh printed, for gone
}

func (e *GhError) Error() string { return e.Msg }

// gone is GitHub saying the review is not there (404) or is no longer pending (a
// 422 that names pending).
func (e *GhError) gone() bool {
	return e.status == 404 || (e.status == 422 && strings.Contains(strings.ToLower(e.detail), "pending"))
}

var httpStatus = regexp.MustCompile(`\(HTTP (\d{3})\)`)

// target is the review a review item names.
type target struct {
	host, owner, repo, pr, reviewID string
}

// prURL is https://<host>/<owner>/<repo>/pull/<n> and nothing more: no user
// info, port, query, fragment, escape or further path.
var prURL = regexp.MustCompile(`^https://([A-Za-z0-9.-]+)/([A-Za-z0-9-]+)/([A-Za-z0-9._-]+)/pull/([1-9][0-9]{0,8})$`)

var reviewIDPattern = regexp.MustCompile(`^[1-9][0-9]{0,17}$`)

// targetOf reads the review of a review item. Only these parts of the item's
// text ever reach gh, and only as a path.
func targetOf(it Item) (target, string, error) {
	var r struct {
		PRURL    string          `json:"pr_url"`
		ReviewID json.RawMessage `json:"review_id"`
		Summary  string          `json:"summary"`
	}
	_ = json.Unmarshal(it.Field(KindReview), &r)
	m := prURL.FindStringSubmatch(r.PRURL)
	if m == nil || strings.HasPrefix(m[1], "-") || strings.HasPrefix(m[1], ".") || m[3] == "." || m[3] == ".." {
		return target{}, "", invalid("item %s: pr_url must be https://<host>/<owner>/<repo>/pull/<number>", it.ID)
	}
	id := string(bytes.TrimSpace(r.ReviewID))
	if !reviewIDPattern.MatchString(id) {
		return target{}, "", invalid("item %s: review_id must be a whole number", it.ID)
	}
	return target{strings.ToLower(m[1]), m[2], m[3], m[4], id}, r.Summary, nil
}

func (t target) endpoint() string {
	return "repos/" + t.owner + "/" + t.repo + "/pulls/" + t.pr + "/reviews/" + t.reviewID
}

// ghRun runs gh for one review action: the binary found on Admiral's PATH, the
// environment of the ship's dock, and the host of the PR.
type ghRun struct {
	bin  string
	env  []string
	host string
	note string // appended to a failure: where gh ran
}

// ghFor finds gh and the environment for session's ship. A ship Admiral has no
// dock for runs gh in Admiral's own environment, and an error says so.
func (s *Service) ghFor(session, host string) (*ghRun, error) {
	bin, err := exec.LookPath("gh")
	if err != nil {
		return nil, &GhError{Msg: "gh was not found on Admiral's PATH, so the review cannot be changed on GitHub", Missing: true}
	}
	g := &ghRun{bin: bin, host: host}
	if s.Dock != nil {
		if env, ok := s.Dock(session); ok {
			g.env = env
		}
	}
	if g.env == nil {
		g.env = os.Environ()
		g.note = "ship " + session + " is unknown, so gh ran in Admiral's own environment"
	}
	g.env = append(g.env, "GH_PROMPT_DISABLED=1", "GH_NO_UPDATE_NOTIFIER=1")
	return g, nil
}

// call runs `gh api` for method and endpoint, with body as JSON on stdin when it
// is not nil, and returns gh's stdout. The item's text is only ever in the body.
func (g *ghRun) call(method, endpoint string, body any) ([]byte, *GhError) {
	args := []string{"api", "--method", method}
	if g.host != "github.com" {
		args = append(args, "--hostname", g.host)
	}
	var stdin *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		stdin = bytes.NewReader(b)
		args = append(args, "--input", "-")
	}
	args = append(args, endpoint)
	// Not the request's context: a client that hangs up must not abandon a
	// review gh may already have changed.
	ctx, cancel := context.WithTimeout(context.Background(), ghTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, g.bin, args...)
	cmd.Env = g.env
	cmd.WaitDelay = time.Second
	if stdin != nil {
		cmd.Stdin = stdin
	}
	var stdout, stderr capped
	stdout.max, stderr.max = ghStdoutCap, ghStderrCap
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err == nil {
		return stdout.buf.Bytes(), nil
	}
	e := &GhError{detail: stderr.buf.String() + "\n" + stdout.buf.String()}
	if m := httpStatus.FindStringSubmatch(stderr.buf.String()); m != nil {
		fmt.Sscan(m[1], &e.status)
	}
	switch msg := strings.TrimSpace(stderr.buf.String()); {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		e.Msg = fmt.Sprintf("gh did not answer within %s", ghTimeout)
	case msg != "":
		e.Msg = "gh failed: " + msg + githubMessage(stdout.buf.Bytes(), msg)
	default:
		e.Msg = "gh failed: " + err.Error()
	}
	if g.note != "" {
		e.Msg += " (" + g.note + ")"
	}
	return nil, e
}

// githubMessage is the error text of GitHub's JSON body that gh's stderr does not
// already show, set off by a newline.
func githubMessage(body []byte, shown string) string {
	var j struct {
		Message string            `json:"message"`
		Errors  []json.RawMessage `json:"errors"`
	}
	if json.Unmarshal(body, &j) != nil {
		return ""
	}
	texts := []string{j.Message}
	for _, raw := range j.Errors {
		var s string
		var o struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(raw, &s) == nil {
			texts = append(texts, s)
		} else if json.Unmarshal(raw, &o) == nil {
			texts = append(texts, o.Message)
		}
	}
	var out string
	for _, t := range texts {
		if t != "" && !strings.Contains(shown, t) {
			out += "\n" + t
		}
	}
	return out
}

// capped keeps the first max bytes written and drops the rest.
type capped struct {
	buf bytes.Buffer
	max int
}

func (c *capped) Write(p []byte) (int, error) {
	if room := c.max - c.buf.Len(); room > 0 {
		c.buf.Write(p[:min(room, len(p))])
	}
	return len(p), nil
}

// SubmitRequest is the body of the submit route: the review event, and whether
// the engineer opened Edit on GitHub first.
type SubmitRequest struct {
	Event  string `json:"event"`
	Edited bool   `json:"edited"`
}

var reviewEvents = map[string]bool{"COMMENT": true, "APPROVE": true, "REQUEST_CHANGES": true}

// SubmitReview submits the pending review of a review item as the engineer, with
// the body it has on GitHub now (the engineer may have edited it there), or the
// item's summary when that is empty. It returns what happened: submitted, or gone
// when GitHub no longer has a pending review.
func (s *Service) SubmitReview(session, id string, req SubmitRequest) (string, error) {
	if !reviewEvents[req.Event] {
		return "", invalid("event must be COMMENT, APPROVE or REQUEST_CHANGES")
	}
	return s.reviewAction(session, id, req.Event, req.Edited)
}

// DiscardReview deletes the pending review of a review item. It returns
// discarded, or gone when GitHub no longer has a pending review.
func (s *Service) DiscardReview(session, id string) (string, error) {
	return s.reviewAction(session, id, "", false)
}

// reviewAction runs gh for a review item and writes the answer. An empty event
// is a discard. gh runs outside answerMu; only the answer is written under it,
// and an item has one action at a time, so a double tap runs gh once.
func (s *Service) reviewAction(session, id, event string, edited bool) (string, error) {
	it, err := s.open(session, id)
	if err != nil {
		return "", err
	}
	if it.Kind != KindReview {
		return "", invalid("item %s is a %s, not a review", id, it.Kind)
	}
	t, summary, err := targetOf(it)
	if err != nil {
		return "", err
	}
	g, err := s.ghFor(session, t.host)
	if err != nil {
		return "", err
	}
	key := session + "/" + id
	s.actMu.Lock()
	if s.acting[key] {
		s.actMu.Unlock()
		return "", ErrBusy
	}
	if s.acting == nil {
		s.acting = map[string]bool{}
	}
	s.acting[key] = true
	s.actMu.Unlock()
	defer func() {
		s.actMu.Lock()
		delete(s.acting, key)
		s.actMu.Unlock()
	}()

	action, gerr := "", (*GhError)(nil)
	if event == "" {
		action = "discarded"
		_, gerr = g.call("DELETE", t.endpoint(), nil)
	} else {
		var current []byte
		if current, gerr = g.call("GET", t.endpoint(), nil); gerr == nil {
			var r struct {
				Body string `json:"body"`
			}
			_ = json.Unmarshal(current, &r)
			if r.Body == "" {
				r.Body = summary
			}
			action = "submitted"
			_, gerr = g.call("POST", t.endpoint()+"/events", map[string]string{"event": event, "body": r.Body})
		}
	}
	if gerr != nil {
		if !gerr.gone() {
			return "", gerr
		}
		action = "gone"
	}
	err = s.Answer(session, id, KindReview, func(Item) (any, []outcomes.Record, error) {
		file := struct {
			Version  int    `json:"version"`
			ID       string `json:"id"`
			Kind     string `json:"kind"`
			Answered string `json:"answered"`
			Action   string `json:"action"`
			Event    string `json:"event,omitempty"`
		}{1, id, KindReview, time.Now().UTC().Format(time.RFC3339), action, ""}
		rec := outcomes.Record{Fields: map[string]any{"item": id, "kind": KindReview}}
		switch action {
		case "submitted":
			file.Event = event
			rec.Event = "review_submitted"
			rec.Fields["review_event"], rec.Fields["edited"] = event, edited
		case "gone":
			rec.Fields["external"] = true
			fallthrough
		default:
			rec.Event = "review_discarded"
		}
		return file, []outcomes.Record{rec}, nil
	})
	return action, err
}
