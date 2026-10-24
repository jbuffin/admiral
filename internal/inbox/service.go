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
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/outcomes"
	"github.com/jbuffin/admiral/internal/server"
	"github.com/jbuffin/admiral/internal/state"
)

// settle lets a ship finish writing before the inbox is read again.
const settle = 100 * time.Millisecond

// rescanEvery is how often the inbox is read when it cannot be watched.
const rescanEvery = 30 * time.Second

// Service reads and serves the inbox of $ADMIRAL_HOME.
type Service struct {
	// Home is $ADMIRAL_HOME, absolute.
	Home string
	Hub  *server.Hub
	// Ship says whether a session is a ship Admiral knows, and its name and harness.
	Ship func(session string) (name, harness string, ok bool)
	// Outcomes is the outcome log; nil keeps none.
	Outcomes *outcomes.Writer
	// Dock is the environment of the dock a ship was launched from, for gh. With no
	// env, why says which of the ship and its dock is unknown.
	Dock func(session string) (env []string, why string)
	// Kept holds the records of sailed-off ships whose inbox is kept; nil keeps none.
	Kept *state.Store
	// ResumeTarget is autopilot.resume_source; nil when the config names none.
	ResumeTarget func() *config.ResumeSource
	// AddOrder adds an order to a panel of a source. A failure says through
	// AddKind() AddKind whether it may pass or the order may have been added; any
	// other failure is final.
	AddOrder func(ctx context.Context, source, panel, text string) (id string, err error)
	// Origin is where a ship ran, live or kept; ok is false for a ship Admiral
	// does not know.
	Origin func(session string) (Origin, bool)
	// ResumeAdd is called before each add to the panel, and its result after, with
	// the id the add gave ("" when it gave none). Autopilot holds off on the
	// panel's unrecorded orders in between, and remembers where the order
	// launches (#120). Nil does nothing.
	ResumeAdd func(source, panel string, from Origin) (done func(orderID string))

	answerMu sync.Mutex        // one answer at a time
	actMu    sync.Mutex        // guards acting
	acting   map[string]bool   // the items a gh action is running on
	resumeMu sync.Mutex        // one pass over the resume orders at a time
	lastErr  map[string]string // the last failure logged for a resume order; under resumeMu
	noTarget sync.Once
	resume   chan struct{} // resume orders may be ready; buffered; under mu
	scanMu   sync.Mutex    // one refresh at a time
	mu       sync.Mutex    // guards items
	items    []Item
	notify   chan struct{} // a ship changed; buffered, so Notify never blocks
	watched  map[string]os.FileInfo
	watcher  *fsnotify.Watcher
}

// List is the open items, oldest first, with the name and harness of the ships
// that are known.
func (s *Service) List() []Item {
	s.mu.Lock()
	items := append([]Item{}, s.items...)
	s.mu.Unlock()
	if s.Ship != nil {
		for i := range items {
			if name, harness, ok := s.Ship(items[i].Session); ok {
				items[i].Name, items[i].Harness = name, harness
			}
		}
	}
	return items
}

func (s *Service) message() any { return map[string]any{"type": "inbox", "items": s.List()} }

// Notify asks Run to push the items again, for a change in the ships that name
// them. It never blocks.
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

func (s *Service) broadcast() { s.Hub.BroadcastOnce("inbox", s.message) }

// Connected sends a new client the items.
func (s *Service) Connected(c *server.Client) { _ = c.SendOnce("inbox", s.message) }

// Routes registers the inbox API.
func (s *Service) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/inbox", s.serveList)
	mux.HandleFunc("POST /api/inbox/{session}/{id}/answer", s.serveAnswer)
	mux.HandleFunc("POST /api/inbox/{session}/{id}/submit", s.serveSubmit)
	mux.HandleFunc("POST /api/inbox/{session}/{id}/discard", s.serveDiscard)
	mux.HandleFunc("POST /api/inbox/{session}/{id}/findings", s.serveFindings)
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
	writeJSON(w, http.StatusOK, map[string]any{"items": s.List()})
}

