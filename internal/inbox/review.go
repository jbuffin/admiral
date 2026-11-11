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

// A gh call is never retried.
const ghTimeout = 30 * time.Second

// Caps on what is read from gh: its stderr reaches the UI, so it is short.
const (
	ghStderrCap = 4 << 10
	ghStdoutCap = 1 << 20
)

const (
	ActionSubmitted = "submitted"
	ActionDiscarded = "discarded"
	ActionGone      = "gone" // GitHub no longer had a pending review to act on
)

var ErrBusy = errors.New("another action on this item is still running")

// GhError's Msg is what the engineer sees. A review GitHub reports gone is not
// an error to the caller.
type GhError struct {
	Msg     string
	Missing bool   // gh is not on PATH
	status  int    // the HTTP status gh printed, 0 if none
	detail  string // GitHub's message and errors, for gone
}

func (e *GhError) Error() string { return e.Msg }

// gone is GitHub saying the review is not there (404) or is no longer pending (a
// 422 whose message or errors name pending; its documentation_url does not count).
func (e *GhError) gone() bool {
	return e.status == 404 || (e.status == 422 && strings.Contains(strings.ToLower(e.detail), "pending"))
}

var httpStatus = regexp.MustCompile(`\(HTTP (\d{3})\)`)

type target struct {
	host, owner, repo, pr, reviewID string
}

// prURL allows no user info, port, query, fragment, escape or further path.
var prURL = regexp.MustCompile(`^https://([A-Za-z0-9.-]+)/([A-Za-z0-9-]+)/([A-Za-z0-9._-]+)/pull/([1-9][0-9]{0,8})$`)

var reviewIDPattern = regexp.MustCompile(`^[1-9][0-9]{0,17}$`)

// Only these parts of the item's text ever reach gh, and only as a path.
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

func (t target) pullEndpoint() string {
	return "repos/" + t.owner + "/" + t.repo + "/pulls/" + t.pr
}

func (t target) endpoint() string {
	return "repos/" + t.owner + "/" + t.repo + "/pulls/" + t.pr + "/reviews/" + t.reviewID
}

type ghRun struct {
	bin  string
	env  []string
	host string
	note string // appended to a failure: where gh ran
}

// A ship Admiral has no dock for runs gh in Admiral's own environment.
func (s *Service) ghFor(session, host string) (*ghRun, error) {
	bin, err := exec.LookPath("gh")
	if err != nil {
		return nil, &GhError{Msg: "gh was not found on Admiral's PATH, so the review cannot be changed on GitHub", Missing: true}
	}
	g := &ghRun{bin: bin, host: host}
	if s.Dock != nil {
		env, why := s.Dock(session)
		g.env, g.note = env, why+", so gh ran in Admiral's own environment"
	} else {
		g.note = "no dock lookup, so gh ran in Admiral's own environment"
	}
	if g.env == nil {
		g.env = os.Environ()
	} else {
		g.note = ""
	}
	g.env = append(g.env, "GH_PROMPT_DISABLED=1", "GH_NO_UPDATE_NOTIFIER=1")
	return g, nil
}

// The item's text only ever reaches gh in the body, never in args.
func (g *ghRun) call(method, endpoint string, body any) ([]byte, *GhError) {
	// Always the PR's host: without it a GH_HOST in the environment would take the call elsewhere.
	args := []string{"api", "--method", method, "--hostname", g.host}
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
	texts := githubTexts(stdout.buf.Bytes())
	e := &GhError{detail: strings.Join(texts, "\n")}
	if e.detail == "" {
		e.detail = stderr.buf.String()
	}
	if m := httpStatus.FindStringSubmatch(stderr.buf.String()); m != nil {
		fmt.Sscan(m[1], &e.status)
	}
	switch msg := strings.TrimSpace(stderr.buf.String()); {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		e.Msg = fmt.Sprintf("gh did not answer within %s", ghTimeout)
	case msg != "":
		e.Msg = "gh failed: " + msg
		for _, t := range texts {
			if !strings.Contains(msg, t) {
				e.Msg += "\n" + t
			}
		}
	default:
		e.Msg = "gh failed: " + err.Error()
	}
	// gh's text is cut first, so the note on where gh ran always survives.
	if len(e.Msg) > ghStderrCap {
		e.Msg = strings.ToValidUTF8(e.Msg[:ghStderrCap], "")
	}
	if g.note != "" {
		e.Msg += " (" + g.note + ")"
	}
	return nil, e
}

func githubTexts(body []byte) []string {
	var j struct {
		Message string            `json:"message"`
		Errors  []json.RawMessage `json:"errors"`
	}
	if json.Unmarshal(body, &j) != nil {
		return nil
	}
	var texts []string
	add := func(t string) {
		if t != "" {
			texts = append(texts, t)
		}
	}
	add(j.Message)
	for _, raw := range j.Errors {
		var s string
		var o struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(raw, &s) == nil {
			add(s)
		} else if json.Unmarshal(raw, &o) == nil {
			add(o.Message)
		}
	}
	return texts
}

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

