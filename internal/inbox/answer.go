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

var (
	ErrNotFound = errors.New("no such inbox item")
	ErrAnswered = errors.New("inbox item is already answered")
)

type InvalidError struct{ Msg string }

func (e *InvalidError) Error() string { return e.Msg }

func invalid(format string, args ...any) error { return &InvalidError{fmt.Sprintf(format, args...)} }

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

// Answer's build runs under the lock, so it does no network I/O: anything slow
// runs before Answer.
func (s *Service) Answer(session, id, kind string, build func(Item) (file any, recs []outcomes.Record, err error)) error {
	if err := s.writeAnswered(session, id, kind, build); err != nil {
		return err
	}
	// Outside answerMu: typing the resume text calls the ship's pane. Its text is in
	// the state already, so the files may go whether or not it worked.
	s.addResumes(session)
	s.answerMu.Lock()
	s.release(session)
	s.answerMu.Unlock()
	s.Refresh()
	return nil
}

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

// WriteAnswerFile's caller holds the lock that makes this one answer at a time.
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

// writeAnswer links rather than renames: a link fails when the answer exists, so
// one written by a ship or another Admiral is never replaced. The dot temp name
// is never taken for an item.
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

// QuestionAnswer's Choice is an option label, or a list of them on a multi question.
type QuestionAnswer struct {
	Choice json.RawMessage `json:"choice,omitempty"`
	Other  string          `json:"other,omitempty"`
}

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

func (s *Service) AnswerQuestion(session, id string, req QuestionRequest) error {
	return s.Answer(session, id, KindQuestion, func(it Item) (any, []outcomes.Record, error) {
		file, err := BuildQuestionAnswer(it, req)
		if err != nil {
			return nil, nil, err
		}
		return file, []outcomes.Record{{Event: "answered", Fields: map[string]any{"item": id, "kind": KindQuestion}}}, nil
	})
}

// BuildQuestionAnswer writes nothing. Every question needs an answer; the
// engineer's own text can stand alone without a choice.
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
