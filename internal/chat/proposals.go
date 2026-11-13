package chat

import (
	"encoding/json"
	"os"

	"github.com/jbuffin/admiral/internal/commitments"
	"github.com/jbuffin/admiral/internal/inbox"
)

// Propose, Reply, Withdraw and ChatOn make *Service the runner's question seam for With-you children.
var _ commitments.Proposals = (*Service)(nil)

// proposalQuestions is a proposal's one single-choice question; its text is the title, which the open card shows.
func proposalQuestions(title string) []map[string]any {
	return []map[string]any{{"id": "start", "text": title, "multi": false, "options": []map[string]string{{"label": commitments.ProposalYes}, {"label": commitments.ProposalNo}}}}
}

func (s *Service) ChatOn(fleet string) bool {
	_, err := s.chatFor(fleet)
	return err == nil
}

// Propose writes the question unless its id is already there, then shows it in the chat.
func (s *Service) Propose(fleet string, p commitments.Proposal) error {
	fc, lerr := s.chatFor(fleet)
	if lerr != nil {
		return os.ErrNotExist
	}
	body, err := json.Marshal(map[string]any{"title": p.Title, "questions": proposalQuestions(p.Title)})
	if err != nil {
		return err
	}
	fc.qmu.Lock()
	err = func() error {
		root, err := os.OpenRoot(s.Home)
		if err != nil {
			return err
		}
		defer root.Close()
		if inbox.QuestionExists(root, fleet, p.ID) {
			return nil
		}
		_, err = inbox.WriteQuestionAs(root, fleet, p.ID, body)
		return err
	}()
	fc.qmu.Unlock()
	if err == nil {
		fc.questions(true)
		fc.broadcast()
	}
	return err
}

// Reply reads the engineer's answer: anything but a Yes counts as a No, including text typed with no choice.
func (s *Service) Reply(fleet, id string) string {
	root, err := os.OpenRoot(s.Home)
	if err != nil {
		return commitments.ProposalOpen
	}
	defer root.Close()
	if b, ok := inbox.ReadQuestionAnswer(root, fleet, id); ok {
		var a struct {
			Answers map[string]struct{ Choice json.RawMessage }
		}
		var label string
		if json.Unmarshal(b, &a) == nil {
			_ = json.Unmarshal(a.Answers["start"].Choice, &label)
		}
		if label == commitments.ProposalYes {
			return commitments.ProposalYes
		}
		return commitments.ProposalNo
	}
	if !inbox.QuestionExists(root, fleet, id) {
		return commitments.ProposalGone
	}
	return commitments.ProposalOpen
}

// Withdraw removes the question and its answer, so a chat card for it reads Withdrawn.
func (s *Service) Withdraw(fleet, id string) {
	fc := s.chat(fleet)
	fc.qmu.Lock()
	if dir, err := fc.openQuestions(); err == nil {
		_ = dir.Remove(id + ".json")
		_ = dir.Remove(inbox.AnswerName(id))
		dir.Close()
	}
	fc.qmu.Unlock()
	fc.questions(true)
	fc.broadcast()
}
