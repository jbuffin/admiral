package ships

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/jbuffin/admiral/internal/actor"
	"github.com/jbuffin/admiral/internal/inbox"
	"github.com/jbuffin/admiral/internal/outcomes"
	"github.com/jbuffin/admiral/internal/sessionhost"
)

// serveNext gives a ship whose turn has ended its next prompt. It never types mid-turn: TypeResume refuses unless the ship needs you for `stop`.
func (s *Service) serveNext(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "request is not valid JSON: "+err.Error())
		return
	}
	switch {
	case !inbox.Typable(body.Text):
		writeError(w, http.StatusBadRequest, "the text is empty or holds a control character, so it can't be typed at a prompt")
		return
	case isHarnessCommand(r, body.Text):
		writeError(w, http.StatusBadRequest, "AdmiralBot may give a ship a prompt, never a harness command: the text can't start with / or !")
		return
	case len(body.Text) > sessionhost.MaxPaste:
		writeError(w, http.StatusBadRequest, fmt.Sprintf("the text is %d bytes; at most %d", len(body.Text), sessionhost.MaxPaste))
		return
	}
	session := r.PathValue("session")
	sess, ok := s.find(session)
	if !ok {
		if _, moored := s.mooredRecord(session); moored {
			writeError(w, http.StatusConflict, "ship "+session+" is moored: wake it instead")
			return
		}
		writeError(w, http.StatusNotFound, "no ship "+session)
		return
	}
	switch sh := s.ship(sess); {
	case sh.Status != StatusUnderway && sh.Status != StatusNeedsYou:
		writeError(w, http.StatusConflict, "ship "+session+" is "+sh.Status+", not afloat: wake it instead if it is moored")
		return
	case sh.Status != StatusNeedsYou || sh.Reason != "stop":
		writeError(w, http.StatusConflict, "ship "+session+" is mid-turn or waiting on something else, so its turn has not ended")
		return
	}
	_, typed, err := s.TypeResume(session, body.Text)
	if typed {
		s.record(outcomes.Record{Ship: session, Event: "resumed", Fields: withVia(map[string]any{"live": true}, viaOf(r))}, nil)
	}
	switch {
	case err != nil && !typed:
		writeError(w, http.StatusInternalServerError, "the text could not be typed: "+err.Error())
	case !typed:
		writeError(w, http.StatusConflict, "ship "+session+"'s turn ended no longer: it is mid-turn or waiting on something else")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "the text is in the ship's prompt but could not be sent: "+err.Error())
	default:
		writeJSON(w, http.StatusOK, map[string]any{"typed": true})
	}
}

// isHarnessCommand is true for an actor's text that the harness would run as a slash command or a shell line, which the
// harness's own approvals never see; the engineer's text is theirs to type.
func isHarnessCommand(r *http.Request, text string) bool {
	if _, acting := actor.FleetOf(r.Context()); !acting {
		return false
	}
	t := strings.TrimSpace(text)
	return strings.HasPrefix(t, "/") || strings.HasPrefix(t, "!")
}

// serveOutcomes reads the log. An actor sees only its fleet's records, each one's fleet coming from the latest `launched`
// record of its ship before it (the others name only the ship), else from the listed or moored ship; an unknown fleet is hidden.
func (s *Service) serveOutcomes(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := 100
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "limit is a positive number")
			return
		}
		limit = min(n, 1000)
	}
	fleet, acting := actor.FleetOf(r.Context())
	recs, err := readOutcomes(s.Launcher.Home, q.Get("ship"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "the outcomes could not be read: "+err.Error())
		return
	}
	if acting {
		recs = slices.DeleteFunc(recs, keepFleet(recs, fleet, s.FleetOf))
	}
	if len(recs) > limit {
		recs = recs[len(recs)-limit:]
	}
	out := make([]json.RawMessage, len(recs))
	for i, rec := range recs {
		out[i] = rec.line
	}
	writeJSON(w, http.StatusOK, map[string]any{"outcomes": out})
}

// keepFleet returns the DeleteFunc test for a walk of recs in file order. Names are reused once free, so a record takes the
// fleet of the latest `launched` record before it. A name with no such record in recs is judged by the ship that holds it now,
// unless recs hold a launch of that name from later on, which makes the record an older ship's and hides it.
func keepFleet(recs []outcomeRec, fleet string, now func(string) (string, bool)) func(outcomeRec) bool {
	launched := map[string]bool{}
	for _, rec := range recs {
		if rec.event == "launched" && rec.fleet != "" {
			launched[rec.ship] = true
		}
	}
	current := map[string]string{}
	return func(rec outcomeRec) bool {
		if rec.ship == "" {
			return true
		}
		if rec.event == "launched" && rec.fleet != "" {
			current[rec.ship] = rec.fleet
		}
		f, ok := current[rec.ship]
		if !ok && !launched[rec.ship] {
			f, _ = now(rec.ship)
		}
		return f != fleet
	}
}

type outcomeRec struct {
	ship, event, fleet string
	line               json.RawMessage
}

// readOutcomes returns the log's records oldest first, those of one ship when ship is set.
func readOutcomes(home, ship string) ([]outcomeRec, error) {
	files, err := outcomes.MonthFiles(filepath.Join(home, "outcomes"))
	if err != nil {
		return nil, err
	}
	var out []outcomeRec
	for _, f := range files {
		fh, err := os.Open(f.Path)
		if err != nil {
			return nil, err
		}
		br := bufio.NewReaderSize(fh, 64<<10)
		for {
			line, err := outcomes.ReadLine(br)
			if len(line) > 0 && line[0] == '{' {
				var rec struct {
					Ship  string `json:"ship"`
					Event string `json:"event"`
					Fleet string `json:"fleet"`
				}
				if json.Unmarshal(line, &rec) == nil && (ship == "" || rec.Ship == ship) {
					out = append(out, outcomeRec{rec.Ship, rec.Event, rec.Fleet, append(json.RawMessage(nil), line...)})
				}
			}
			if err != nil {
				fh.Close()
				if err != io.EOF {
					return nil, err
				}
				break
			}
		}
	}
	return out, nil
}
