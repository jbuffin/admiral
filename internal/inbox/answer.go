package inbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math/rand/v2"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jbuffin/admiral/internal/outcomes"
)

// The errors Answer returns. An *InvalidError is a bad answer.
var (
	ErrNotFound = errors.New("no such inbox item")
	ErrAnswered = errors.New("inbox item is already answered")
)

// InvalidError is an answer the item cannot take.
type InvalidError struct{ Msg string }

func (e *InvalidError) Error() string { return e.Msg }

func invalid(format string, args ...any) error { return &InvalidError{fmt.Sprintf(format, args...)} }

// open reads item id of session when it is still unanswered: ErrNotFound for an
// item that is not there, ErrAnswered for one with an answer.
func (s *Service) open(session, id string) (Item, error) {
	if !idPattern.MatchString(id) {
		return Item{}, ErrNotFound
	}
	dir, err := openShipDir(s.Home, session)
	if err != nil {
		return Item{}, ErrNotFound
	}
	defer dir.Close()
	it, err := readItem(dir, session, id)
	if err != nil {
		return Item{}, ErrNotFound
	}
	if Answered(dir, id) {
		return Item{}, ErrAnswered
	}
	return it, nil
}

// Answer writes the answer to item id of session and records what build says.
// build gets the item, which has the given kind, and returns the answer file's
// content and the outcome records (their ship is set here), or an *InvalidError.
// It does no network I/O: anything slow runs before Answer, outside the lock.
// One answer at a time: under the lock the item is read again and the answer
// file checked, so two answers to one item write one file. The file goes through
// a temp file and a link, next to the item, and the item is never touched. A ship
// directory that is gone is ErrNotFound. When the answer is the last one of a
// ship whose inbox is kept, the ship's files go.
func (s *Service) Answer(session, id, kind string, build func(Item) (file any, recs []outcomes.Record, err error)) error {
	if err := s.writeAnswered(session, id, kind, build); err != nil {
		return err
	}
	// Outside answerMu: adding the ship's resume order calls the source. The text is
	// in the state already, so the files may go whether or not the add worked; the
	// loop tries the rest.
	s.addResumes(session)
	s.answerMu.Lock()
	s.release(session)
	s.answerMu.Unlock()
	s.Refresh()
	return nil
}

// writeAnswered is Answer up to the answer file, under answerMu.
func (s *Service) writeAnswered(session, id, kind string, build func(Item) (file any, recs []outcomes.Record, err error)) error {
	if !validSession(session) || !idPattern.MatchString(id) {
		return ErrNotFound
	}
	s.answerMu.Lock()
	defer s.answerMu.Unlock()
	dir, err := openShipDir(s.Home, session)
	if err != nil {
		return ErrNotFound
	}
	defer dir.Close()
	var recs []outcomes.Record
	err = WriteAnswerFile(dir, session, id, kind, func(it Item) (any, error) {
		file, r, err := build(it)
		recs = r
		return file, err
	})
	if err != nil {
		return err
	}
	if s.Outcomes != nil {
		for _, r := range recs {
			r.Ship = session
			s.Outcomes.Enqueue(r, nil)
		}
	}
	s.CaptureResume(session) // before anything deletes the report
	return nil
}

// WriteAnswerFile writes the answer to item id in dir, whose items are the
// session's: it reads the item again, checks that it has the kind and no answer,
// asks build for the answer file's content and publishes it through writeAnswer.
// ErrNotFound is an item that is not there (or a directory that vanished
// meanwhile), ErrAnswered one with an answer already, and an *InvalidError a
// wrong kind or an answer build refused. The caller holds the lock that makes
// this one answer at a time.
func WriteAnswerFile(dir *os.Root, session, id, kind string, build func(Item) (any, error)) error {
	if !idPattern.MatchString(id) {
		return ErrNotFound
	}
	it, err := readItem(dir, session, id)
	if err != nil {
		return ErrNotFound
	}
	if Answered(dir, id) {
		return ErrAnswered
	}
	if it.Kind != kind {
		return invalid("item %s is a %s, not a %s", id, it.Kind, kind)
	}
	file, err := build(it)
	if err != nil {
		return err
	}
	if err := writeAnswer(dir, id, file); err != nil {
		switch {
		case errors.Is(err, fs.ErrExist):
			return ErrAnswered
		case errors.Is(err, fs.ErrNotExist):
			return ErrNotFound
		}
		return err
	}
	return nil
}

