// Package inbox serves the item files ships write under $ADMIRAL_HOME/inbox/<session>/.
// Admiral writes answers beside them and never edits an item.
package inbox

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"syscall"
	"time"
)

// A ship writes the item file, so a larger one is skipped.
const maxItem = 1 << 20

// idPattern is what an item id must match (Commodore's ID_RE).
var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

const (
	KindQuestion = "question"
	KindReview   = "review"
	KindFindings = "findings"
	// KindNotice lives only in the AdmiralSession directory, and can only be dismissed.
	KindNotice = "notice"
)

type Item struct {
	fields  map[string]json.RawMessage
	Session string
	// Name and Harness are set only when the ship is known.
	Name, Harness string
	ID, Kind      string
	Created       time.Time
}

func (it Item) Field(name string) json.RawMessage { return it.fields[name] }

// MarshalJSON sets session, name and harness last, so a file cannot name another ship.
func (it Item) MarshalJSON() ([]byte, error) {
	out := make(map[string]json.RawMessage, len(it.fields)+3)
	for k, v := range it.fields {
		out[k] = v
	}
	out["session"], _ = json.Marshal(it.Session)
	delete(out, "name")
	delete(out, "harness")
	if it.Name != "" {
		out["name"], _ = json.Marshal(it.Name)
	}
	if it.Harness != "" {
		out["harness"], _ = json.Marshal(it.Harness)
	}
	return json.Marshal(out)
}

var errSkip = errors.New("not an item")

func answerName(id string) string { return id + ".answer.json" }

func validSession(name string) bool {
	return name != "" && !strings.HasPrefix(name, ".") && filepath.IsLocal(name) && !strings.ContainsRune(name, filepath.Separator)
}

func Answered(dir *os.Root, id string) bool {
	_, err := dir.Lstat(answerName(id))
	return err == nil
}

// readItem never follows a link and never waits on a pipe.
func readItem(dir *os.Root, session, id string) (Item, error) {
	name := id + ".json"
	fi, err := dir.Lstat(name)
	if err != nil {
		return Item{}, err
	}
	if !fi.Mode().IsRegular() {
		return Item{}, errSkip
	}
	f, err := dir.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return Item{}, errSkip
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		return Item{}, errSkip
	}
	b, err := io.ReadAll(io.LimitReader(f, maxItem+1))
	if err != nil || len(b) > maxItem {
		return Item{}, errSkip
	}
	return parseItem(b, session, id)
}

func parseItem(b []byte, session, id string) (Item, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(b, &fields) != nil || fields == nil {
		return Item{}, errSkip
	}
	it := Item{fields: fields, Session: session}
	var version int
	var created, title string
	if json.Unmarshal(fields["version"], &version) != nil || version != 1 ||
		json.Unmarshal(fields["id"], &it.ID) != nil || it.ID != id ||
		json.Unmarshal(fields["kind"], &it.Kind) != nil ||
		json.Unmarshal(fields["title"], &title) != nil || title == "" ||
		json.Unmarshal(fields["created"], &created) != nil {
		return Item{}, errSkip
	}
	var err error
	if it.Created, err = time.Parse(time.RFC3339, created); err != nil {
		return Item{}, errSkip
	}
	switch it.Kind {
	case KindQuestion:
		if _, err := questionsOf(it); err != nil {
			return Item{}, errSkip
		}
	case KindReview, KindFindings:
		if !isObject(fields[it.Kind]) {
			return Item{}, errSkip
		}
	case KindNotice: // only Admiral's own inbox holds notices
		if session != AdmiralSession || !isObject(fields[it.Kind]) {
			return Item{}, errSkip
		}
	default:
		return Item{}, errSkip
	}
	for _, k := range []string{KindQuestion, KindReview, KindFindings, KindNotice} { // only the kind's own object
		if k != it.Kind {
			delete(fields, k)
		}
	}
	return it, nil
}

func isObject(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) > 0 && raw[0] == '{'
}

func listDir(dir *os.Root) ([]string, error) {
	f, err := dir.Open(".")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.Readdirnames(-1)
}

func realDir(root *os.Root, name string) (*os.Root, error) {
	fi, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", name)
	}
	return root.OpenRoot(name)
}

// openShipDir refuses a link. The caller closes the result.
func openShipDir(home, session string) (*os.Root, error) {
	if !validSession(session) {
		return nil, ErrNotFound
	}
	root, err := os.OpenRoot(home)
	if err != nil {
		return nil, ErrNotFound
	}
	defer root.Close()
	inbox, err := realDir(root, "inbox")
	if err != nil {
		return nil, ErrNotFound
	}
	defer inbox.Close()
	dir, err := realDir(inbox, session)
	if err != nil {
		return nil, ErrNotFound
	}
	return dir, nil
}

// Waiting counts only the unanswered items the ship's report lists in `inbox`,
// or every one when there is no such list: a ship that took its answer in its
// terminal is not kept for an answer it will never read.
func Waiting(home, session string) bool {
	dir, err := openShipDir(home, session)
	if err != nil {
		return false
	}
	defer dir.Close()
	open := scanShip(dir, session)
	var r struct {
		Inbox *[]string `json:"inbox"`
	}
	if b := readReportFile(home, session); b == nil || json.Unmarshal(b, &r) != nil || r.Inbox == nil {
		return len(open) > 0
	}
	for _, it := range open {
		if slices.Contains(*r.Inbox, it.ID) {
			return true
		}
	}
	return false
}

// scan returns open items oldest first.
func scan(home string) []Item {
	items := []Item{}
	root, err := os.OpenRoot(home)
	if err != nil {
		return items
	}
	defer root.Close()
	inbox, err := realDir(root, "inbox")
	if err != nil {
		return items
	}
	defer inbox.Close()
	sessions, _ := listDir(inbox)
	for _, session := range sessions {
		if !validSession(session) {
			continue
		}
		dir, err := realDir(inbox, session)
		if err != nil {
			continue
		}
		items = append(items, scanShip(dir, session)...)
		dir.Close()
	}
	sort.Slice(items, func(i, j int) bool {
		a, b := items[i], items[j]
		switch {
		case !a.Created.Equal(b.Created):
			return a.Created.Before(b.Created)
		case a.Session != b.Session:
			return a.Session < b.Session
		}
		return a.ID < b.ID
	})
	return items
}

func scanShip(dir *os.Root, session string) []Item {
	names, _ := listDir(dir)
	have := map[string]bool{}
	for _, n := range names {
		have[n] = true
	}
	var items []Item
	for _, n := range names {
		id, ok := strings.CutSuffix(n, ".json")
		if !ok || !idPattern.MatchString(id) || have[answerName(id)] {
			continue
		}
		if it, err := readItem(dir, session, id); err == nil {
			items = append(items, it)
		}
	}
	return items
}

func ValidID(id string) bool { return idPattern.MatchString(id) }

const MaxItem = maxItem

func AnswerName(id string) string { return answerName(id) }

// ReadItem never follows a link and never waits on a pipe.
func ReadItem(dir *os.Root, session, id string) (Item, error) {
	if !idPattern.MatchString(id) {
		return Item{}, errSkip
	}
	return readItem(dir, session, id)
}

func ParseItem(b []byte, session, id string) (Item, error) { return parseItem(b, session, id) }