// decode reads the request body, at most max bytes, as a JSON object into v.
func decode(w http.ResponseWriter, r *http.Request, max int64, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, max)).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "the request is not a valid JSON object: "+err.Error())
		return false
	}
	return true
}

// serveAnswer takes the engineer's answer to a question item. A review is
// answered by submit or discard, and findings by findings.
func (s *Service) serveAnswer(w http.ResponseWriter, r *http.Request) {
	session, id := r.PathValue("session"), r.PathValue("id")
	it, err := s.open(session, id)
	if err == nil && it.Kind != KindQuestion {
		err = invalid("item %s is a %s: answer it with %s", id, it.Kind, map[string]string{KindReview: "submit or discard", KindFindings: "findings"}[it.Kind])
	}
	if err == nil {
		var req QuestionRequest
		if !decode(w, r, 256<<10, &req) {
			return
		}
		err = s.AnswerQuestion(session, id, req)
	}
	respond(w, session, id, ReviewResult{}, err)
}

// serveSubmit submits the pending review of a review item as the engineer.
func (s *Service) serveSubmit(w http.ResponseWriter, r *http.Request) {
	session, id := r.PathValue("session"), r.PathValue("id")
	var req SubmitRequest
	if !decode(w, r, 64<<10, &req) {
		return
	}
	res, err := s.SubmitReview(session, id, req)
	respond(w, session, id, res, err)
}

// serveDiscard deletes the pending review of a review item, or with
// {"forget": true} removes the item from the inbox without gh.
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

// serveFindings takes the engineer's decision on every card of a findings item.
func (s *Service) serveFindings(w http.ResponseWriter, r *http.Request) {
	session, id := r.PathValue("session"), r.PathValue("id")
	var req FindingsRequest
	if !decode(w, r, 1<<20, &req) {
		return
	}
	respond(w, session, id, ReviewResult{}, s.AnswerFindings(session, id, req))
}

// respond answers a request with the item answered, or with the error and the
// status it calls for. res is what a review did, for the UI to show.
func respond(w http.ResponseWriter, session, id string, res ReviewResult, err error) {
	var bad *InvalidError
	var gh *GhError
	switch {
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
		writeJSON(w, http.StatusOK, out)
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "no item "+id+" in the inbox of "+session)
	case errors.Is(err, ErrAnswered):
		writeError(w, http.StatusConflict, "item "+id+" is already answered")
	case errors.Is(err, ErrBusy):
		writeError(w, http.StatusConflict, err.Error())
	case errors.As(err, &bad):
		writeError(w, http.StatusBadRequest, bad.Msg)
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

// Refresh reads the inbox again and pushes it. The watches are brought up to
// date first, so a directory that came back cannot hide a file renamed into it.
func (s *Service) Refresh() {
	s.scanMu.Lock()
	s.syncWatches()
	items := scan(s.Home)
	s.mu.Lock()
	s.items = items // under scanMu, so an older scan cannot land after a newer one
	s.mu.Unlock()
	s.scanMu.Unlock()
	s.captureAll() // answers written by someone else count too
	s.broadcast()
}

// Run watches the inbox until ctx ends: $ADMIRAL_HOME (inbox/ may not exist
// yet), inbox/ and every ship's directory.
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

// relevant is whether an event can change what is served: not a chmod, not a
// dot file (a temp file; the rename that publishes it is its own event), and
// in $ADMIRAL_HOME only inbox itself.
func (s *Service) relevant(ev fsnotify.Event) bool {
	base := filepath.Base(ev.Name)
	if ev.Op == fsnotify.Chmod || (base[0] == '.' && filepath.Dir(ev.Name) != s.Home) {
		return false
	}
	return filepath.Dir(ev.Name) != s.Home || base == "inbox"
}

// syncWatches watches $ADMIRAL_HOME, inbox/ and each ship directory, drops the
// watch of one that is gone, and renews one whose directory was replaced.
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
