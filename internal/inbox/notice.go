package inbox

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// AdmiralSession is the pseudo-session directory under inbox/ that holds
// Admiral's own notices, the way `_chat` is the chat's session name.
const AdmiralSession = "_admiral"

// WriteNotice posts a notice to the inbox of home: a read-only item with a
// title and a plain-text body, which the engineer can only dismiss. The id is
// returned. A later migration posts its own the same way (ADR 0034).
func WriteNotice(home, title, body string) (string, error) {
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
