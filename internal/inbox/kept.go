package inbox

import "github.com/jbuffin/admiral/internal/launcher"

// release removes the report and inbox of a sailed-off ship whose inbox was
// kept, once none of its items is unanswered, and then drops its record. The
// files go first, so the name cannot be drawn again while they exist. It runs
// under answerMu, so it makes no plugin call: a resume order is captured here,
// and added by the resume loop.
func (s *Service) release(session string) {
	if s.Kept == nil {
		return
	}
	if _, kept := s.Kept.KeptInbox(session); !kept || Unanswered(s.Home, session) {
		return
	}
	s.CaptureResume(session) // the report goes next
	launcher.RemoveShipFiles(s.Home, session)
	_ = s.Kept.DropInbox(session)
}

// sweepKept releases every kept inbox whose items were all answered while
// Admiral was not running.
func (s *Service) sweepKept() {
	if s.Kept == nil {
		return
	}
	s.answerMu.Lock()
	defer s.answerMu.Unlock()
	for _, session := range s.Kept.KeptInboxes() {
		s.release(session)
	}
}
