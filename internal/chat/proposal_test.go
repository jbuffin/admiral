package chat

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/commitments"
	"github.com/jbuffin/admiral/internal/inbox"
	"github.com/jbuffin/admiral/internal/ledger"
	"github.com/jbuffin/admiral/internal/sessionhost"
)

const (
	propID    = "campaign-w1-ab12-1"
	propChild = "https://github.com/acme/widgets/issues/21"
	propTitle = "Start Annex doors of Moonbase annex with you?"
)

func propose(t *testing.T, r *briefRig, fleet string) {
	t.Helper()
	if err := r.Propose(fleet, commitments.Proposal{ID: propID, Title: propTitle, Child: propChild}); err != nil {
		t.Fatalf("Propose: %v", err)
	}
}

func (r *briefRig) questionPath(fleet, id string) string {
	return filepath.Join(r.home, "chat", fleet, "questions", id+".json")
}

// reply answers a chat question the way the chat and the Inbox do.
func (r *briefRig) reply(t *testing.T, fleet, id string, a inbox.QuestionAnswer) {
	t.Helper()
	req := inbox.QuestionRequest{Answers: map[string]inbox.QuestionAnswer{"start": a}}
	if err := r.AnswerQuestion(sessionhost.ChatSession(fleet), id, req); err != nil {
		t.Fatalf("AnswerQuestion(%s) = %v", id, err)
	}
}

func choice(label string) inbox.QuestionAnswer {
	raw, _ := json.Marshal(label)
	return inbox.QuestionAnswer{Choice: raw}
}

