// Package inbox is the Needs You inbox on the server (ADR 0016). Ships write
// item files under $ADMIRAL_HOME/inbox/<session>/ (#112); Admiral reads them,
// watches the directories, serves the open ones and writes the engineer's
// answer next to an item as <id>.answer.json. It never edits an item.
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
	"sort"
	"strings"
	"syscall"
	"time"
)

// maxItem is the most of an item file that Admiral reads. A ship writes the
// file, so a larger one is skipped.
const maxItem = 1 << 20

// idPattern is what an item id must match (Commodore's ID_RE).
var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

// Item kinds.
const (
	KindQuestion = "question"
	KindReview   = "review"
	KindFindings = "findings"
)

// Item is one item as served: the item file's fields, passed through as the
// ship wrote them, and the ship's session, name and harness.
type Item struct {
	fields map[string]json.RawMessage
	// Session is the ship's session name, the directory the item is in.
	Session string
	// Name and Harness are the ship's, when the ship is known.
	Name, Harness string
	// ID and Kind come from the file; Created is its parsed created time.
	ID, Kind string
	Created  time.Time
}

// Field is a field of the item file as written, nil when it has none.
func (it Item) Field(name string) json.RawMessage { return it.fields[name] }

// MarshalJSON is the item file's fields with session, name and harness set last,
// so a file cannot name another ship.
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

// errSkip marks a file that is not an item.
var errSkip = errors.New("not an item")

// answerName is the file that holds the answer to item id.
func answerName(id string) string { return id + ".answer.json" }

// validSession is whether name can be a ship's inbox directory.
func validSession(name string) bool {
	return name != "" && !strings.HasPrefix(name, ".") && filepath.IsLocal(name) && !strings.ContainsRune(name, filepath.Separator)
}

// Answered is whether item id in dir already has an answer file, whoever wrote it.
func Answered(dir *os.Root, id string) bool {
	_, err := dir.Lstat(answerName(id))
	return err == nil
}

// readItem reads and validates dir/<id>.json against #112's item format. It
// never follows a link and never waits on a pipe.
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

// parseItem validates the file's bytes as item id of session.
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
	default:
		return Item{}, errSkip
	}
	for _, k := range []string{KindQuestion, KindReview, KindFindings} { // only the kind's own object
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

// listDir is the names in dir.
func listDir(dir *os.Root) ([]string, error) {
	f, err := dir.Open(".")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.Readdirnames(-1)
}

// realDir opens name under root when it is a directory itself and not a link.
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

// openShipDir opens the directory of session's inbox, which must be a directory
// itself and not a link, through a root on home. The caller closes it. A
// directory that is not there is ErrNotFound.
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

// Unanswered is whether the session's inbox holds an item with no answer.
func Unanswered(home, session string) bool {
	dir, err := openShipDir(home, session)
	if err != nil {
		return false
	}
	defer dir.Close()
	return len(scanShip(dir, session)) > 0
}

// scan is the open items of every ship directory under home, oldest first. A
// file that is not a valid item, and an item with an answer, is left out.
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

// ValidID is whether id can name an item file (ADR 0016's id rule).
func ValidID(id string) bool { return idPattern.MatchString(id) }

// MaxItem is the most of an item or answer file that Admiral reads.
const MaxItem = maxItem

// AnswerName is the file that holds the answer to item id.
func AnswerName(id string) string { return answerName(id) }

// ReadItem reads and validates dir/<id>.json as item id of session, never
// following a link and never waiting on a pipe. A file that is not a valid item
// is an error.
func ReadItem(dir *os.Root, session, id string) (Item, error) {
	if !idPattern.MatchString(id) {
		return Item{}, errSkip
	}
	return readItem(dir, session, id)
}

// ParseItem validates b as item id of session against #112's item format.
func ParseItem(b []byte, session, id string) (Item, error) { return parseItem(b, session, id) }
