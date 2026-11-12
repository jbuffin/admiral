package inbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/jbuffin/admiral/internal/actor"
	"github.com/jbuffin/admiral/internal/outcomes"
	"github.com/jbuffin/admiral/internal/server"
	"github.com/jbuffin/admiral/internal/sessionhost"
	"github.com/jbuffin/admiral/internal/state"
)

// settle lets a ship finish writing before the inbox is read again.
const settle = 100 * time.Millisecond

// rescanEvery applies only when the inbox cannot be watched.
const rescanEvery = 30 * time.Second

// ShipInfo is what the inbox shows beside an item of a known ship.
type ShipInfo struct {
	Name, Harness, Fleet, FleetName, FleetColor string
}

type Service struct {
	Home string // absolute
	Hub  *server.Hub
	Ship func(session string) (ShipInfo, bool)
	// Fleet looks a configured fleet up by key; a crew item carries its fleet's tags only while it is configured.
	Fleet func(key string) (FleetInfo, bool)
	// BuiltIns names a fleet's built-in crew members; Keep refuses an ad hoc member of one of those names.
	BuiltIns func(key string) []string
	Outcomes *outcomes.Writer
	// Dock's why says which of the ship and its dock is unknown.
	Dock func(session string) (env []string, why string)
	Kept *state.Store
	// Afloat is whether a session is a ship that is underway or needs you; a ship
	// that is not afloat has no resume.
	Afloat func(session string) bool
	// TypeResume types only while the ship waits at its prompt because its turn ended.
	TypeResume func(session, text string) (afloat, typed bool, err error)
	// LaunchProposal starts a feedback round for a wake proposal.
	LaunchProposal LaunchProposalFunc
	// LaunchRoutine runs a routine proposal.
	LaunchRoutine LaunchRoutineFunc
	// ChatQuestions lists the waiting AdmiralBot chat questions, named and tagged with their fleet.
	ChatQuestions func() []Item
	// AnswerChat answers a chat question where the chat keeps it; the inbox has no copy.
	AnswerChat func(session, id string, req QuestionRequest) error
	// OnChange runs after every change to the list is sent. It must not block.
	OnChange func()

	scanned atomic.Bool

	answerMu sync.Mutex        // one answer at a time
	actMu    sync.Mutex        // guards acting
	acting   map[string]bool   // the items a gh action is running on
	resumeMu sync.Mutex        // one pass over the resume texts at a time
	lastErr  map[string]string // the last failure logged for a resume text; under resumeMu
	resume   chan struct{}     // resume texts may be ready; buffered; under mu
	scanMu   sync.Mutex        // one refresh at a time
	mu       sync.Mutex        // guards items
	items    []Item
	notify   chan struct{} // a ship changed; buffered, so Notify never blocks
	watched  map[string]os.FileInfo
	watcher  *fsnotify.Watcher
}

func (s *Service) List() []Item {
	s.mu.Lock()
	items := append([]Item{}, s.items...)
	s.mu.Unlock()
	now := time.Now()
	items = slices.DeleteFunc(items, func(it Item) bool {
		p, ok := it.RoutineProposal()
		return ok && p.expired(now)
	})
	for i := range items {
		if items[i].Session == AdmiralSession {
			items[i].Name = "Admiral"
			if p, ok := items[i].RoutineProposal(); ok {
				items[i].Fleet = p.Fleet
				if f, ok := s.fleetOf(p.Fleet); ok {
					items[i].FleetName, items[i].FleetColor = f.Name, f.Color
				}
			}
			if o, ok := items[i].CrewObject(); ok && s.Fleet != nil {
				if f, ok := s.Fleet(o.Fleet); ok {
					items[i].Fleet, items[i].FleetName, items[i].FleetColor = f.Key, f.Name, f.Color
				}
			}
		}
	}
	if s.Ship != nil {
		for i := range items {
			if items[i].Session == AdmiralSession {
				continue
			}
			if info, ok := s.Ship(items[i].Session); ok {
				items[i].Name, items[i].Harness = info.Name, info.Harness
				items[i].Fleet, items[i].FleetName, items[i].FleetColor = info.Fleet, info.FleetName, info.FleetColor
			}
		}
	}
	if s.ChatQuestions != nil {
		if qs := s.ChatQuestions(); len(qs) > 0 {
			items = append(items, qs...)
			sort.SliceStable(items, func(i, j int) bool { return before(items[i], items[j]) })
		}
	}
	return items
}

// Scanned is whether the first scan of the inbox directory has finished.
func (s *Service) Scanned() bool { return s.scanned.Load() }

// Changed sends the list to every client, for a change the inbox does not watch for itself.
func (s *Service) Changed() { s.broadcast() }

func (s *Service) message() any { return map[string]any{"type": "inbox", "items": s.List()} }

