package inbox

import "github.com/jbuffin/admiral/internal/launcher"

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
		s.release(session)
	}
}
