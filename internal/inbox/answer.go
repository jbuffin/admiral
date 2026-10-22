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

// Answer writes the answer to item id of session and records `answered`. build
// gets the item, which has the given kind, and returns the answer file's content
// or an *InvalidError. One answer at a time: under the lock the item is read
// again and the answer file checked, so two answers to one item write one file.
// The file goes through a temp file and a rename, next to the item, and the item
// is never touched. A ship directory that is gone is ErrNotFound.
func (s *Service) Answer(session, id, kind string, build func(Item) (any, error)) error {
	if !validSession(session) || !idPattern.MatchString(id) {
		return ErrNotFound
	}
	s.answerMu.Lock()
	defer s.answerMu.Unlock()
	root, err := os.OpenRoot(s.Home)
	if err != nil {
		return ErrNotFound
	}
	defer root.Close()
	inbox, err := realDir(root, "inbox")
	if err != nil {
		return ErrNotFound
	}
	defer inbox.Close()
	dir, err := realDir(inbox, session)
	if err != nil {
		return ErrNotFound
	}
	defer dir.Close()
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
	answer, err := build(it)
	if err != nil {
		return err
	}
	if err := writeAnswer(dir, id, answer); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return ErrNotFound
		}
		return err
	}
	if s.Outcomes != nil {
		s.Outcomes.Enqueue(outcomes.Record{Ship: session, Event: "answered", Fields: map[string]any{"item": id, "kind": kind}}, nil)
	}
	s.Refresh()
	return nil
}

// writeAnswer writes v as dir/<id>.answer.json through a temp file and a rename.
// The temp name starts with a dot and does not end in .json, so a reader never
// takes it for an item or an answer.
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
		err = dir.Rename(tmp, answerName(id))
	}
	if err != nil {
		_ = dir.Remove(tmp)
	}
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
	return s.Answer(session, id, KindQuestion, func(it Item) (any, error) {
		qs, err := questionsOf(it)
		if err != nil {
			return nil, invalid("item %s has no questions", id)
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
		file := struct {
			Version  int                       `json:"version"`
			ID       string                    `json:"id"`
			Kind     string                    `json:"kind"`
			Answered string                    `json:"answered"`
			Answers  map[string]QuestionAnswer `json:"answers"`
			Note     string                    `json:"note,omitempty"`
		}{1, id, KindQuestion, time.Now().UTC().Format(time.RFC3339), answers, req.Note}
		return file, nil
	})
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