// Notify never blocks.
func (s *Service) Notify() {
	s.kickResume()
	select {
	case s.notifyChan() <- struct{}{}:
	default:
	}
}

func (s *Service) notifyChan() chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.notify == nil {
		s.notify = make(chan struct{}, 1)
	}
	return s.notify
}

func (s *Service) broadcast() {
	s.Hub.BroadcastOnce("inbox", s.message)
	if s.OnChange != nil {
		s.OnChange()
	}
}

func (s *Service) Connected(c *server.Client) { _ = c.SendOnce("inbox", s.message) }

func (s *Service) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/inbox", s.serveList)
	mux.HandleFunc("POST /api/inbox/{session}/{id}/answer", s.serveAnswer)
	mux.HandleFunc("POST /api/inbox/{session}/{id}/submit", s.serveSubmit)
	mux.HandleFunc("POST /api/inbox/{session}/{id}/discard", s.serveDiscard)
	mux.HandleFunc("POST /api/inbox/{session}/{id}/findings", s.serveFindings)
	mux.HandleFunc("POST /api/inbox/{session}/{id}/remove", s.serveRemove)
	mux.HandleFunc("POST /api/inbox/"+AdmiralSession+"/{id}/crew-notes", s.serveCrewNotes)
	mux.HandleFunc("POST /api/inbox/"+AdmiralSession+"/{id}/crew-member", s.serveCrewMember)
	mux.HandleFunc("POST /api/inbox/"+AdmiralSession+"/{id}/launch", s.serveLaunchProposal)
	mux.HandleFunc("POST /api/inbox/"+AdmiralSession+"/{id}/dismiss", s.serveDismissProposal)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func (s *Service) serveList(w http.ResponseWriter, r *http.Request) {
	items := s.List()
	if fleet, ok := actor.FleetOf(r.Context()); ok {
		items = slices.DeleteFunc(items, func(it Item) bool { return it.Fleet != fleet })
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func decode(w http.ResponseWriter, r *http.Request, max int64, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, max)).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "the request is not a valid JSON object: "+err.Error())
		return false
	}
	return true
}

// serveAnswer is for question items only: a review is answered by submit or
// discard, and findings by findings.
func (s *Service) serveAnswer(w http.ResponseWriter, r *http.Request) {
	session, id := r.PathValue("session"), r.PathValue("id")
	if s.AnswerChat != nil && sessionhost.IsChatSession(session) {
		var req QuestionRequest
		if !decode(w, r, 256<<10, &req) {
			return
		}
		respond(w, session, id, ReviewResult{}, s.AnswerChat(session, id, req))
		return
	}
	it, err := s.open(session, id)
	if err == nil && it.Kind != KindQuestion {
		err = invalid("item %s is a %s: answer it with %s", id, it.Kind, map[string]string{KindReview: "submit or discard", KindFindings: "findings", KindCrewNotes: "crew-notes", KindCrewMember: "crew-member"}[it.Kind])
	}
	if err == nil {
		var req QuestionRequest
		if !decode(w, r, 256<<10, &req) {
			return
		}
		via := ""
		if _, ok := actor.FleetOf(r.Context()); ok {
			via = actor.Via
		}
		err = s.AnswerQuestionVia(session, id, req, via)
	}
	respond(w, session, id, ReviewResult{}, err)
}

func (s *Service) serveSubmit(w http.ResponseWriter, r *http.Request) {
	session, id := r.PathValue("session"), r.PathValue("id")
	var req SubmitRequest
	if !decode(w, r, 64<<10, &req) {
		return
	}
	res, err := s.SubmitReview(session, id, req)
	respond(w, session, id, res, err)
}

