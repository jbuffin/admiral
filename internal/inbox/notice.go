package inbox

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// AdmiralSession is a pseudo-session for Admiral's own notices, the way `_chat` is the chat's.
const AdmiralSession = "_admiral"

// NoticeEntry is a view autopilot entry a migration removed: the source's key and name and the panel id.
type NoticeEntry struct {
	Key   string `json:"key"`
	Name  string `json:"name"`
	Panel string `json:"panel"`
}

// WriteNotice posts a read-only item the engineer can only dismiss; entries ride in the notice object for the shell to link.
func WriteNotice(home, title, body string, entries ...NoticeEntry) (string, error) {
	now := time.Now().UTC()
	id := fmt.Sprintf("notice-%s-%d", now.Format("20060102T150405"), now.UnixNano()%1_000_000)
	b, err := json.Marshal(map[string]any{
		"version":  1,
		"id":       id,
		"kind":     KindNotice,
		"title":    title,
		"created":  now.Format(time.RFC3339),
		KindNotice: map[string]string{"body": body},
	})
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, "inbox", AdmiralSession)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, ".notice-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name()) // no-op after the rename
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	return id, os.Rename(tmp.Name(), filepath.Join(dir, id+".json"))
}

// OpenNotices lists the notices not yet dismissed, oldest first.
func OpenNotices(home string) []Item {
	var out []Item
	for _, it := range scan(home) {
		if it.Session == AdmiralSession && it.Kind == KindNotice {
			out = append(out, it)
		}
	}
	return out
}

// NoticeBody is the body of a notice item.
func (it Item) NoticeBody() string {
	var n struct {
		Body string `json:"body"`
	}
	_ = json.Unmarshal(it.fields[KindNotice], &n)
	return n.Body
}

// NoticeEntries are the migrated entries a notice carries; none for a plain note.
func (it Item) NoticeEntries() []NoticeEntry {
	return nil
}
