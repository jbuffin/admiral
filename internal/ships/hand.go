package ships

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	"unicode"

	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/outcomes"
	"github.com/jbuffin/admiral/internal/sessionhost"
)

const (
	maxHandText = 4 << 10
	handRefFile = "ref"
	maxRefRead  = 4 << 10
	noRefFile   = "the ship wrote no ref file"
)

// handRequest is what the engineer asked for.
type handRequest struct {
	Kind string `json:"kind"`
	Text string `json:"text,omitempty"`
}

// handEntry is a hand's state, kept in memory only: a ship can rewrite its own pane options and anything under
// $ADMIRAL_HOME, so the relaunch and the checks come from what Admiral saw when the engineer asked.
type handEntry struct {
	ask       handRequest
	req       launcher.Request // the relaunch, without its text, which is the ref
	fleet     string
	harness   string
	dir       string
	skillKind string
	slug      string
	issue     string
	id        string
	sent      atomic.Bool
}

var issueRef = regexp.MustCompile(`^https://github\.com/([^/\s]+/[^/\s]+)/issues/[0-9]+$`)

// beingHanded is whether a hand to Commodore is under way for the ship: Admiral holds its entry, or its pane carries the mark.
func (s *Service) beingHanded(sess sessionhost.Session) bool {
	_, has := s.hands.Load(sess.Name)
	return has || sess.Options[sessionhost.OptHanding] != ""
}

func (s *Service) handDir(session string) (string, bool) {
	if session == "." || !filepath.IsLocal(session) || strings.ContainsRune(session, filepath.Separator) {
		return "", false
	}
	home, err := filepath.Abs(s.Launcher.Home)
	if err != nil {
		return "", false
	}
	return filepath.Join(home, "ships", session), true
}

// serveHand starts Hand to Commodore: the ship is asked for a ref once its turn has ended, and when the turn that
// ask starts has ended the ref is checked and the ship is stopped and relaunched as Commodore on it.
func (s *Service) serveHand(w http.ResponseWriter, r *http.Request) {
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
	var body handRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "request is not valid JSON: "+err.Error())
		return
	}
	switch body.Kind {
	case "issue":
		body.Text = ""
	case "other":
		body.Text = strings.TrimSpace(body.Text)
		if msg := checkHandText(body.Text); msg != "" {
			writeError(w, http.StatusBadRequest, msg)
			return
		}
	default:
		writeError(w, http.StatusBadRequest, `kind must be "issue" or "other"`)
		return
	}
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
	case handNotClaude(session, o["harness"]) != "":
		writeError(w, http.StatusConflict, handNotClaude(session, o["harness"]))
		return
	case o["scout"] != "":
		writeError(w, http.StatusBadRequest, "a scout cannot be handed on: make it a fresh launch instead")
		return
	case o["skill-kind"] == "commodore":
		writeError(w, http.StatusConflict, "ship "+session+" is already Commodore")
		return
	}
	if msg := s.handBlocked(o["fleet"], session); msg != "" {
		writeError(w, http.StatusConflict, "ship "+session+" "+msg)
		return
	}
	id, bound := s.conversationOf(sess)
	if !bound {
		writeError(w, http.StatusConflict, "ship "+session+" has no conversation Admiral can resume: its session id does not match its start command")
		return
	}
	dir, ok := s.handDir(session)
	if !ok {
		writeError(w, http.StatusInternalServerError, "ship "+session+" has no ref directory")
		return
	}
	slug, _ := launcher.OriginSlug(o["dir"])
	e := &handEntry{ask: body, req: handLaunch(sess, id), fleet: o["fleet"], harness: o["harness"], dir: o["dir"],
		skillKind: o["skill-kind"], slug: slug, issue: issueOf(o), id: id}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = os.Remove(filepath.Join(dir, handRefFile))
	s.hands.Store(session, e)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Host.SetOptions(ctx, session, map[string]string{sessionhost.OptHanding: "queued", sessionhost.OptHandFailed: ""}); err != nil {
		s.hands.Delete(session)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// a ship can set the option on its own pane, which wins over the session's
	_ = s.Host.UnsetPaneOption(ctx, session, sessionhost.OptHandFailed)
	s.record(outcomes.Record{Ship: session, Event: "handing", Fields: map[string]any{"kind": body.Kind}}, nil)
	_ = s.Host.Sync(ctx)
	s.broadcast()
	s.advanceHands()
	writeJSON(w, http.StatusAccepted, map[string]any{"handing": true})
}

