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
	"github.com/jbuffin/admiral/internal/launcher"
)

// Chat questions (ADR 0020): AdmiralBot asks with `admiral chat ask`, which writes
// $ADMIRAL_HOME/chat/questions/<id>.json and waits for <id>.answer.json. They are
// read the way ADR 0016 reads a ship's inbox directory, and never reach the inbox.

// Session is what a chat question carries as its `session`, so its key can't
// collide with a ship's.
const Session = launcher.ChatSession

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

// fileKey says whether a cached read is still of the file as it is.
type fileKey struct {
	mod  time.Time
	size int64
}

// cachedQuestion is what one question file read as: no item for a file that is
// not a valid question, and so never read again until it changes.
type cachedQuestion struct {
	key  fileKey
	item *inbox.Item
}

// cachedAnswer is what one answer file read as; nil answer is no answer.
type cachedAnswer struct {
	key    fileKey
	exists bool
	answer json.RawMessage
}

// scanQuestions reads the questions directory: the newest valid question items,
// each with its answer if one is there, oldest first. A file that fails is skipped
// and never hides another. Ids start with a time, so the names are read newest
// first until maxQuestions valid ones are found, and a file whose modification time
// and size haven't changed is not read again. The caller holds cmu.
func (s *Service) scanQuestions() []Question {
	out := []Question{}
	dir, err := s.openQuestions()
	if err != nil {
		s.qItems, s.qAnswers = nil, nil
		return out
	}
	defer dir.Close()
	f, err := dir.Open(".")
	if err != nil {
		return out
	}
	names, _ := f.Readdirnames(-1)
	f.Close()
	var ids []string
	here := map[string]bool{}
	for _, n := range names {
		here[n] = true
		if id, ok := strings.CutSuffix(n, ".json"); ok && inbox.ValidID(id) {
			ids = append(ids, id)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(ids)))
	if s.qItems == nil {
		s.qItems, s.qAnswers = map[string]cachedQuestion{}, map[string]cachedAnswer{}
	}
	for n := range s.qItems {
		if !here[n+".json"] {
			delete(s.qItems, n)
		}
	}
	for n := range s.qAnswers {
		if !here[inbox.AnswerName(n)] {
			delete(s.qAnswers, n)
		}
	}
	for _, id := range ids {
		if len(out) == maxQuestions {
			break
		}
		it := s.questionItem(dir, id)
		if it == nil {
			continue
		}
		out = append(out, Question{Item: *it, Answer: s.answerOf(dir, id)})
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].Item, out[j].Item
		if !a.Created.Equal(b.Created) {
			return a.Created.Before(b.Created)
		}
		return a.ID < b.ID
	})
	return out
}

// questionItem is the valid question item in dir/<id>.json, or nil.
func (s *Service) questionItem(dir *os.Root, id string) *inbox.Item {
	fi, err := dir.Lstat(id + ".json")
	if err != nil {
		return nil
	}
	key := fileKey{fi.ModTime(), fi.Size()}
	if c, ok := s.qItems[id]; ok && c.key == key {
		return c.item
	}
	c := cachedQuestion{key: key}
	if it, err := inbox.ReadItem(dir, Session, id); err == nil && it.Kind == inbox.KindQuestion {
		c.item = &it
	}
	s.qItems[id] = c
	return c.item
}

// answerOf is the answer to question id as served, or nil.
func (s *Service) answerOf(dir *os.Root, id string) json.RawMessage {
	fi, err := dir.Lstat(inbox.AnswerName(id))
	if err != nil {
		delete(s.qAnswers, id)
		return nil
	}
	key := fileKey{fi.ModTime(), fi.Size()}
	if c, ok := s.qAnswers[id]; ok && c.key == key {
		return c.answer
	}
	c := cachedAnswer{key: key, exists: true, answer: readAnswer(dir, id)}
	s.qAnswers[id] = c
	return c.answer
}

// answerFile is the part of an answer file that reaches a browser: the envelope and
// each question's choice and text, nothing else a writer put there.
type answerFile struct {
	Version  int                             `json:"version"`
	ID       string                          `json:"id"`
	Kind     string                          `json:"kind"`
	Answered string                          `json:"answered,omitempty"`
	Answers  map[string]inbox.QuestionAnswer `json:"answers"`
	Note     string                          `json:"note,omitempty"`
}

// readAnswer is the answer file as served, or nil when there is none that can be
// trusted: a link, a file that is not regular, one over 1 MiB, not an answer, or
// one whose choices are not a label or a list of labels. Only the fields of
// answerFile are kept.
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
	var a answerFile
	if json.Unmarshal(b, &a) != nil || a.Answers == nil {
		return nil
	}
	for _, q := range a.Answers {
		var one string
		var many []string
		c := strings.TrimSpace(string(q.Choice))
		if c != "" && c != "null" && json.Unmarshal(q.Choice, &one) != nil && json.Unmarshal(q.Choice, &many) != nil {
			return nil
		}
	}
	out, err := json.Marshal(a)
	if err != nil {
		return nil
	}
	return out
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

// WithdrawQuestions removes every file in chat/questions, answered or not: New
// chat, and a fresh session after the old one ended. A waiting `admiral chat ask`
// sees its question gone and exits 4.
func (s *Service) WithdrawQuestions() {
	s.qmu.Lock()
	defer s.qmu.Unlock()
	// A second pass after the first: a question renamed in while the listing was
	// being removed is withdrawn too.
	s.removeAll()
	s.removeAll()
	s.cmu.Lock()
	s.qs, s.qAt = nil, time.Time{}
	s.cmu.Unlock()
}

// removeAll removes what chat/questions holds now.
func (s *Service) removeAll() {
	dir, err := s.openQuestions()
	if err != nil {
		return
	}
	defer dir.Close()
	f, err := dir.Open(".")
	if err != nil {
		return
	}
	names, _ := f.Readdirnames(-1)
	f.Close()
	for _, n := range names {
		_ = dir.Remove(n)
	}
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
