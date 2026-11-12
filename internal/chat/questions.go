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
	"github.com/jbuffin/admiral/internal/sessionhost"
)

// Chat questions are read the way a ship's inbox directory is, and the inbox lists the waiting ones.

const maxQuestions = 20

const questionEvery = 100 * time.Millisecond

type Question struct {
	Item   inbox.Item
	Answer json.RawMessage
}

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

// None of the directories may be a link.
func (s *fleetChat) openQuestions() (*os.Root, error) {
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
	dir, err := subDir(chat, s.key)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	return subDir(dir, "questions")
}

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

type fileKey struct {
	mod  time.Time
	size int64
}

// An invalid question file has no item, and is not read again until it changes.
type cachedQuestion struct {
	key  fileKey
	item *inbox.Item
}

type cachedAnswer struct {
	key    fileKey
	exists bool
	answer json.RawMessage
}

// Ids start with a time, so names are read newest first until maxQuestions valid ones are
// found. A file that fails is skipped and never hides another. The caller holds cmu.
func (s *fleetChat) scanQuestions() []Question {
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

func (s *fleetChat) questionItem(dir *os.Root, id string) *inbox.Item {
	fi, err := dir.Lstat(id + ".json")
	if err != nil {
		return nil
	}
	key := fileKey{fi.ModTime(), fi.Size()}
	if c, ok := s.qItems[id]; ok && c.key == key {
		return c.item
	}
	c := cachedQuestion{key: key}
	if it, err := inbox.ReadItem(dir, s.name(), id); err == nil && it.Kind == inbox.KindQuestion {
		c.item = &it
	}
	s.qItems[id] = c
	return c.item
}

func (s *fleetChat) answerOf(dir *os.Root, id string) json.RawMessage {
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

// answerFile is all of an answer file that reaches a browser.
type answerFile struct {
	Version  int                             `json:"version"`
	ID       string                          `json:"id"`
	Kind     string                          `json:"kind"`
	Answered string                          `json:"answered,omitempty"`
	Answers  map[string]inbox.QuestionAnswer `json:"answers"`
	Note     string                          `json:"note,omitempty"`
}

// readAnswer refuses a link, a file that is not regular, one over 1 MiB, or choices that are
// not a label or a list of labels.
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

// The directory is read under cmu, not under mu.
func (s *fleetChat) questions(fresh bool) []Question {
	s.cmu.Lock()
	defer s.cmu.Unlock()
	if fresh || s.qs == nil || time.Since(s.qAt) >= questionEvery {
		s.qs, s.qAt = s.scanQuestions(), time.Now()
	}
	return append([]Question{}, s.qs...)
}

func unanswered(qs []Question) bool {
	for _, q := range qs {
		if len(q.Answer) == 0 {
			return true
		}
	}
	return false
}

// WithdrawQuestions removes one fleet's chat questions, as a new chat session for it starts.
func (s *Service) WithdrawQuestions(key string) { s.chat(key).withdraw() }

// A waiting `admiral chat ask` sees its question gone and exits 4.
func (s *fleetChat) withdraw() {
	s.qmu.Lock()
	defer s.qmu.Unlock()
	// A second pass after the first: a question renamed in while the listing was
	// being removed is withdrawn too.
	s.removeAll()
	s.removeAll()
	s.cmu.Lock()
	s.qs, s.qAt = nil, time.Time{}
	s.cmu.Unlock()
	s.questionsChanged()
}

func (s *fleetChat) removeAll() {
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

// WaitingQuestions are the unanswered questions of every fleet whose chat is on, as inbox items.
func (s *Service) WaitingQuestions() []inbox.Item {
	cfg := s.Config()
	var out []inbox.Item
	for _, fc := range s.onFleets() {
		f, _ := cfg.Fleet(fc.key)
		for _, q := range fc.questions(false) {
			if len(q.Answer) > 0 {
				continue
			}
			it := q.Item
			it.Name, it.Fleet, it.FleetName, it.FleetColor = "AdmiralBot", fc.key, f.Name, f.Color
			out = append(out, it)
		}
	}
	return out
}

// AnswerQuestion is the one way a chat question is answered, from the chat or from the Inbox. A
// session that is not a configured fleet's chat has no such question.
func (s *Service) AnswerQuestion(session, id string, req inbox.QuestionRequest) error {
	key, ok := sessionhost.ChatFleetOfSession(session)
	if _, known := s.Config().Fleet(key); !ok || !known {
		return inbox.ErrNotFound
	}
	return s.chat(key).answer(id, req)
}

// answer writes no outcome record and queues no resume text: the chat is not a ship.
func (s *fleetChat) answer(id string, req inbox.QuestionRequest) error {
	s.qmu.Lock()
	err := func() error {
		dir, err := s.openQuestions()
		if err != nil {
			return inbox.ErrNotFound
		}
		defer dir.Close()
		return inbox.WriteAnswerFile(dir, s.name(), id, inbox.KindQuestion, func(it inbox.Item) (any, error) {
			return inbox.BuildQuestionAnswer(it, req)
		})
	}()
	s.qmu.Unlock()
	if err == nil {
		s.questions(true)
		s.broadcast()
	}
	return err
}

func (s *fleetChat) serveAnswer(w http.ResponseWriter, r *http.Request) {
	var req inbox.QuestionRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "request is not valid JSON: "+err.Error())
		return
	}
	err := s.answer(r.PathValue("id"), req)
	var bad *inbox.InvalidError
	switch {
	case err == nil:
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
