package inbox

import (
	"strings"

	"github.com/jbuffin/admiral/internal/launcher"
)

// release removes the files before the record, so the name cannot be drawn
// again while they exist. It runs under answerMu, so it makes no plugin call.
func (s *Service) release(session string) {
	if s.Kept == nil {
		return
	}
	if _, kept := s.Kept.KeptInbox(session); !kept || Waiting(s.Home, session) {
		return
	}
	s.CaptureResume(session) // the report goes next
	launcher.RemoveShipFiles(s.Home, session)
	_ = s.Kept.DropInbox(session)
}

func (s *Service) sweepKept() {
	if s.Kept == nil {
		return
	}
	s.answerMu.Lock()
	defer s.answerMu.Unlock()
	for _, session := range s.Kept.KeptInboxes() {
		s.settleAnswered(session)
		s.release(session)
	}
}

// settleAnswered tells OnSettled about every answered item of a session release is about to delete, so an answer written while Admiral was down still settles its card.
func (s *Service) settleAnswered(session string) {
	if s.OnSettled == nil {
		return
	}
	if _, kept := s.Kept.KeptInbox(session); !kept || Waiting(s.Home, session) {
		return
	}
	dir, err := openShipDir(s.Home, session)
	if err != nil {
		return
	}
	names, _ := listDir(dir)
	var marks []struct {
		id      string
		removed bool
	}
	for _, name := range names {
		if id, ok := strings.CutSuffix(name, ".answer.json"); ok && idPattern.MatchString(id) {
			marks = append(marks, struct {
				id      string
				removed bool
			}{id, removed(dir, id)})
		}
	}
	dir.Close()
	for _, m := range marks {
		s.OnSettled(session, m.id, m.removed)
	}
}
