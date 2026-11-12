package chat

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"time"

	"github.com/jbuffin/admiral/internal/actor"
	"github.com/jbuffin/admiral/internal/inbox"
)

// maxAnswerWait caps one long poll; the asker polls again with the question's id.
const maxAnswerWait = 110

// questionID is the shape chat ask gives a question: a UTC time and four random bytes.
var questionID = regexp.MustCompile(`^\d{8}T\d{6}-[0-9a-f]{8}$`)

const answerPoll = 100 * time.Millisecond

// serveAsk writes a question into the token's fleet's chat, whatever fleet the body names.
func (s *Service) serveAsk(w http.ResponseWriter, r *http.Request) {
	fleet, ok := actor.FleetOf(r.Context())
	if !ok {
		writeError(w, http.StatusForbidden, "only AdmiralBot asks chat questions")
		return
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, inbox.MaxItem+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "the question could not be read")
		return
	}
	root, err := os.OpenRoot(s.Home)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer root.Close()
	id, err := inbox.WriteQuestion(root, fleet, b)
	var qe *inbox.QuestionError
	switch {
	case errors.As(err, &qe):
		writeError(w, http.StatusBadRequest, qe.Msg)
	case err != nil:
		writeError(w, http.StatusInternalServerError, err.Error())
	default:
		writeJSON(w, http.StatusCreated, map[string]string{"id": id})
	}
}

// serveAskAnswer answers a question of the token's own fleet: the answer, "waiting" after wait seconds, or "withdrawn" once its file is gone.
func (s *Service) serveAskAnswer(w http.ResponseWriter, r *http.Request) {
	fleet, ok := actor.FleetOf(r.Context())
	if !ok {
		writeError(w, http.StatusForbidden, "only AdmiralBot reads chat answers")
		return
	}
	id := r.PathValue("id")
	if !questionID.MatchString(id) {
		writeError(w, http.StatusBadRequest, "not a question id")
		return
	}
	wait := 0
	if v := r.URL.Query().Get("wait"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "wait is a number of seconds")
			return
		}
		wait = min(n, maxAnswerWait)
	}
	root, err := os.OpenRoot(s.Home)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer root.Close()
	deadline := time.Now().Add(time.Duration(wait) * time.Second)
	for {
		if b, ok := inbox.ReadQuestionAnswer(root, fleet, id); ok {
			writeJSON(w, http.StatusOK, map[string]any{"state": "answered", "answer": json.RawMessage(b)})
			return
		}
		if !inbox.QuestionExists(root, fleet, id) {
			writeJSON(w, http.StatusOK, map[string]string{"state": "withdrawn"})
			return
		}
		if !time.Now().Before(deadline) {
			writeJSON(w, http.StatusOK, map[string]string{"state": "waiting"})
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(answerPoll):
		}
	}
}
