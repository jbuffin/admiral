package inbox

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/jbuffin/admiral/internal/sessionhost"
)

// QuestionError is a question that was refused, with the reason for its asker.
type QuestionError struct{ Msg string }

func (e *QuestionError) Error() string { return e.Msg }

func badQuestion(format string, a ...any) error { return &QuestionError{fmt.Sprintf(format, a...)} }

// newQuestionID follows ADR 0016's id rule.
func newQuestionID() (string, error) {
	var r [4]byte
	if _, err := rand.Read(r[:]); err != nil {
		return "", err
	}
	return time.Now().UTC().Format("20060102T150405") + "-" + hex.EncodeToString(r[:]), nil
}

// questionsRoot refuses a link anywhere on the path.
func questionsRoot(root *os.Root, fleet string) (*os.Root, error) {
	dir := ""
	for _, part := range []string{"chat", fleet, "questions"} {
		dir = filepath.Join(dir, part)
		fi, err := root.Lstat(dir)
		switch {
		case os.IsNotExist(err):
			if err := root.Mkdir(dir, 0o700); err != nil {
				return nil, err
			}
		case err != nil:
			return nil, err
		case !fi.IsDir():
			return nil, fmt.Errorf("%s is not a directory", dir)
		}
	}
	return root.OpenRoot(dir)
}

// WriteQuestion writes the question in body ({title, questions}) for a fleet's chat and returns its id; it writes nothing for a bad question, which comes back as a *QuestionError.
func WriteQuestion(root *os.Root, fleet string, body []byte) (string, error) {
	if len(body) > MaxItem {
		return "", badQuestion("the question is over %d bytes", MaxItem)
	}
	var in struct {
		Title     string          `json:"title"`
		Questions json.RawMessage `json:"questions"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		return "", badQuestion("the question is not valid JSON: %v", err)
	}
	id, err := newQuestionID()
	if err != nil {
		return "", err
	}
	item, err := json.Marshal(map[string]any{"version": 1, "id": id, "kind": KindQuestion,
		"created": time.Now().UTC().Format(time.RFC3339), "title": in.Title,
		"question": map[string]json.RawMessage{"questions": in.Questions}})
	if err != nil {
		return "", badQuestion("the question is not valid: %v", err)
	}
	if in.Questions == nil {
		return "", badQuestion("the question has no questions")
	}
	if _, err := ParseItem(item, sessionhost.ChatSession(fleet), id); err != nil {
		return "", badQuestion("the question is not valid: it needs a title and questions of {id, text, multi, options: [{label}]}, each id once")
	}
	dir, err := questionsRoot(root, fleet)
	if err != nil {
		return "", err
	}
	defer dir.Close()
	tmp := "." + id + ".json.tmp"
	f, err := dir.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	_, err = f.Write(append(item, '\n'))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = dir.Rename(tmp, id+".json")
	}
	if err != nil {
		_ = dir.Remove(tmp)
		return "", err
	}
	return id, nil
}

// QuestionExists is false once the question's file is gone: New chat withdrew it.
func QuestionExists(root *os.Root, fleet, id string) bool {
	_, err := root.Lstat(filepath.Join("chat", fleet, "questions", id+".json"))
	return err == nil
}

// ReadQuestionAnswer trusts only a regular file, not a link, at most 1 MiB, holding a JSON object.
func ReadQuestionAnswer(root *os.Root, fleet, id string) ([]byte, bool) {
	name := filepath.Join("chat", fleet, "questions", AnswerName(id))
	fi, err := root.Lstat(name)
	if err != nil || !fi.Mode().IsRegular() {
		return nil, false
	}
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		return nil, false
	}
	b, err := io.ReadAll(io.LimitReader(f, MaxItem+1))
	var obj map[string]json.RawMessage
	if err != nil || len(b) > MaxItem || json.Unmarshal(b, &obj) != nil || obj == nil {
		return nil, false
	}
	return b, true
}
