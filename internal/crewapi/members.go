package crewapi

import (
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/jbuffin/admiral/internal/crew"
)

type memberBody struct {
	Role         string   `json:"role"`
	BasedOn      *string  `json:"based-on"`
	Skills       []string `json:"skills"`
	Model        *string  `json:"model"`
	Harness      *string  `json:"harness"`
	PickMeWhen   string   `json:"pick-me-when"`
	Instructions string   `json:"instructions"`
}

// quoted is a double-quoted scalar the crew parser reads back exactly.
func quoted(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

func (b memberBody) file(name string) []byte {
	var sb strings.Builder
	sb.WriteString("---\nname: " + name + "\nrole: " + quoted(b.Role) + "\n")
	if b.BasedOn != nil {
		sb.WriteString("based-on: " + quoted(*b.BasedOn) + "\n")
	}
	if len(b.Skills) > 0 {
		sb.WriteString("skills:\n")
		for _, skill := range b.Skills {
			sb.WriteString("  - " + quoted(skill) + "\n")
		}
	}
	if b.Model != nil {
		sb.WriteString("model: " + quoted(*b.Model) + "\n")
	}
	if b.Harness != nil {
		sb.WriteString("harness: " + quoted(*b.Harness) + "\n")
	}
	sb.WriteString("pick-me-when: " + quoted(b.PickMeWhen) + "\n---\n")
	if b.Instructions != "" {
		sb.WriteString("\n" + b.Instructions + "\n")
	}
	return []byte(sb.String())
}

// oneLine names the first single-line field that holds a line break.
func (b memberBody) oneLine() string {
	fields := []struct {
		name  string
		value string
	}{{"role", b.Role}, {"pick-me-when", b.PickMeWhen}}
	for name, p := range map[string]*string{"based-on": b.BasedOn, "model": b.Model, "harness": b.Harness} {
		if p != nil {
			fields = append(fields, struct{ name, value string }{name, *p})
		}
	}
	for _, skill := range b.Skills {
		fields = append(fields, struct{ name, value string }{"skills", skill})
	}
	for _, f := range fields {
		if strings.ContainsAny(f.value, "\r\n") {
			return f.name
		}
	}
	return ""
}

// trimBlankLines drops the blank lines around s, as the resolver does when it reads a body.
func trimBlankLines(s string) string {
	lines := strings.Split(s, "\n")
	blank := func(l string) bool { return strings.Trim(l, " \t\r") == "" }
	for len(lines) > 0 && blank(lines[0]) {
		lines = lines[1:]
	}
	for len(lines) > 0 && blank(lines[len(lines)-1]) {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

func sameOptional(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// differs names the first field the resolver would read back differently from what was sent.
func (b memberBody) differs(m crew.Member) string {
	switch {
	case m.Role != b.Role:
		return "role"
	case !sameOptional(m.BasedOn, b.BasedOn):
		return "based-on"
	case !slices.Equal(m.Skills, b.Skills):
		return "skills"
	case !sameOptional(m.Model, b.Model):
		return "model"
	case !sameOptional(m.Harness, b.Harness):
		return "harness"
	case m.PickMeWhen != b.PickMeWhen:
		return "pick-me-when"
	case m.Instructions != b.Instructions:
		return "instructions"
	}
	return ""
}

func fieldMessage(field, message string) string {
	if field == "" {
		return message
	}
	return field + ": " + message
}

// fieldError answers a 400 that names a member field, so the page can show it beside that input.
func fieldError(w http.ResponseWriter, field, message string) {
	body := map[string]string{"error": fieldMessage(field, message)}
	if field != "" {
		body["field"] = field
	}
	writeJSON(w, http.StatusBadRequest, body)
}

// memberDir is the fleet's crew directory when it can be read; found is false when it doesn't exist yet.
func (c fleetCrew) memberDir(create bool) (dir string, found bool, err error) {
	return walk(c.home, create, "fleets", c.key, "crew")
}

// lstatFile reports whether path holds a file; anything but a regular file there is a refusal.
func lstatFile(path string) (bool, error) {
	fi, err := os.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR):
		return false, nil
	case err != nil:
		return false, err
	case !fi.Mode().IsRegular():
		return true, errLink
	}
	return true, nil
}

// fleetFile finds a fleet member's file by name, answering the request itself when there is none.
func (s *Service) fleetFile(w http.ResponseWriter, r *http.Request, c fleetCrew, name string) (dir string, ok bool) {
	dir, found, err := c.memberDir(false)
	if err == nil && found {
		found, err = lstatFile(filepath.Join(dir, name+".md"))
	}
	switch {
	case err != nil:
		refuse(w, err)
	case found:
		return dir, true
	case c.builtIns()[name].Name != "":
		writeError(w, http.StatusConflict, name+" is a built-in member with no fleet file; customize it for this fleet first")
	default:
		writeError(w, http.StatusNotFound, "no crew member "+name+" in this fleet")
	}
	return "", false
}

func memberName(w http.ResponseWriter, r *http.Request) (string, bool) {
	name := r.PathValue("name")
	if !crew.ValidName(name) {
		writeError(w, http.StatusBadRequest, "crew member name "+name+" is not a lowercase slug of at most 64 characters")
		return "", false
	}
	return name, true
}

func (s *Service) servePut(w http.ResponseWriter, r *http.Request) {
	c, ok := s.fleetCrew(w, r)
	if !ok {
		return
	}
	name, ok := memberName(w, r)
	if !ok {
		return
	}
	var body memberBody
	if !decode(w, r, &body) {
		return
	}
	if body.Skills == nil {
		body.Skills = []string{}
	}
	body.Instructions = trimBlankLines(body.Instructions)
	writeMu.Lock()
	defer writeMu.Unlock()
	dir, ok := s.fleetFile(w, r, c, name)
	if !ok {
		return
	}
	if field := body.oneLine(); field != "" {
		fieldError(w, field, "must be one line")
		return
	}
	data := body.file(name)
	got, ferr := crew.CheckFile(data, name)
	if ferr != nil {
		fieldError(w, ferr.Field, ferr.Message)
		return
	}
	if field := body.differs(got); field != "" {
		fieldError(w, field, "would be read back differently from what was sent")
		return
	}
	if builtIn, ok := c.builtIns()[name]; ok {
		if builtIn.Role != got.Role {
			fieldError(w, "role", "differs from the built-in member, which a customized member keeps")
			return
		}
		if !sameOptional(builtIn.BasedOn, got.BasedOn) {
			fieldError(w, "based-on", "differs from the built-in member, which a customized member keeps")
			return
		}
	}
	if err := writeAtomic(dir, name+".md", data); err != nil {
		refuse(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"name": name})
}

func (s *Service) serveCustomize(w http.ResponseWriter, r *http.Request) {
	c, ok := s.fleetCrew(w, r)
	if !ok {
		return
	}
	name, ok := memberName(w, r)
	if !ok {
		return
	}
	writeMu.Lock()
	defer writeMu.Unlock()
	builtIn, ok := c.builtIns()[name]
	if !ok {
		writeError(w, http.StatusNotFound, "no built-in crew member "+name)
		return
	}
	dir, found, err := c.memberDir(false)
	if err == nil && found {
		var exists bool
		if exists, err = lstatFile(filepath.Join(dir, name+".md")); exists && err == nil {
			writeError(w, http.StatusConflict, "this fleet already has a crew member "+name)
			return
		}
	}
	if err == nil && !found {
		dir, _, err = c.memberDir(true)
	}
	if err != nil {
		refuse(w, err)
		return
	}
	data, err := crew.ReadFile(c.builtInDir, builtIn)
	if err != nil {
		writeError(w, http.StatusConflict, "the built-in member "+name+" can't be read: "+err.Error())
		return
	}
	if err := writeAtomic(dir, name+".md", data); err != nil {
		refuse(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"name": name})
}

func (s *Service) serveDelete(w http.ResponseWriter, r *http.Request) {
	c, ok := s.fleetCrew(w, r)
	if !ok {
		return
	}
	name, ok := memberName(w, r)
	if !ok {
		return
	}
	writeMu.Lock()
	defer writeMu.Unlock()
	dir, ok := s.fleetFile(w, r, c, name)
	if !ok {
		return
	}
	if err := os.Remove(filepath.Join(dir, name+".md")); err != nil {
		refuse(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"name": name})
}

// renamed is raw with the frontmatter's name line set to to.
func renamed(raw []byte, to string) []byte {
	lines := strings.Split(string(raw), "\n")
	for i := 1; i < len(lines) && lines[i] != "---"; i++ {
		if strings.HasPrefix(lines[i], "name:") {
			cr := ""
			if strings.HasSuffix(lines[i], "\r") {
				cr = "\r"
			}
			lines[i] = "name: " + to + cr
			break
		}
	}
	return []byte(strings.Join(lines, "\n"))
}

func (s *Service) serveRename(w http.ResponseWriter, r *http.Request) {
	c, ok := s.fleetCrew(w, r)
	if !ok {
		return
	}
	name, ok := memberName(w, r)
	if !ok {
		return
	}
	var req struct {
		To         string `json:"to"`
		CarryNotes bool   `json:"carry_notes"`
	}
	if !decode(w, r, &req) {
		return
	}
	switch {
	case !crew.ValidName(req.To):
		writeError(w, http.StatusBadRequest, "to: "+req.To+" is not a lowercase slug of at most 64 characters")
		return
	case req.To == name:
		writeError(w, http.StatusBadRequest, "to: is the member's own name")
		return
	}
	writeMu.Lock()
	defer writeMu.Unlock()
	dir, ok := s.fleetFile(w, r, c, name)
	if !ok {
		return
	}
	taken, err := lstatFile(filepath.Join(dir, req.To+".md"))
	if err != nil {
		refuse(w, err)
		return
	}
	if taken || c.builtIns()[req.To].Name != "" {
		writeError(w, http.StatusConflict, "a crew member named "+req.To+" is already listed")
		return
	}
	raw, _, err := readRegular(filepath.Join(dir, name+".md"))
	if err != nil {
		refuse(w, err)
		return
	}
	data := renamed(raw, req.To)
	if _, ferr := crew.CheckFile(data, req.To); ferr != nil {
		writeError(w, http.StatusConflict, "the file can't be renamed: "+fieldMessage(ferr.Field, ferr.Message))
		return
	}
	var from, to string
	if req.CarryNotes {
		var memory string
		if memory, from, err = carryPaths(dir, name, req.To); err != nil {
			refuse(w, err)
			return
		}
		to = filepath.Join(memory, req.To)
		if memory != "" {
			var ignored []crew.Diagnostic
			if fleet, repos := crew.MemberNotes(memory, req.To, &ignored); len(fleet) > 0 || len(repos) > 0 {
				writeError(w, http.StatusConflict, req.To+" already has notes, which carrying "+name+"'s would mix with; nothing changed")
				return
			}
		}
	}
	if err := writeAtomic(dir, req.To+".md", data); err != nil {
		refuse(w, err)
		return
	}
	if from != "" {
		// Only blank files and the directories they leave are cleared out of the way; any other content makes the rename fail.
		err := pruneBlank(to)
		if err == nil {
			err = os.Rename(from, to)
		}
		if err != nil {
			_ = os.Remove(filepath.Join(dir, req.To+".md"))
			writeError(w, http.StatusConflict, "the notes can't be moved to "+req.To+": "+err.Error())
			return
		}
	}
	if err := os.Remove(filepath.Join(dir, name+".md")); err != nil {
		refuse(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"name": req.To})
}

// pruneBlank removes the files under dir that hold nothing but blank lines, then each directory that leaves
// empty. It looks at links without following them and leaves everything else where it is.
func pruneBlank(dir string) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		switch {
		case e.IsDir():
			if err := pruneBlank(path); err != nil {
				return err
			}
		case e.Type().IsRegular():
			if raw, found, err := readRegular(path); err == nil && found && noNotes(strings.Split(string(raw), "\n")) {
				if err := os.Remove(path); err != nil {
					return err
				}
			}
		}
	}
	if err := os.Remove(dir); err != nil && !errors.Is(err, syscall.ENOTEMPTY) {
		return err
	}
	return nil
}

// carryPaths is the memory directory and the member's own directory in it, which a carry moves; each is
// empty when it doesn't exist. A link at either, or at the destination, is refused.
func carryPaths(crewDir, name, to string) (memory, from string, err error) {
	memory, found, err := walk(crewDir, false, "memory")
	if err != nil || !found {
		return "", "", err
	}
	if _, _, err := walk(memory, false, to); err != nil {
		return "", "", err
	}
	from, found, err = walk(memory, false, name)
	if err != nil || !found {
		return memory, "", err
	}
	return memory, from, nil
}
