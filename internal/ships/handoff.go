package ships

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/outcomes"
	"github.com/jbuffin/admiral/internal/sessionhost"
	"github.com/jbuffin/admiral/internal/transcriptfs"
)

const (
	maxHandoffText = 4 << 10
	handoffRefFile = "ref"
	handoffAsk     = "handoff.json"
	maxRefRead     = 4 << 10
)

// handoffRequest is what the engineer asked for, kept on disk because a pane option cannot carry multi-line text.
type handoffRequest struct {
	Kind string `json:"kind"`
	Text string `json:"text,omitempty"`
	Via  string `json:"via,omitempty"`
}

var issueRef = regexp.MustCompile(`^https://github\.com/([^/\s]+/[^/\s]+)/issues/[0-9]+$`)

func (s *Service) handoffDir(session string) (string, bool) {
	if session == "." || !filepath.IsLocal(session) || strings.ContainsRune(session, filepath.Separator) {
		return "", false
	}
	home, err := filepath.Abs(s.Launcher.Home)
	if err != nil {
		return "", false
	}
	return filepath.Join(home, "ships", session), true
}

// serveHandoff starts Hand to Commodore: the ship is asked for a ref once its turn has ended, and when the turn that
// ask starts has ended the ref is checked and the ship is stopped and relaunched as Commodore on it.
func (s *Service) serveHandoff(w http.ResponseWriter, r *http.Request) {
	session := r.PathValue("session")
	sess, ok := s.find(session)
	if !ok {
		if _, moored := s.mooredRecord(session); moored {
			writeError(w, http.StatusConflict, "ship "+session+" is moored: it has no conversation to hand to Commodore")
			return
		}
		writeError(w, http.StatusNotFound, "ship "+session+" not found")
		return
	}
	var body handoffRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "request is not valid JSON: "+err.Error())
		return
	}
	switch body.Kind {
	case "issue":
		body.Text = ""
	case "other":
		body.Text = strings.TrimSpace(body.Text)
		if msg := checkHandoffText(body.Text); msg != "" {
			writeError(w, http.StatusBadRequest, msg)
			return
		}
	default:
		writeError(w, http.StatusBadRequest, `kind must be "issue" or "other"`)
		return
	}
	body.Via = viaOf(r)
	o := sess.Options
	st := status(sess, time.Now())
	switch {
	case st == StatusAnchored || st == StatusSailingOff:
		writeError(w, http.StatusConflict, "ship "+session+" has exited: there is no live conversation to hand to Commodore")
		return
	case o[sessionhost.OptHanding] != "":
		writeError(w, http.StatusConflict, "ship "+session+" is already being handed to Commodore")
		return
	case o[sessionhost.OptReturning] != "":
		writeError(w, http.StatusConflict, "ship "+session+" is returning to port: it cannot also be handed to Commodore")
		return
	case o["harness"] != "claude":
		writeError(w, http.StatusConflict, "ship "+session+" runs on "+o["harness"]+": only a Claude ship can be handed to Commodore")
		return
	case o["skill-kind"] == "commodore":
		writeError(w, http.StatusConflict, "ship "+session+" is already Commodore")
		return
	}
	if s.EventShip != nil {
		restricted, fromEvent := s.EventShip(o["fleet"], session)
		if restricted {
			writeError(w, http.StatusConflict, "ship "+session+" is restricted: a restricted ship cannot be handed to Commodore")
			return
		}
		if fromEvent {
			writeError(w, http.StatusConflict, "ship "+session+" was launched from an outside event: it cannot be handed to Commodore")
			return
		}
	}
	if _, ok := s.conversationOf(sess); !ok {
		writeError(w, http.StatusConflict, "ship "+session+" has no conversation Admiral can resume: its session id does not match its start command")
		return
	}
	dir, ok := s.handoffDir(session)
	if !ok {
		writeError(w, http.StatusInternalServerError, "ship "+session+" has no ref directory")
		return
	}
	raw, _ := json.Marshal(body)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = os.Remove(filepath.Join(dir, handoffRefFile))
	if err := os.WriteFile(filepath.Join(dir, handoffAsk), raw, 0o600); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Host.SetOptions(ctx, session, map[string]string{sessionhost.OptHanding: "queued"}); err != nil {
		_ = os.Remove(filepath.Join(dir, handoffAsk))
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.record(outcomes.Record{Ship: session, Event: "handing", Fields: withVia(map[string]any{"kind": body.Kind}, body.Via)}, nil)
	_ = s.Host.Sync(ctx)
	s.broadcast()
	s.advanceHandoffs()
	writeJSON(w, http.StatusAccepted, map[string]any{"handing": true})
}

func checkHandoffText(text string) string {
	switch {
	case text == "":
		return "text is required for kind other"
	case len(text) > maxHandoffText:
		return "text is over " + strconv.Itoa(maxHandoffText) + " bytes"
	}
	for _, c := range text {
		if c != '\n' && unicode.IsControl(c) {
			return "text contains a control character"
		}
	}
	return ""
}