func TestAProposalIsAQuestionFileOfItsFleetWithAYesAndANoOption(t *testing.T) {
	r := newBriefRig(t)
	propose(t, r, "home")
	propose(t, r, "home")
	b, err := os.ReadFile(r.questionPath("home", propID))
	if err != nil {
		t.Fatalf("no question file for the proposal: %v", err)
	}
	var got struct {
		Title    string
		Question struct {
			Questions []struct {
				ID      string
				Multi   bool
				Options []struct{ Label string }
			}
		}
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	qs := got.Question.Questions
	if got.Title != propTitle || len(qs) != 1 || qs[0].ID != "start" || qs[0].Multi || len(qs[0].Options) != 2 || qs[0].Options[0].Label != commitments.ProposalYes || qs[0].Options[1].Label != commitments.ProposalNo {
		t.Errorf("question %s; want %q with one single-choice question `start` of Yes and No", b, propTitle)
	}
	files, _ := filepath.Glob(filepath.Join(r.home, "chat", "home", "questions", "*.json"))
	if len(files) != 1 {
		t.Errorf("files %v; asking one id twice makes one question", files)
	}
	if other, _ := filepath.Glob(filepath.Join(r.home, "chat", "work", "questions", "*.json")); len(other) != 0 {
		t.Errorf("work's questions %v; the proposal is home's", other)
	}
}

func TestAProposalReadsOpenUntilAnsweredThenYesNoOrAnOtherOnlyNo(t *testing.T) {
	for name, tc := range map[string]struct {
		answer inbox.QuestionAnswer
		want   string
	}{
		"yes":        {choice(commitments.ProposalYes), commitments.ProposalYes},
		"no":         {choice(commitments.ProposalNo), commitments.ProposalNo},
		"other only": {inbox.QuestionAnswer{Other: "not now, the build is red"}, commitments.ProposalNo},
	} {
		t.Run(name, func(t *testing.T) {
			r := newBriefRig(t)
			propose(t, r, "home")
			if got := r.Reply("home", propID); got != commitments.ProposalOpen {
				t.Fatalf("Reply before an answer = %q, want open", got)
			}
			r.reply(t, "home", propID, tc.answer)
			if got := r.Reply("home", propID); got != tc.want {
				t.Errorf("Reply = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAProposalWhoseFileIsGoneReadsGoneAndWithdrawRemovesItsFile(t *testing.T) {
	r := newBriefRig(t)
	if got := r.Reply("home", propID); got != commitments.ProposalGone {
		t.Errorf("Reply for a question never asked = %q, want gone", got)
	}
	propose(t, r, "home")
	r.Withdraw("home", propID)
	if _, err := os.Stat(r.questionPath("home", propID)); !os.IsNotExist(err) {
		t.Errorf("the question file is still there after Withdraw: %v", err)
	}
	if got := r.Reply("home", propID); got != commitments.ProposalGone {
		t.Errorf("Reply after Withdraw = %q, want gone", got)
	}
	propose(t, r, "home")
	if err := os.Remove(r.questionPath("home", propID)); err != nil {
		t.Fatal(err)
	}
	r.questions("home")
	if got := r.Reply("home", propID); got != commitments.ProposalGone {
		t.Errorf("Reply after the file was removed by hand = %q, want gone", got)
	}
}

func (r *briefRig) questions(fleet string) []Question { return r.chat(fleet).questions(true) }

func TestAChatIsOnForAFleetWithAChatAndNotForAnUnknownOne(t *testing.T) {
	r := newBriefRig(t)
	if !r.ChatOn("home") || r.ChatOn("nowhere") {
		t.Errorf("ChatOn(home) = %v, ChatOn(nowhere) = %v; want true and false", r.ChatOn("home"), r.ChatOn("nowhere"))
	}
}

func TestAProposalIsNotAWaitingInboxItemButAdmiralBotsOwnQuestionIs(t *testing.T) {
	r := newBriefRig(t)
	r.post(t, "/api/chat/questions", askBody, "home")
	propose(t, r, "home")
	var titles []string
	for _, it := range r.WaitingQuestions() {
		var title string
		_ = json.Unmarshal(it.Field("title"), &title)
		titles = append(titles, title)
	}
	if len(titles) != 1 || titles[0] != "Which branch?" {
		t.Errorf("waiting questions %v; want only AdmiralBot's own, not the proposal", titles)
	}
}

func TestAProposalShowsInTheChatButDoesNotMakeItWaitOnAnAnswer(t *testing.T) {
	r := newBriefRig(t)
	propose(t, r, "home")
	qs := r.questions("home")
	if len(qs) != 1 || qs[0].Item.ID != propID {
		t.Fatalf("questions %+v; want the proposal listed so the chat can show it", qs)
	}
	if unanswered(qs) {
		t.Error("a proposal counts as a question the chat waits on, want it ignored")
	}
	r.post(t, "/api/chat/questions", askBody, "home")
	if !unanswered(r.questions("home")) {
		t.Error("AdmiralBot's own question no longer counts as waiting")
	}
}

func TestANewChatWithdrawsAdmiralBotsQuestionsButKeepsAProposal(t *testing.T) {
	r := newBriefRig(t)
	own := r.post(t, "/api/chat/questions", askBody, "home")
	var asked struct{ ID string }
	_ = json.Unmarshal(own.Body.Bytes(), &asked)
	propose(t, r, "home")
	r.WithdrawQuestions("home")
	if _, err := os.Stat(r.questionPath("home", asked.ID)); !os.IsNotExist(err) {
		t.Errorf("AdmiralBot's question survived a new chat: %v", err)
	}
	if _, err := os.Stat(r.questionPath("home", propID)); err != nil {
		t.Errorf("the proposal was removed by a new chat: %v", err)
	}
	if got := r.Reply("home", propID); got != commitments.ProposalOpen {
		t.Errorf("Reply after a new chat = %q, want still open", got)
	}
}

// section is the text under a `## heading` of the brief, up to the next one.
func section(brief, heading string) string {
	_, rest, ok := strings.Cut(brief, "## "+heading+"\n")
	if !ok {
		return ""
	}
	rest, _, _ = strings.Cut(rest, "\n## ")
	return rest
}

func TestTheBriefListsAProposalApartFromAdmiralBotsWaitingQuestions(t *testing.T) {
	r := newBriefRig(t)
	r.post(t, "/api/chat/questions", askBody, "home")
	propose(t, r, "home")
	r.Tick()
	got := r.brief("home")
	if waiting := section(got, "AdmiralBot's waiting questions"); !strings.Contains(waiting, "Which branch?") || strings.Contains(waiting, "Annex doors") {
		t.Errorf("waiting questions section %q; want AdmiralBot's own question and not the proposal", waiting)
	}
	if !strings.Contains(got, "Admiral is asking") || !strings.Contains(got, propTitle) {
		t.Errorf("brief %q; want a line that says Admiral is asking, with %q", got, propTitle)
	}
}

func TestNoAnsweredYourQuestionLineIsMadeForAProposalButIsForAdmiralBotsOwn(t *testing.T) {
	r := newBriefRig(t)
	own := r.post(t, "/api/chat/questions", askBody, "home")
	var asked struct{ ID string }
	_ = json.Unmarshal(own.Body.Bytes(), &asked)
	propose(t, r, "home")
	r.Tick()
	r.reply(t, "home", propID, choice(commitments.ProposalYes))
	req := inbox.QuestionRequest{Answers: map[string]inbox.QuestionAnswer{"q1": choice("main")}}
	if err := r.AnswerQuestion(sessionhost.ChatSession("home"), asked.ID, req); err != nil {
		t.Fatal(err)
	}
	r.Tick()
	lines := lineTexts(r.digest(t, "home"))
	if !strings.Contains(lines, "answered your question: Which branch?") {
		t.Fatalf("digest %q; want the line for AdmiralBot's own question", lines)
	}
	if strings.Contains(lines, "Annex doors") {
		t.Errorf("digest %q; want no line for the proposal's answer", lines)
	}
}

func TestCancellingACampaignCommitmentWithdrawsItsOpenProposal(t *testing.T) {
	r := newBriefRig(t)
	c, _, err := r.notes().AddCampaignCommitment("home", ledger.Commitment{Text: "work on acme/widgets#20", Trigger: ledger.Trigger{Kind: ledger.TriggerCampaign, Subject: "camp-1"}, Action: ledger.ActionWork, Harness: "claude"}, "admiralbot")
	if err != nil {
		t.Fatal(err)
	}
	line := `{"v":1,"ts":"2026-11-12T09:00:00Z","event":"child_proposed","id":"` + c.ID + `","url":"` + propChild + `","question":"` + propID + `"}` + "\n"
	f, err := os.OpenFile(filepath.Join(r.home, "chat", "home", "ledger.jsonl"), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(line); err != nil {
		t.Fatal(err)
	}
	f.Close()
	propose(t, r, "home")

	rec := httptest.NewRecorder()
	r.mux.ServeHTTP(rec, httptest.NewRequest("DELETE", "/api/chat/home/commitments/"+c.ID, nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE = %d %s, want 204", rec.Code, rec.Body)
	}
	if _, err := os.Stat(r.questionPath("home", propID)); !os.IsNotExist(err) {
		t.Errorf("the open proposal survived the cancel: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(r.home, "chat", "home", "ledger.jsonl"))
	text := string(b)
	w, x := strings.Index(text, `"event":"child_withdrawn"`), strings.Index(text, `"event":"cancelled"`)
	if w < 0 || x < 0 || w > x || !strings.Contains(text, `"reason":"cancelled"`) {
		t.Errorf("ledger %s; want child_withdrawn (cancelled) before the cancel", text)
	}
}