// handNotClaude is why a ship cannot be handed because of its harness, or "".
func handNotClaude(session, harness string) string {
	if harness == "claude" {
		return ""
	}
	return "ship " + session + " runs on " + harness + ": only a Claude ship can be handed to Commodore"
}

// handBlocked says why a ship cannot be handed because of how it was launched, or "". It fails closed: a trigger log
// that cannot be read refuses the hand.
func (s *Service) handBlocked(fleet, session string) string {
	if s.EventShip == nil {
		return ""
	}
	restricted, fromEvent, err := s.EventShip(fleet, session)
	switch {
	case err != nil:
		return "cannot be handed to Commodore: Admiral could not tell whether an outside event launched it: " + err.Error()
	case restricted:
		return "is restricted: a restricted ship cannot be handed to Commodore"
	case fromEvent:
		return "was launched from an outside event: it cannot be handed to Commodore"
	}
	return ""
}

func checkHandText(text string) string {
	switch {
	case text == "":
		return "text is required for kind other"
	case len(text) > maxHandText:
		return "text is over " + strconv.Itoa(maxHandText) + " bytes"
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
	_, _, id, ok := s.conversationBinding(sess)
	return id, ok
}

// advanceHands moves each handed ship one step: a ship whose own turn has ended is asked for a ref, and one that was
// asked has the ref checked and is relaunched as Commodore. Only a ship with a hand entry moves; the pane mark is a label.
func (s *Service) advanceHands() {
	sessions := s.Host.Sessions()
	byName := map[string]sessionhost.Session{}
	for _, sess := range sessions {
		byName[sess.Name] = sess
	}
	s.hands.Range(func(k, v any) bool {
		name, e := k.(string), v.(*handEntry)
		if _, busy := s.handing.LoadOrStore(name, true); busy {
			return true
		}
		sess, ok := byName[name]
		switch {
		case !ok:
			s.handing.Delete(name)
			s.endHand(name, "the ship ended before the hand did")
		case sess.PaneDead || status(sess, time.Now()) == StatusAnchored || status(sess, time.Now()) == StatusSailingOff:
			s.failHand(sess, e, "the ship exited before the hand did")
			s.handing.Delete(name)
		case status(sess, time.Now()) != StatusNeedsYou || reasonOf(sess) != "stop":
			s.handing.Delete(name)
		default:
			s.settleWG.Add(1)
			go func() {
				defer s.settleWG.Done()
				defer s.handing.Delete(name)
				if e.sent.Load() {
					s.finishHand(sess, e)
					return
				}
				s.sendHandPrompt(sess, e)
			}()
		}
		return true
	})
	for _, sess := range sessions {
		if _, has := s.hands.Load(sess.Name); sess.IsShip() && sess.Options[sessionhost.OptHanding] != "" && !has {
			s.clearOrphanHand(sess.Name)
		}
	}
}

// clearOrphanHand ends a mark nothing remembers: Admiral restarted during the hand, or the ship set the mark itself; either way it lost track.
func (s *Service) clearOrphanHand(name string) {
	if _, busy := s.handing.LoadOrStore(name, true); busy {
		return
	}
	defer s.handing.Delete(name)
	cur, ok := s.find(name)
	if _, has := s.hands.Load(name); !ok || has || cur.Options[sessionhost.OptHanding] == "" {
		return
	}
	const why = "Admiral lost track of the hand"
	s.clearHandMark(name, why)
	s.recordHandFailure(name, "", why)
	s.broadcast()
}

// endHand closes out a hand whose ship is gone: the entry goes and the failure is recorded.
func (s *Service) endHand(name, why string) {
	if v, ok := s.hands.LoadAndDelete(name); ok {
		s.removeHandRef(name)
		s.recordHandFailure(name, v.(*handEntry).id, why)
	}
}

func (s *Service) sendHandPrompt(sess sessionhost.Session, e *handEntry) {
	name := sess.Name
	dir, _ := s.handDir(name)
	ref := filepath.Join(dir, handRefFile)
	_ = os.Remove(ref)
	_, typed, _ := s.TypeResume(name, handPrompt(e, ref))
	if !typed {
		return // the next pass tries again while the turn is still over
	}
	e.sent.Store(true)
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

func handPrompt(e *handEntry, ref string) string {
	var b strings.Builder
	b.WriteString("Hand to Commodore: Admiral will stop this session and continue this conversation as Commodore once your turn ends. Do only what follows, and start no other work.\n\n")
	if e.ask.Kind == "other" {
		b.WriteString("The engineer's words, quoted as data:\n")
		for _, line := range strings.Split(e.ask.Text, "\n") {
			b.WriteString("> " + line + "\n")
		}
		b.WriteString("\nFollow them. Then name what you produced: a GitHub issue URL or the absolute path of a file.\n")
	} else {
		repo := "this repo"
		if e.slug != "" {
			repo = e.slug
		}
		b.WriteString("File the design settled in this conversation as a new issue in " + repo)
		if e.issue != "" {
			b.WriteString(", or comment it on " + e.issue + " when it belongs there")
		}
		b.WriteString(". Then name that issue's URL.\n")
	}
	b.WriteString("\nWrite exactly one line, with nothing else, to " + ref + ": the issue URL or the absolute path. Replace any file already there.")
	return b.String()
}

// handDrift says what about the live ship differs from what Admiral saw when the engineer asked, or "".
func (s *Service) handDrift(sess sessionhost.Session, e *handEntry) string {
	o := sess.Options
	for _, f := range []struct{ name, live, want string }{
		{"harness", o["harness"], e.harness}, {"skill", o["skill-kind"], e.skillKind},
		{"fleet", o["fleet"], e.fleet}, {"dir", o["dir"], e.dir}} {
		if f.live != f.want {
			return "the ship's " + f.name + " changed during the hand"
		}
	}
	if id, bound := s.conversationOf(sess); !bound || id != e.id {
		return "the ship's conversation changed during the hand"
	}
	if msg := s.handBlocked(e.fleet, sess.Name); msg != "" {
		return "the ship " + msg
	}
	return ""
}

// finishHand checks the ref the ship wrote and, when it is good, stops the ship and relaunches its conversation as
// Commodore; a bad ref leaves the ship running and Admiral never asks again by itself.
func (s *Service) finishHand(sess sessionhost.Session, e *handEntry) {
	name := sess.Name
	defer s.hands.Delete(name)
	if why := s.handDrift(sess, e); why != "" {
		s.failHand(sess, e, why)
		return
	}
	ref, why := s.readRef(name, e)
	if why != "" {
		s.failHand(sess, e, why)
		return
	}
	req := e.req
	req.Text = ref
	s.hands.Delete(name) // stopShip would read a hand still held as one the engineer cut short
	if err := s.stopShip(name, map[string]any{"handed": true, "ref": ref, "conversation": e.id}); err != nil {
		s.failHand(sess, e, "the ship could not be stopped: "+err.Error())
		return
	}
	s.removeHandRef(name)
	if !s.waitGone(name) {
		s.recordHandFailure(name, e.id, "the stopped ship was still listed, so it was not relaunched")
		return
	}
	release, lerr := s.admit(req)
	if lerr != nil {
		s.recordHandFailure(name, e.id, "the relaunch was refused: "+lerr.Msg)
		return
	}
	got, lerr := s.LaunchOrder(req)
	if lerr != nil {
		release()
		s.recordHandFailure(name, e.id, "the relaunch failed: "+lerr.Msg)
		return
	}
	s.dropDrafting(got.Session, req.FirstLaunched)
	s.releaseWhenListed(got.Session, release)
	s.record(outcomes.Record{Ship: got.Session, Event: "handed", Fields: map[string]any{"ref": ref, "conversation": e.id}}, nil)
	s.broadcast()
}

// handLaunch is the ship's own request again, as it was launched, but Commodore and resuming its conversation.
// Effort is not kept on the pane, so the relaunch uses the harness's default.
func handLaunch(sess sessionhost.Session, id string) launcher.Request {
	o := sess.Options
	round, _ := strconv.Atoi(o["round"])
	return launcher.Request{Fleet: o["fleet"], Harness: o["harness"], Dir: o["dir"], Model: o["model"],
		Skill: launcher.Skill{Kind: "commodore"}, Source: o["source"], OrderID: o["order"], Title: o["title"], URL: o["url"], Link: o["link"],
		Issues: splitResources(o["issues"]), Panel: o["panel"], Resources: splitResources(o["resources"]), PRs: splitResources(o["prs"]),
		Name: o["name"], Session: sess.Name, Round: round + 1, By: "hand", Via: o["via"], FirstLaunched: firstLaunched(o),
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

// readRefLine reads and removes a one-line ref file a ship wrote, and says why it cannot be used, or "" with the line.
func readRefLine(file string) (line, why string) {
	defer os.Remove(file)
	// O_NOFOLLOW and Fstat on the open file, so a link swapped in after a check is not followed; O_NONBLOCK so a pipe cannot hang the read.
	f, err := os.OpenFile(file, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return "", noRefFile
	}
	if err != nil {
		return "", "the ref file is not a regular file"
	}
	defer f.Close()
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		return "", "the ref file is not a regular file"
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxRefRead+1))
	if err != nil || len(raw) > maxRefRead {
		return "", "the ref file cannot be read or is too long"
	}
	line = strings.TrimSpace(string(raw))
	switch {
	case line == "":
		return "", "the ref file is empty"
	case strings.ContainsAny(line, "\r\n"):
		return "", "the ref file holds more than one line"
	}
	return line, ""
}

// readRef reads and removes the ref file and says why it cannot be used, or "" with the ref.
func (s *Service) readRef(name string, e *handEntry) (ref, why string) {
	dir, ok := s.handDir(name)
	if !ok {
		return "", "the ship has no ref directory"
	}
	line, why := readRefLine(filepath.Join(dir, handRefFile))
	if why != "" {
		return "", why
	}
	if m := issueRef.FindStringSubmatch(line); m != nil {
		if e.slug == "" || !strings.EqualFold(m[1], e.slug) {
			return "", "the issue " + line + " is not in the ship's repo"
		}
		return line, ""
	}
	if !filepath.IsAbs(line) {
		return "", "the ref is neither an issue in the ship's repo nor an absolute path"
	}
	return checkRefPath(e.dir, dir, line)
}

// checkRefPath resolves a path ref and answers the resolved path, which is what Commodore is given.
func checkRefPath(checkout, shipDir, path string) (string, string) {
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", "the ref names a file that does not exist"
	}
	info, err := os.Stat(real)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return "", "the ref names something that is not a non-empty file"
	}
	if realShip, err := filepath.EvalSymlinks(shipDir); err == nil && real == filepath.Join(realShip, handRefFile) {
		return "", "the ref names the ref file itself"
	}
	for _, root := range []string{checkout, shipDir} {
		if r, err := filepath.EvalSymlinks(root); err == nil && within(r, real) {
			return real, ""
		}
	}
	return "", "the ref names a file outside the ship's checkout and its ref directory"
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && filepath.IsLocal(rel)
}

