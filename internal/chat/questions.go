package chat

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/jbuffin/admiral/internal/inbox"
)

// Chat questions (ADR 0020): AdmiralBot asks with `admiral chat ask`, which writes
// $ADMIRAL_HOME/chat/questions/<id>.json and waits for <id>.answer.json. They are
// read the way ADR 0016 reads a ship's inbox directory, and never reach the inbox.

// Session is what a chat question carries as its `session`, so its key can't
// collide with a ship's.
const Session = "_chat"

// maxQuestions is how many chat questions are served: the newest.
const maxQuestions = 20

// questionEvery is how often the questions directory is read at most.
const questionEvery = 100 * time.Millisecond

// Question is a chat question as served: the inbox's question item, with the
// answer file's content or null.
type Question struct {
	Item   inbox.Item
	Answer json.RawMessage
}

// MarshalJSON is the inbox's item with `session` set to "_chat" and `answer` added.
func (q Question) MarshalJSON() ([]byte, error) {
	b, err := q.Item.MarshalJSON()
	if err != nil {
		return nil, err
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	m["answer"] = json.RawMessage("null")
	if len(q.Answer) > 0 {
		m["answer"] = q.Answer
	}
	return json.Marshal(m)
}

// openQuestions opens chat/questions under the home through a root. Neither
// directory may be a link.
func (s *Service) openQuestions() (*os.Root, error) {
	root, err := os.OpenRoot(s.Home)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	chat, err := subDir(root, "chat")
	if err != nil {
		return nil, err
	}
	defer chat.Close()
	return subDir(chat, "questions")
}

// subDir opens name under root when it is a directory itself and not a link.
func subDir(root *os.Root, name string) (*os.Root, error) {
	fi, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", name)
	}
	return root.OpenRoot(name)
}

// scanQuestions reads the questions directory: every valid question item, with its
// answer if one is there. A file that fails is skipped and never hides another.
func (s *Service) scanQuestions() []Question {
	out := []Question{}
	dir, err := s.openQuestions()
	if err != nil {
		return out
	}
	defer dir.Close()
	f, err := dir.Open(".")
	if err != nil {
		return out
	}
	names, _ := f.Readdirnames(-1)
	f.Close()
	for _, n := range names {
		id, ok := strings.CutSuffix(n, ".json")
		if !ok || !inbox.ValidID(id) {
			continue
		}
		it, err := inbox.ReadItem(dir, Session, id)
		if err != nil || it.Kind != inbox.KindQuestion {
			continue
		}
		out = append(out, Question{Item: it, Answer: readAnswer(dir, id)})
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].Item, out[j].Item
		if !a.Created.Equal(b.Created) {
			return a.Created.Before(b.Created)
		}
		return a.ID < b.ID
	})
	if len(out) > maxQuestions {
		out = out[len(out)-maxQuestions:]
	}
	return out
}

// readAnswer is the answer file's content, or nil when there is none that can be
// trusted: a link, a file that is not regular, one over 1 MiB, or not a JSON object.
func readAnswer(dir *os.Root, id string) json.RawMessage {
	name := inbox.AnswerName(id)
	fi, err := dir.Lstat(name)
	if err != nil || !fi.Mode().IsRegular() {
		return nil
	}
	f, err := dir.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		return nil
	}
	b, err := io.ReadAll(io.LimitReader(f, inbox.MaxItem+1))
	if err != nil || len(b) > inbox.MaxItem {
		return nil
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(b, &obj) != nil || obj == nil {
		return nil
	}
	return json.RawMessage(strings.TrimSpace(string(b)))
}

// questions is the served questions, read again when the last read is older than
// questionEvery or fresh says so. The directory is read under cmu, not under mu.
func (s *Service) questions(fresh bool) []Question {
	s.cmu.Lock()
	defer s.cmu.Unlock()
	if fresh || s.qs == nil || time.Since(s.qAt) >= questionEvery {
		s.qs, s.qAt = s.scanQuestions(), time.Now()
	}
	return append([]Question{}, s.qs...)
}

// unanswered says whether any question is still waiting for its answer.
func unanswered(qs []Question) bool {
	for _, q := range qs {
		if len(q.Answer) == 0 {
			return true
		}
	}
	return false
}

// withdrawQuestions removes every file in chat/questions, answered or not: New
// chat, and a fresh session after the old one ended. A waiting `admiral chat ask`
// sees its question gone and exits 4.
func (s *Service) withdrawQuestions() {
	s.qmu.Lock()
	defer s.qmu.Unlock()
	if dir, err := s.openQuestions(); err == nil {
		if f, err := dir.Open("."); err == nil {
			names, _ := f.Readdirnames(-1)
			f.Close()
			for _, n := range names {
				_ = dir.Remove(n)
			}
		}
		dir.Close()
	}
	s.cmu.Lock()
	s.qs, s.qAt = nil, time.Time{}
	s.cmu.Unlock()
}

// serveAnswer writes the engineer's answer to a chat question. It writes no
// outcome record and adds no resume order: the chat is not a ship.
func (s *Service) serveAnswer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req inbox.QuestionRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "request is not valid JSON: "+err.Error())
		return
	}
	s.qmu.Lock()
	err := func() error {
		dir, err := s.openQuestions()
		if err != nil {
			return inbox.ErrNotFound
		}
		defer dir.Close()
		return inbox.WriteAnswerFile(dir, Session, id, inbox.KindQuestion, func(it inbox.Item) (any, error) {
			return inbox.BuildQuestionAnswer(it, req)
		})
	}()
	s.qmu.Unlock()
	var bad *inbox.InvalidError
	switch {
	case err == nil:
		s.questions(true)
		s.broadcast()
		writeJSON(w, http.StatusOK, map[string]bool{"answered": true})
	case errors.Is(err, inbox.ErrNotFound):
		writeError(w, http.StatusNotFound, "no such chat question")
	case errors.Is(err, inbox.ErrAnswered):
		writeError(w, http.StatusConflict, "the chat question is already answered")
	case errors.As(err, &bad):
		writeError(w, http.StatusBadRequest, bad.Msg)
	default:
		writeError(w, http.StatusInternalServerError, "could not write the answer: "+err.Error())
	}
}