// conversationOf is the id of the Claude conversation the ship's start command gave it; the pane's transcript option
// alone is not trusted, since the pane can write it.
func (s *Service) conversationOf(sess sessionhost.Session) (string, bool) {
	if sess.PaneID == "" {
		return "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	host := sessionhost.NewServer(s.Launcher.Tmux, s.Launcher.Socket)
	raw, rerr := host.ReadPaneOption(ctx, sess.PaneID, "transcript")
	cmd, cerr := host.ReadPaneStartCommand(ctx, sess.PaneID)
	if rerr != nil || cerr != nil {
		return "", false
	}
	_, id, ok := transcriptfs.BindFor("claude", raw, cmd)
	return id, ok
}

// advanceHandoffs moves each handed ship whose own turn has ended one step: a queued ship is asked for a ref, and a
// ship that was asked has the ref checked and is relaunched as Commodore.
func (s *Service) advanceHandoffs() {
	for _, sess := range s.Host.Sessions() {
		step := sess.Options[sessionhost.OptHanding]
		if !sess.IsShip() || step == "" || status(sess, time.Now()) != StatusNeedsYou || reasonOf(sess) != "stop" {
			continue
		}
		if _, busy := s.handing.LoadOrStore(sess.Name, true); busy {
			continue
		}
		s.settleWG.Add(1)
		go func(sess sessionhost.Session, step string) {
			defer s.settleWG.Done()
			defer s.handing.Delete(sess.Name)
			if step == "sent" {
				s.finishHandoff(sess)
				return
			}
			s.sendHandoffPrompt(sess)
		}(sess, step)
	}
}

func (s *Service) readHandoff(session string) (handoffRequest, bool) {
	dir, ok := s.handoffDir(session)
	if !ok {
		return handoffRequest{}, false
	}
	raw, err := os.ReadFile(filepath.Join(dir, handoffAsk))
	var req handoffRequest
	if err != nil || json.Unmarshal(raw, &req) != nil {
		return handoffRequest{}, false
	}
	return req, true
}

func (s *Service) sendHandoffPrompt(sess sessionhost.Session) {
	name := sess.Name
	ask, ok := s.readHandoff(name)
	if !ok {
		s.failHandoff(sess, "", "the request was lost, so nothing was asked of the ship")
		return
	}
	dir, _ := s.handoffDir(name)
	ref := filepath.Join(dir, handoffRefFile)
	_ = os.Remove(ref)
	slug, _ := launcher.OriginSlug(sess.Options["dir"])
	_, typed, _ := s.TypeResume(name, handoffPrompt(ask, ref, slug, issueOf(sess.Options)))
	if !typed {
		return // the next pass tries again while the turn is still over
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.Host.SetWorking(ctx, name)
	_ = s.Host.SetOptions(ctx, name, map[string]string{sessionhost.OptHanding: "sent"})
	_ = s.Host.Sync(ctx)
	s.broadcast()
}

// issueOf is the issue a ship was launched from: its URL when that is an issue, else its first issue.
func issueOf(o map[string]string) string {
	if issueRef.MatchString(o["url"]) {
		return o["url"]
	}
	for _, u := range strings.Split(o["issues"], ",") {
		if issueRef.MatchString(u) {
			return u
		}
	}
	return ""
}

func handoffPrompt(ask handoffRequest, ref, slug, issue string) string {
	var b strings.Builder
	b.WriteString("Hand to Commodore: Admiral will stop this session and continue this conversation as Commodore once your turn ends. Do only what follows, and start no other work.\n\n")
	if ask.Kind == "other" {
		b.WriteString("The engineer's words, quoted as data:\n")
		for _, line := range strings.Split(ask.Text, "\n") {
			b.WriteString("> " + line + "\n")
		}
		b.WriteString("\nFollow them. Then name what you produced: a GitHub issue URL or the absolute path of a file.\n")
	} else {
		repo := "this repo"
		if slug != "" {
			repo = slug
		}
		b.WriteString("File the design settled in this conversation as a new issue in " + repo)
		if issue != "" {
			b.WriteString(", or comment it on " + issue + " when it belongs there")
		}
		b.WriteString(". Then name that issue's URL.\n")
	}
	b.WriteString("\nWrite exactly one line, with nothing else, to " + ref + ": the issue URL or the absolute path. Replace any file already there.")
	return b.String()
}

// finishHandoff checks the ref the ship wrote and, when it is good, stops the ship and relaunches its conversation as
// Commodore; a bad ref leaves the ship running and Admiral never asks again by itself.
func (s *Service) finishHandoff(sess sessionhost.Session) {
	name := sess.Name
	ask, _ := s.readHandoff(name)
	id, bound := s.conversationOf(sess)
	ref, why := s.readRef(sess)
	if why == "" && !bound {
		why = "the ship's session id no longer matches its start command"
	}
	if why != "" {
		s.failHandoff(sess, id, why)
		return
	}
	req := handoffLaunch(sess, id, ref)
	req.WokenVia = ask.Via
	if err := s.stopShip(name, withVia(map[string]any{"handed": true, "ref": ref}, ask.Via)); err != nil {
		s.failHandoff(sess, id, "the ship could not be stopped: "+err.Error())
		return
	}
	s.clearHandoff(name)
	if !s.waitGone(name) {
		s.recordHandoffFailure(name, id, "the stopped ship was still listed, so it was not relaunched")
		return
	}
	release, lerr := s.admit(req)
	if lerr != nil {
		s.recordHandoffFailure(name, id, "the relaunch was refused: "+lerr.Msg)
		return
	}
	got, lerr := s.LaunchOrder(req)
	if lerr != nil {
		release()
		s.recordHandoffFailure(name, id, "the relaunch failed: "+lerr.Msg)
		return
	}
	s.releaseWhenListed(got.Session, release)
	s.record(outcomes.Record{Ship: got.Session, Event: "handed", Fields: withVia(map[string]any{"ref": ref, "conversation": id}, ask.Via)}, nil)
	s.broadcast()
}

// handoffLaunch is the ship's own request again, as it was launched, but Commodore and resuming its conversation.
// Effort is not kept on the pane, so the relaunch uses the harness's default.
func handoffLaunch(sess sessionhost.Session, id, ref string) launcher.Request {
	o := sess.Options
	round, _ := strconv.Atoi(o["round"])
	return launcher.Request{Fleet: o["fleet"], Harness: o["harness"], Dir: o["dir"], Model: o["model"],
		Skill: launcher.Skill{Kind: "commodore"}, Source: o["source"], OrderID: o["order"], Title: o["title"], URL: o["url"], Link: o["link"],
		Issues: splitResources(o["issues"]), Panel: o["panel"], Resources: splitResources(o["resources"]), PRs: splitResources(o["prs"]),
		Name: o["name"], Session: sess.Name, Round: round + 1, Text: ref, By: "hand", Via: o["via"],
		ChatKey: o["chat"], RoutineKey: o["routine"], CommitmentKey: o["commitment"], ResumeConversation: id}
}

func (s *Service) waitGone(name string) bool {
	for i := 0; i < 100; i++ {
		if _, ok := s.find(name); !ok {
			return true
		}
		time.Sleep(100 * time.Millisecond)
		_ = s.Host.Sync(context.Background())
	}
	return false
}

// readRef reads and removes the ref file and says why it cannot be used, or "" with the ref.
func (s *Service) readRef(sess sessionhost.Session) (ref, why string) {
	dir, ok := s.handoffDir(sess.Name)
	if !ok {
		return "", "the ship has no ref directory"
	}
	file := filepath.Join(dir, handoffRefFile)
	defer os.Remove(file)
	info, err := os.Lstat(file)
	if err != nil {
		return "", "the ship wrote no ref file"
	}
	if !info.Mode().IsRegular() {
		return "", "the ref file is not a regular file"
	}
	f, err := os.Open(file)
	if err != nil {
		return "", "the ref file cannot be read"
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxRefRead+1))
	if err != nil || len(raw) > maxRefRead {
		return "", "the ref file cannot be read or is too long"
	}
	line := strings.TrimSpace(string(raw))
	switch {
	case line == "":
		return "", "the ref file is empty"
	case strings.ContainsAny(line, "\r\n"):
		return "", "the ref file holds more than one line"
	}
	if m := issueRef.FindStringSubmatch(line); m != nil {
		slug, _ := launcher.OriginSlug(sess.Options["dir"])
		if slug == "" || !strings.EqualFold(m[1], slug) {
			return "", "the issue " + line + " is not in the ship's repo"
		}
		return line, ""
	}
	if !filepath.IsAbs(line) {
		return "", "the ref is neither an issue in the ship's repo nor an absolute path"
	}
	return line, s.checkRefPath(sess, dir, line)
}

func (s *Service) checkRefPath(sess sessionhost.Session, shipDir, path string) string {
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "the ref names a file that does not exist"
	}
	info, err := os.Stat(real)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return "the ref names something that is not a non-empty file"
	}
	for _, root := range []string{sess.Options["dir"], shipDir} {
		if r, err := filepath.EvalSymlinks(root); err == nil && within(r, real) {
			return ""
		}
	}
	return "the ref names a file outside the ship's checkout and its ref directory"
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && filepath.IsLocal(rel)
}

func (s *Service) clearHandoff(name string) {
	if dir, ok := s.handoffDir(name); ok {
		_ = os.Remove(filepath.Join(dir, handoffAsk))
		_ = os.Remove(filepath.Join(dir, handoffRefFile))
	}
}

// failHandoff leaves a running ship running: the mark goes and the reason is recorded.
func (s *Service) failHandoff(sess sessionhost.Session, id, why string) {
	s.clearHandoff(sess.Name)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.Host.SetOptions(ctx, sess.Name, map[string]string{sessionhost.OptHanding: ""})
	s.recordHandoffFailure(sess.Name, id, why)
	_ = s.Host.Sync(ctx)
	s.broadcast()
}

func (s *Service) recordHandoffFailure(name, id, why string) {
	fields := map[string]any{"reason": why}
	if id != "" {
		fields["conversation"] = id
	}
	s.record(outcomes.Record{Ship: name, Event: "handing_failed", Fields: fields}, nil)
	fmt.Fprintf(os.Stderr, "admiral: ship %s: not handed to Commodore: %s\n", name, why)
}