// writeAnswer writes v as dir/<id>.answer.json through a temp file and a link.
// The temp name starts with a dot and does not end in .json, so a reader never
// takes it for an item or an answer. A link fails when the answer exists, so an
// answer written by a ship, or by another Admiral, is never replaced.
func writeAnswer(dir *os.Root, id string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	tmp := "." + answerName(id) + ".tmp-" + strconv.FormatUint(rand.Uint64(), 36)
	f, err := dir.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = dir.Link(tmp, answerName(id))
	}
	_ = dir.Remove(tmp)
	return err
}

// QuestionAnswer is the engineer's answer to one question: a choice (an option
// label, or a list of them on a multi question) and/or text of their own.
type QuestionAnswer struct {
	Choice json.RawMessage `json:"choice,omitempty"`
	Other  string          `json:"other,omitempty"`
}

// QuestionRequest is the body of the answer route for a question item.
type QuestionRequest struct {
	Answers map[string]QuestionAnswer `json:"answers"`
	Note    string                    `json:"note,omitempty"`
}

type question struct {
	ID      string `json:"id"`
	Multi   bool   `json:"multi"`
	Options []struct {
		Label string `json:"label"`
	} `json:"options"`
}

// questionsOf is the questions of a question item.
func questionsOf(it Item) ([]question, error) {
	var q struct {
		Questions []question `json:"questions"`
	}
	if json.Unmarshal(it.Field(KindQuestion), &q) != nil || len(q.Questions) == 0 {
		return nil, errSkip
	}
	seen := map[string]bool{}
	for _, x := range q.Questions {
		if x.ID == "" || seen[x.ID] {
			return nil, errSkip
		}
		seen[x.ID] = true
	}
	return q.Questions, nil
}

// AnswerQuestion answers a question item. Every question needs an answer, and a
// choice must be one of the question's option labels (a list only on a multi
// question); text of the engineer's own can stand alone.
func (s *Service) AnswerQuestion(session, id string, req QuestionRequest) error {
	return s.Answer(session, id, KindQuestion, func(it Item) (any, []outcomes.Record, error) {
		file, err := BuildQuestionAnswer(it, req)
		if err != nil {
			return nil, nil, err
		}
		return file, []outcomes.Record{{Event: "answered", Fields: map[string]any{"item": id, "kind": KindQuestion}}}, nil
	})
}

// BuildQuestionAnswer is the answer file for a question item and the engineer's
// request, or an *InvalidError: every question needs an answer, a choice is one of
// the question's option labels (a list only on a multi question), and text of the
// engineer's own can stand alone. It writes nothing.
func BuildQuestionAnswer(it Item, req QuestionRequest) (any, error) {
	qs, err := questionsOf(it)
	if err != nil {
		return nil, invalid("item %s has no questions", it.ID)
	}
	answers := map[string]QuestionAnswer{}
	for _, q := range qs {
		a, ok := req.Answers[q.ID]
		if !ok {
			return nil, invalid("question %s has no answer", q.ID)
		}
		a, err := checkChoice(q, a)
		if err != nil {
			return nil, err
		}
		answers[q.ID] = a
	}
	for qid := range req.Answers {
		if _, ok := answers[qid]; !ok {
			return nil, invalid("the item has no question %q", qid)
		}
	}
	return struct {
		Version  int                       `json:"version"`
		ID       string                    `json:"id"`
		Kind     string                    `json:"kind"`
		Answered string                    `json:"answered"`
		Answers  map[string]QuestionAnswer `json:"answers"`
		Note     string                    `json:"note,omitempty"`
	}{1, it.ID, KindQuestion, time.Now().UTC().Format(time.RFC3339), answers, req.Note}, nil
}

// checkChoice validates one question's answer and returns it normalised.
func checkChoice(q question, a QuestionAnswer) (QuestionAnswer, error) {
	label := map[string]bool{}
	for _, o := range q.Options {
		label[o.Label] = true
	}
	other := strings.TrimSpace(a.Other)
	var one string
	var many []string
	switch c := strings.TrimSpace(string(a.Choice)); {
	case c == "" || c == "null":
		a.Choice = nil
	case json.Unmarshal(a.Choice, &one) == nil:
		if !label[one] {
			return a, invalid("question %s: %q is not one of its options", q.ID, one)
		}
	case json.Unmarshal(a.Choice, &many) == nil:
		if !q.Multi && len(many) > 0 {
			return a, invalid("question %s takes one choice, not a list", q.ID)
		}
		for _, l := range many {
			if !label[l] {
				return a, invalid("question %s: %q is not one of its options", q.ID, l)
			}
		}
		if len(many) == 0 {
			a.Choice = nil
		}
	default:
		return a, invalid("question %s: choice must be a label or a list of labels", q.ID)
	}
	if a.Choice == nil && other == "" {
		return a, invalid("question %s has no answer", q.ID)
	}
	a.Other = other
	return a, nil
}
