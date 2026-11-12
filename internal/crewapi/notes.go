package crewapi

import (
	"net/http"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jbuffin/admiral/internal/crew"
)

const maxNoteText = 500

type noteTarget struct {
	Scope string `json:"scope"`
	Repo  string `json:"repo"`
}

type noteRequest struct {
	Op     string      `json:"op"`
	Member string      `json:"member"`
	Scope  string      `json:"scope"`
	Repo   string      `json:"repo"`
	Note   string      `json:"note"`
	Text   string      `json:"text"`
	To     *noteTarget `json:"to"`
}

// repoOf is the "<owner>/<repo>" a scope names, empty for the fleet scope.
func (t noteTarget) repoOf() (string, string) {
	switch t.Scope {
	case "fleet":
		return "", ""
	case "repo":
		if _, _, ok := crew.SplitRepo(t.Repo); ok {
			return t.Repo, ""
		}
		return "", "repo must be <owner>/<repo>"
	}
	return "", "scope must be fleet or repo"
}

// indexOfNote finds the first line that is the note, the way the resolver reads it: without a trailing "\r".
func indexOfNote(lines []string, note string) int {
	return slices.IndexFunc(lines, func(l string) bool { return strings.TrimSuffix(l, "\r") == "- "+note })
}

func (s *Service) serveNotes(w http.ResponseWriter, r *http.Request) {
	c, ok := s.fleetCrew(w, r)
	if !ok {
		return
	}
	var req noteRequest
	if !decode(w, r, &req) {
		return
	}
	if !crew.ValidName(req.Member) {
		writeError(w, http.StatusBadRequest, "member "+req.Member+" is not a lowercase slug of at most 64 characters")
		return
	}
	repo, msg := noteTarget{req.Scope, req.Repo}.repoOf()
	var dest string
	switch {
	case msg != "":
		writeError(w, http.StatusBadRequest, msg)
		return
	case req.Note == "":
		writeError(w, http.StatusBadRequest, "note is required")
		return
	case req.Op == "edit" && len(req.Text) > maxNoteText:
		writeError(w, http.StatusBadRequest, "text is over 500 bytes")
		return
	case req.Op == "move":
		if req.To == nil {
			writeError(w, http.StatusBadRequest, "to is required to move a note")
			return
		}
		if dest, msg = req.To.repoOf(); msg != "" {
			writeError(w, http.StatusBadRequest, "to: "+msg)
			return
		}
		if dest == repo {
			writeError(w, http.StatusBadRequest, "to: is the scope the note is already in")
			return
		}
	case req.Op != "edit" && req.Op != "delete":
		writeError(w, http.StatusBadRequest, "op must be edit, move or delete")
		return
	}

	writeMu.Lock()
	defer writeMu.Unlock()
	crewDir, found, err := c.memberDir(false)
	if err != nil {
		refuse(w, err)
		return
	}
	src := scopeFile{crewDir, req.Member, repo}
	var lines []string
	if found {
		lines, found, err = src.read()
		if err != nil {
			refuse(w, err)
			return
		}
	}
	at := indexOfNote(lines, req.Note)
	if !found || at < 0 || !crew.ValidNote("- "+req.Note) {
		writeError(w, http.StatusConflict, "that note is no longer there; reload the crew")
		return
	}
	switch req.Op {
	case "edit":
		prefix := req.Note[:strings.Index(req.Note, "): ")+3]
		line := "- " + prefix + req.Text
		if !crew.ValidNote(line) {
			writeError(w, http.StatusBadRequest, "text must be one line of visible text that starts with a character that isn't a space")
			return
		}
		if strings.HasSuffix(lines[at], "\r") {
			line += "\r"
		}
		lines[at] = line
	case "move":
		dst := scopeFile{crewDir, req.Member, dest}
		into, _, err := dst.read()
		if err != nil {
			refuse(w, err)
			return
		}
		if dst.full(into) {
			writeError(w, http.StatusConflict, ErrScopeFull.Error()+"; nothing changed")
			return
		}
		// Written first, so a failure between the two writes leaves a copy and never loses the note.
		if err := dst.write(appended(into, lines[at])); err != nil {
			refuse(w, err)
			return
		}
		lines = slices.Delete(lines, at, at+1)
	case "delete":
		lines = slices.Delete(lines, at, at+1)
	}
	if err := src.write(lines); err != nil {
		refuse(w, err)
		return
	}
	var ignored []crew.Diagnostic
	fleet, repos := crew.MemberNotes(filepath.Join(crewDir, "memory"), req.Member, &ignored)
	writeJSON(w, http.StatusOK, notesView{fleet, repos})
}