type SubmitRequest struct {
	Event  string `json:"event"`
	Edited bool   `json:"edited"`
}

var reviewEvents = map[string]bool{"COMMENT": true, "APPROVE": true, "REQUEST_CHANGES": true}

// InvalidTarget and Forgotten mean gh never ran: the item names no review GitHub
// could have, so nothing changed there.
type ReviewResult struct {
	Action        string
	InvalidTarget bool
	Forgotten     bool
}

// SubmitReview uses the body the review has on GitHub now, since the engineer
// may have edited it there, or the item's summary when that is empty.
func (s *Service) SubmitReview(session, id string, req SubmitRequest) (ReviewResult, error) {
	if !reviewEvents[req.Event] {
		return ReviewResult{}, invalid("event must be COMMENT, APPROVE or REQUEST_CHANGES")
	}
	return s.reviewAction(session, id, false, false, req.Event, req.Edited)
}

// DiscardReview of an invalid target runs no gh, so an item no other action can
// answer still has a way out.
func (s *Service) DiscardReview(session, id string) (ReviewResult, error) {
	return s.reviewAction(session, id, true, false, "", false)
}

// ForgetReview is for a review no action can clear any more (a deleted repository,
// lost access, a gone host). It runs no gh; Submit stays strict.
func (s *Service) ForgetReview(session, id string) (ReviewResult, error) {
	return s.reviewAction(session, id, true, true, "", false)
}

// reviewAction runs gh outside answerMu, and an item has one action at a time,
// so a double tap runs gh once.
func (s *Service) reviewAction(session, id string, discard, forget bool, event string, edited bool) (ReviewResult, error) {
	it, err := s.open(session, id)
	if err != nil {
		return ReviewResult{}, err
	}
	if it.Kind != KindReview {
		return ReviewResult{}, invalid("item %s is a %s, not a review", id, it.Kind)
	}
	t, summary, err := targetOf(it)
	if err != nil && !discard {
		return ReviewResult{}, err
	}
	invalidTarget := err != nil && !forget
	var g *ghRun
	if !invalidTarget && !forget {
		if g, err = s.ghFor(session, t.host); err != nil {
			return ReviewResult{}, err
		}
	}
	key := session + "/" + id
	s.actMu.Lock()
	if s.acting[key] {
		s.actMu.Unlock()
		return ReviewResult{}, ErrBusy
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
	// An answer may have landed while this request waited for the item.
	if _, err := s.open(session, id); err != nil {
		return ReviewResult{}, err
	}

	action, gerr := ActionDiscarded, (*GhError)(nil)
	switch {
	case invalidTarget || forget:
	case discard:
		_, gerr = g.call("DELETE", t.endpoint(), nil)
	default:
		action = ActionSubmitted
		var current []byte
		if current, gerr = g.call("GET", t.endpoint(), nil); gerr == nil {
			var r struct {
				State string `json:"state"`
				Body  string `json:"body"`
			}
			_ = json.Unmarshal(current, &r)
			switch {
			case r.State != "" && r.State != "PENDING": // submitted on GitHub already
				action = ActionGone
			default:
				if r.Body == "" {
					r.Body = summary
				}
				_, gerr = g.call("POST", t.endpoint()+"/events", map[string]string{"event": event, "body": r.Body})
			}
		}
	}
	if gerr != nil {
		if !gerr.gone() {
			return ReviewResult{}, gerr
		}
		// GitHub answers 404 for a repository the token cannot see, too: the review
		// is gone only if the pull request is still there to be seen.
		if gerr.status == 404 {
			if _, perr := g.call("GET", t.pullEndpoint(), nil); perr != nil {
				if perr.status == 404 {
					return ReviewResult{}, &GhError{Msg: "GitHub did not find the review or its pull request, so the token may not reach it: " + perr.Msg, status: perr.status}
				}
				return ReviewResult{}, perr
			}
		}
		action = ActionGone
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
		rec := outcomes.Record{Event: "review_discarded", Fields: map[string]any{"item": id, "kind": KindReview}}
		switch {
		case action == ActionSubmitted:
			file.Event = event
			rec.Event = "review_submitted"
			rec.Fields["review_event"], rec.Fields["edited"] = event, edited
		case forget:
			rec.Fields["unreachable"] = true
		case action == ActionGone:
			rec.Fields["external"] = true
		case invalidTarget:
			rec.Fields["invalid_target"] = true
		}
		return file, []outcomes.Record{rec}, nil
	})
	return ReviewResult{action, invalidTarget && action == ActionDiscarded, forget}, err
}