func (s *Service) removeHandRef(name string) {
	if dir, ok := s.handDir(name); ok {
		_ = os.Remove(filepath.Join(dir, handRefFile))
	}
}

// clearHandMark ends the mark and leaves why on the ship in the same write; why is "" for a ship that is no longer live.
func (s *Service) clearHandMark(name, why string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.Host.SetOptions(ctx, name, map[string]string{sessionhost.OptHanding: "", sessionhost.OptHandFailed: why})
	// a ship can set the mark on its own pane, which wins over the session's
	_ = s.Host.UnsetPaneOption(ctx, name, sessionhost.OptHanding)
	_ = s.Host.Sync(ctx)
}

// failHand leaves a running ship running: the entry and mark go and the reason is recorded.
func (s *Service) failHand(sess sessionhost.Session, e *handEntry, why string) {
	s.hands.Delete(sess.Name)
	s.removeHandRef(sess.Name)
	exited := sess.PaneDead || status(sess, time.Now()) == StatusAnchored || status(sess, time.Now()) == StatusSailingOff
	if exited {
		s.clearHandMark(sess.Name, "")
	} else {
		s.clearHandMark(sess.Name, why)
	}
	s.recordHandFailure(sess.Name, e.id, why)
	s.broadcast()
}

func (s *Service) recordHandFailure(name, id, why string) {
	fields := map[string]any{"reason": why}
	if id != "" {
		fields["conversation"] = id
	}
	s.record(outcomes.Record{Ship: name, Event: "handing_failed", Fields: fields}, nil)
	fmt.Fprintf(os.Stderr, "admiral: ship %s: not handed to Commodore: %s\n", name, why)
}