func (s *Service) serveDiscard(w http.ResponseWriter, r *http.Request) {
	session, id := r.PathValue("session"), r.PathValue("id")
	var req struct {
		Forget bool `json:"forget"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "the request is not a valid JSON object: "+err.Error())
		return
	}
	discard := s.DiscardReview
	if req.Forget {
		discard = s.ForgetReview
	}
	res, err := discard(session, id)
	respond(w, session, id, res, err)
}

func (s *Service) serveFindings(w http.ResponseWriter, r *http.Request) {
	session, id := r.PathValue("session"), r.PathValue("id")
	var req FindingsRequest
	if !decode(w, r, 1<<20, &req) {
		return
	}
	respond(w, session, id, ReviewResult{}, s.AnswerFindings(session, id, req))
}

func respond(w http.ResponseWriter, session, id string, res ReviewResult, err error) {
	var bad *InvalidError
	var gh *GhError
	var crewRefused *crewRefusedError
	var refused *RefusedError
	switch {
	case errors.As(err, &refused):
		writeError(w, http.StatusConflict, refused.Msg)
	case err == nil:
		out := map[string]any{"answered": id}
		if res.Action != "" {
			out["action"] = res.Action
		}
		if res.InvalidTarget {
			out["invalid_target"] = true // nothing was changed on GitHub
		}
		if res.Forgotten {
			out["forgotten"] = true // likewise
		}
		if res.FileRemoved != nil {
			out["file_removed"] = *res.FileRemoved
		}
		writeJSON(w, http.StatusOK, out)
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "no item "+id+" in the inbox of "+session)
	case errors.Is(err, ErrAnswered):
		writeError(w, http.StatusConflict, "item "+id+" is already answered")
	case errors.Is(err, ErrBusy):
		writeError(w, http.StatusConflict, err.Error())
	case errors.As(err, &bad):
		writeError(w, http.StatusBadRequest, bad.Msg)
	case errors.As(err, &crewRefused):
		body := map[string]string{"error": crewRefused.Msg}
		if crewRefused.Note != "" {
			body["note"] = crewRefused.Note
		}
		writeJSON(w, http.StatusConflict, body)
	case errors.As(err, &gh):
		status := http.StatusBadGateway
		if gh.Missing {
			status = http.StatusServiceUnavailable
		}
		writeError(w, status, gh.Msg)
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

// Refresh syncs the watches first, so a directory that came back cannot hide a
// file renamed into it.
func (s *Service) Refresh() {
	s.scanMu.Lock()
	s.syncWatches()
	items := scan(s.Home)
	s.mu.Lock()
	s.items = items // under scanMu, so an older scan cannot land after a newer one
	s.mu.Unlock()
	s.scanMu.Unlock()
	s.captureAll() // answers written by someone else count too
	s.scanned.Store(true)
	s.broadcast()
}

// Run also watches $ADMIRAL_HOME, since inbox/ may not exist yet.
func (s *Service) Run(ctx context.Context) {
	_ = os.MkdirAll(filepath.Join(s.Home, "inbox"), 0o700)
	var events <-chan fsnotify.Event
	var errs <-chan error
	if w, err := fsnotify.NewWatcher(); err != nil {
		fmt.Fprintln(os.Stderr, "admiral: the inbox cannot be watched:", err)
	} else {
		defer w.Close()
		s.scanMu.Lock()
		s.watcher, s.watched = w, map[string]os.FileInfo{}
		s.scanMu.Unlock()
		events, errs = w.Events, w.Errors
	}
	go s.resumeLoop(ctx)
	s.captureAll() // before the sweep deletes the report of an inbox answered while Admiral was down
	s.sweepKept()
	s.Refresh()
	changed := s.notifyChan()
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	var rescan <-chan time.Time // with no watcher, the inbox is read on a clock
	if events == nil {
		t := time.NewTicker(rescanEvery)
		defer t.Stop()
		rescan = t.C
	}
	for {
		select {
		case <-changed:
			s.broadcast()
		case ev, ok := <-events:
			if !ok {
				return
			}
			if s.relevant(ev) {
				timer.Reset(settle)
			}
		case <-errs: // events may have been lost: read again
			timer.Reset(settle)
		case <-rescan:
			s.Refresh()
		case <-timer.C:
			s.Refresh()
		case <-ctx.Done():
			return
		}
	}
}

// relevant skips dot files: they are temp files, and the rename that publishes
// one is its own event.
func (s *Service) relevant(ev fsnotify.Event) bool {
	base := filepath.Base(ev.Name)
	if ev.Op == fsnotify.Chmod || (base[0] == '.' && filepath.Dir(ev.Name) != s.Home) {
		return false
	}
	return filepath.Dir(ev.Name) != s.Home || base == "inbox"
}

// syncWatches renews the watch of a directory that was replaced.
func (s *Service) syncWatches() {
	if s.watcher == nil {
		return
	}
	want := map[string]os.FileInfo{}
	add := func(path string, follow bool) {
		stat := os.Lstat
		if follow {
			stat = os.Stat
		}
		if fi, err := stat(path); err == nil && fi.IsDir() {
			want[path] = fi
		}
	}
	add(s.Home, true)
	inbox := filepath.Join(s.Home, "inbox")
	add(inbox, false)
	if _, ok := want[inbox]; ok {
		names, _ := os.ReadDir(inbox)
		for _, e := range names {
			if validSession(e.Name()) {
				add(filepath.Join(inbox, e.Name()), false)
			}
		}
	}
	for path, fi := range s.watched {
		if now, ok := want[path]; !ok || !os.SameFile(fi, now) {
			_ = s.watcher.Remove(path)
			delete(s.watched, path)
		}
	}
	for path, fi := range want {
		if _, ok := s.watched[path]; !ok && s.watcher.Add(path) == nil {
			s.watched[path] = fi
		}
	}
}
