package inbox

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"syscall"
	"time"

	"github.com/jbuffin/admiral/internal/outcomes"
)

// RemoveItem takes an item out of the inbox without answering it, for one the
// engineer settled elsewhere (in the ship's terminal) or that no longer
// matters. A review goes the way of ForgetReview: nothing changes on GitHub. A
// question or findings item gets an answer file that says "removed": true and
// records item_removed. A removed item never resumes its ship.
func (s *Service) RemoveItem(session, id string) (ReviewResult, error) {
	it, err := s.open(session, id)
	if err != nil {
		return ReviewResult{}, err
	}
	if it.Kind == KindReview {
		return s.ForgetReview(session, id)
	}
	err = s.Answer(session, id, it.Kind, func(Item) (any, []outcomes.Record, error) {
		file := struct {
			Version  int    `json:"version"`
			ID       string `json:"id"`
			Kind     string `json:"kind"`
			Answered string `json:"answered"`
			Removed  bool   `json:"removed"`
		}{1, id, it.Kind, time.Now().UTC().Format(time.RFC3339), true}
		return file, []outcomes.Record{{Event: "item_removed", Fields: map[string]any{"item": id, "kind": it.Kind}}}, nil
	})
	return ReviewResult{Forgotten: true}, err
}

// serveRemove takes any item out of the inbox (RemoveItem).
func (s *Service) serveRemove(w http.ResponseWriter, r *http.Request) {
	session, id := r.PathValue("session"), r.PathValue("id")
	res, err := s.RemoveItem(session, id)
	respond(w, session, id, res, err)
}

// removed is whether the answer to item id in dir says the item was removed.
func removed(dir *os.Root, id string) bool {
	f, err := dir.OpenFile(answerName(id), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return false
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		return false
	}
	var a struct {
		Removed bool `json:"removed"`
	}
	b, err := io.ReadAll(io.LimitReader(f, maxItem))
	return err == nil && json.Unmarshal(b, &a) == nil && a.Removed
}
