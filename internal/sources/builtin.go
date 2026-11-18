package sources

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/jbuffin/admiral/internal/github"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/ships"
)

const (
	maxMessageTitleRunes = 120
	maxMessageBody       = 64 << 10
)

var (
	errNotBuiltin    = errors.New("The action is not that built-in action")
	errNoKind        = errors.New("The plugin did not declare the message kind of this action")
	errMessageStale  = errors.New("The message changed since it was shown; ask again")
	errMessageInErr  = errors.New("The page is in error, so its message actions are off")
	errIssueHasAShip = errors.New("The issue already has a ship")
)

// builtinTarget finds the row's built-in action of the given kind on the stored page; it needs no live plugin.
func (s *Service) builtinTarget(w http.ResponseWriter, r *http.Request, builtin string) (*runner, Action, bool) {
	key := r.PathValue("key")
	rn := s.Sup.runner(key)
	if rn == nil {
		writeError(w, http.StatusNotFound, "source "+key+" not found")
		return nil, Action{}, false
	}
	a, err := rn.moduleAction(r.PathValue("id"), r.PathValue("action"))
	if err == nil && a.Builtin != builtin {
		err = errNotBuiltin
	}
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return nil, Action{}, false
	}
	return rn, a, true
}

// serveBuiltinLaunch answers the issue a Launch action names, in its claim form, and the fleet it routes to.
func (s *Service) serveBuiltinLaunch(w http.ResponseWriter, r *http.Request) {
	_, a, ok := s.builtinTarget(w, r, "launch")
	if !ok {
		return
	}
	k, isIssue := ships.ClaimKey(a.URL)
	if !isIssue {
		writeError(w, http.StatusBadRequest, "Launch takes a GitHub issue URL")
		return
	}
	cfg := s.Config.Current()
	host := strings.TrimPrefix(k, "https://")
	host, path, _ := strings.Cut(host, "/")
	owner, rest, _ := strings.Cut(path, "/")
	repo, _, _ := strings.Cut(rest, "/")
	fleetKey := github.RouterFromFleets(cfg.Fleets).Route(host, owner+"/"+repo)
	fleet, found := cfg.Fleet(fleetKey)
	if !found {
		writeError(w, http.StatusBadRequest, "the issue routes to no fleet")
		return
	}
	if _, lerr := ships.CheckIssueURLs([]string{a.URL}, launcher.GitHubHosts(cfg, fleet)); lerr != nil {
		writeError(w, lerr.Status, cleanMsg(lerr.Msg))
		return
	}
	if s.IssueClaims != nil {
		if holder, held := s.IssueClaims()[k]; held {
			writeError(w, http.StatusConflict, cleanMsg(errIssueHasAShip.Error()+": "+holder))
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": k, "fleet": fleetKey})
}

// fenceFor is a run of backticks longer than any in text, and at least five.
func fenceFor(text string) string {
	longest, run := 0, 0
	for _, r := range text {
		if r == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	return strings.Repeat("`", max(5, longest+1))
}

// composeMessage is the text sent to AdmiralBot: the kind's job, the row's reference, its title as fenced data and its link, and nothing else the plugin wrote.
func composeMessage(page string, k MessageKind, a Action) string {
	title := strings.TrimSpace(truncate(a.Title, maxMessageTitleRunes))
	fence := fenceFor(title)
	return "*From the " + page + ":* " + k.Job + " Row: " + a.Ref + "\n" + fence + "\n" + title + "\n" + fence + "\n" + a.Link
}

// messageFor composes a message action's text from the stored page, or writes the refusal.
func (s *Service) messageFor(w http.ResponseWriter, r *http.Request) (text, fleet string, ok bool) {
	rn, a, ok := s.builtinTarget(w, r, "message")
	if !ok {
		return "", "", false
	}
	rn.mu.Lock()
	inError := rn.pageStateLocked() == PageError
	kinds, def := rn.kinds, rn.def
	rn.mu.Unlock()
	var kind MessageKind
	found := false
	for _, k := range kinds {
		if k.Kind == a.Kind {
			kind, found = k, true
		}
	}
	switch {
	case !found:
		writeError(w, http.StatusNotFound, errNoKind.Error())
		return "", "", false
	case inError:
		writeError(w, http.StatusConflict, errMessageInErr.Error())
		return "", "", false
	}
	page := cleanLine(def.Name)
	if page == "" {
		page = def.Key
	}
	return composeMessage(page, kind, a), def.Fleet, true
}

func chatPath(fleet string) string { return "/app/chat?fleet=" + url.QueryEscape(fleet) }

func (s *Service) serveBuiltinMessage(w http.ResponseWriter, r *http.Request) {
	text, fleet, ok := s.messageFor(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"text": text, "fleet": fleet, "chat": chatPath(fleet)})
}

// sendBuiltinMessage posts the composed text only when it is the text the client showed.
func (s *Service) sendBuiltinMessage(w http.ResponseWriter, r *http.Request) {
	text, fleet, ok := s.messageFor(w, r)
	if !ok {
		return
	}
	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxMessageBody)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "the body is not a JSON object with the shown text")
		return
	}
	if body.Text != text {
		writeError(w, http.StatusConflict, errMessageStale.Error())
		return
	}
	if s.SendChat != nil {
		if lerr := s.SendChat(fleet, text); lerr != nil {
			writeError(w, lerr.Status, cleanMsg(lerr.Msg))
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"sent": true, "chat": chatPath(fleet)})
}
