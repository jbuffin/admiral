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

// RemoveItem changes nothing on GitHub, and a removed item never resumes its
// ship. A notice has no ship to sweep it, so removing one deletes its file.
func (s *Service) RemoveItem(session, id string) (ReviewResult, error) {
	it, err := s.open(session, id)
	if err != nil {
		return ReviewResult{}, err
	}
	if it.Kind == KindReview {
		return s.ForgetReview(session, id)
	}
	if it.Kind == KindNotice {
		return ReviewResult{Forgotten: true}, s.dismissNotice(session, id)
	}
	if it.Kind == KindWakeProposal {
		return ReviewResult{Forgotten: true}, s.DismissProposal(id)
	}
	if it.Kind == KindCrewNotes {
		return ReviewResult{Forgotten: true}, s.dropCrewNotes(id)
	}
	if it.Kind == KindCrewMember {
		gone, err := s.answerCrewMember(id, "drop", false)
		return ReviewResult{Forgotten: true, FileRemoved: &gone}, err
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

func (s *Service) dismissNotice(session, id string) error {
	s.answerMu.Lock()
	dir, err := openShipDir(s.Home, session)
	if err != nil {
		s.answerMu.Unlock()
		return ErrNotFound
	}
	err = dir.Remove(id + ".json")
	dir.Close()
	s.answerMu.Unlock()
	if err != nil {
		return ErrNotFound
	}
	s.Refresh()
	return nil
}

func (s *Service) serveRemove(w http.ResponseWriter, r *http.Request) {
	session, id := r.PathValue("session"), r.PathValue("id")
	res, err := s.RemoveItem(session, id)
	respond(w, session, id, res, err)
}

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
