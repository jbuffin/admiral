package sources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"unicode/utf8"
)

var (
	errNoAction   = errors.New("The row has no such module action")
	errNotAForm   = errors.New("The module action does not ask for a form")
	errActionLink = errors.New("The module action opens a link, and Admiral does not run it")
	errPageInErr  = errors.New("The page is in error, so its module actions are off")
	errRunOpen    = errors.New("A module action is already running on this row")
	errSourceDown = errors.New("The source is not running")
)

var durationValue = regexp.MustCompile(`^\d{2}:[0-5]\d$`)

type FormOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type FormField struct {
	ID      string       `json:"id"`
	Label   string       `json:"label"`
	Type    string       `json:"type"`
	Options []FormOption `json:"options,omitempty"`
	Default any          `json:"default,omitempty"`
	Min     string       `json:"min,omitempty"`
}

type Form struct {
	Title  string      `json:"title,omitempty"`
	Fields []FormField `json:"fields"`
}

// parseForm reads an action.form answer; anything outside the shape or the limits refuses the whole form.
func parseForm(raw json.RawMessage) (Form, error) {
	if len(raw) > maxFormBytes {
		return Form{}, fmt.Errorf("action.form: %d bytes, over the limit of %d", len(raw), maxFormBytes)
	}
	var res struct {
		Title  json.RawMessage   `json:"title"`
		Fields []json.RawMessage `json:"fields"`
	}
	if err := decodeInto(raw, &res); err != nil {
		return Form{}, fmt.Errorf("action.form: %w", err)
	}
	if len(res.Fields) > maxFormFields {
		return Form{}, fmt.Errorf("action.form: %d fields, over the limit of %d", len(res.Fields), maxFormFields)
	}
	form := Form{Title: titleLine(res.Title), Fields: []FormField{}}
	ids := map[string]bool{}
	for _, r := range res.Fields {
		f, err := parseFormField(r)
		if err != nil {
			return Form{}, fmt.Errorf("action.form: %w", err)
		}
		if ids[f.ID] {
			return Form{}, fmt.Errorf("action.form: field id %q is used twice", f.ID)
		}
		ids[f.ID] = true
		form.Fields = append(form.Fields, f)
	}
	return form, nil
}

func parseFormField(raw json.RawMessage) (FormField, error) {
	var item struct {
		ID      json.RawMessage   `json:"id"`
		Label   json.RawMessage   `json:"label"`
		Type    json.RawMessage   `json:"type"`
		Options []json.RawMessage `json:"options"`
		Default json.RawMessage   `json:"default"`
		Min     json.RawMessage   `json:"min"`
	}
	if err := decodeInto(raw, &item); err != nil {
		return FormField{}, err
	}
	id, ok := parseID(item.ID)
	if !ok {
		return FormField{}, errors.New("a field has no usable id")
	}
	typ, _ := rawString(item.Type)
	f := FormField{ID: id, Label: titleLine(item.Label), Type: typ}
	switch typ {
	case "choice", "checklist":
		if len(item.Options) > maxFormOptions {
			return FormField{}, fmt.Errorf("field %q has %d options, over the limit of %d", id, len(item.Options), maxFormOptions)
		}
		f.Options = []FormOption{}
		known := map[string]bool{}
		for _, o := range item.Options {
			var it struct{ ID, Label json.RawMessage }
			if err := decodeInto(o, &it); err != nil {
				return FormField{}, err
			}
			oid, ok := parseID(it.ID)
			if !ok || known[oid] {
				return FormField{}, fmt.Errorf("field %q has an option with an unusable or repeated id", id)
			}
			known[oid] = true
			f.Options = append(f.Options, FormOption{ID: oid, Label: titleLine(it.Label)})
		}
		f.Default = formDefault(typ, item.Default, known)
	case "duration":
		for _, v := range []struct {
			raw json.RawMessage
			dst *string
		}{{item.Default, nil}, {item.Min, &f.Min}} {
			if len(v.raw) == 0 || string(v.raw) == "null" {
				continue
			}
			s, ok := rawString(v.raw)
			if !ok || !durationValue.MatchString(s) {
				return FormField{}, fmt.Errorf("field %q has a duration that is not hh:mm", id)
			}
			if v.dst != nil {
				*v.dst = s
			} else {
				f.Default = s
			}
		}
	default:
		return FormField{}, fmt.Errorf("field %q has the unknown type %q", id, typ)
	}
	return f, nil
}

// formDefault is the default a choice or checklist keeps; one that names an option the field lacks is dropped.
func formDefault(typ string, raw json.RawMessage, options map[string]bool) any {
	if typ == "choice" {
		if s, ok := rawString(raw); ok && options[s] {
			return s
		}
		return nil
	}
	var items []string
	if json.Unmarshal(raw, &items) != nil || items == nil {
		return nil
	}
	for _, it := range items {
		if !options[it] {
			return nil
		}
	}
	return items
}

// parseRunAnswer reads an action.run answer: a message and, if it is safe, a link.
func parseRunAnswer(raw json.RawMessage) (map[string]string, error) {
	var res struct {
		Message json.RawMessage `json:"message"`
		URL     json.RawMessage `json:"url"`
	}
	if err := decodeInto(raw, &res); err != nil {
		return nil, fmt.Errorf("action.run: %w", err)
	}
	msg, _ := rawString(res.Message)
	if msg = cleanMsg(msg); msg == "" {
		return nil, errors.New("action.run: the answer has no message")
	}
	out := map[string]string{"message": msg}
	if u, _ := rawString(res.URL); safeURL(u) != "" {
		out["url"] = safeURL(u)
	}
	return out, nil
}

// parseRunValues checks the shape and size of a run's values: an object of strings or lists of strings.
func parseRunValues(raw json.RawMessage) (map[string]any, error) {
	values := map[string]any{}
	if len(raw) == 0 || string(raw) == "null" {
		return values, nil
	}
	var in map[string]any
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, errors.New("values must be an object")
	}
	if len(in) > maxRunValues {
		return nil, fmt.Errorf("values has %d fields, over the limit of %d", len(in), maxRunValues)
	}
	for k, v := range in {
		if utf8.RuneCountInString(k) > maxIDRunes {
			return nil, fmt.Errorf("a field id in values is over %d characters", maxIDRunes)
		}
		switch v := v.(type) {
		case string:
			if utf8.RuneCountInString(v) > maxRunValueRunes {
				return nil, fmt.Errorf("a value is over %d characters", maxRunValueRunes)
			}
		case []any:
			if len(v) > maxRunItems {
				return nil, fmt.Errorf("a list value has %d items, over the limit of %d", len(v), maxRunItems)
			}
			for _, it := range v {
				if s, ok := it.(string); !ok || utf8.RuneCountInString(s) > maxRunValueRunes {
					return nil, fmt.Errorf("a list value holds only strings of at most %d characters", maxRunValueRunes)
				}
			}
		default:
			return nil, errors.New("a value is a string or a list of strings")
		}
		values[k] = v
	}
	return values, nil
}

// rememberSteps keeps the module actions of a row's last good row.get; the row must still be on the page.
func (r *runner) rememberSteps(id string, d RowDetail) {
	var actions []Action
	for _, sec := range d.Sections {
		for _, st := range sec.Steps {
			actions = append(actions, st.Actions...)
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.rowLocked(id) == nil {
		return
	}
	if r.stepActions == nil {
		r.stepActions = map[string][]Action{}
	}
	r.stepActions[id] = actions
}

// r.mu must be held.
func (r *runner) rowLocked(id string) *Row {
	for _, g := range r.page.Groups {
		for i := range g.Rows {
			if g.Rows[i].ID == id {
				return &g.Rows[i]
			}
		}
	}
	return nil
}

// forgetStepsLocked drops the remembered steps of rows that left the page. r.mu must be held.
func (r *runner) forgetStepsLocked() {
	for id := range r.stepActions {
		if r.rowLocked(id) == nil {
			delete(r.stepActions, id)
		}
	}
}

// moduleAction is the first module action with this id on the row: its action, its actions, then its steps'.
func (r *runner) moduleAction(row, id string) (Action, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.noPage {
		return Action{}, errNoPage
	}
	rw := r.rowLocked(row)
	if rw == nil {
		return Action{}, errNoRow
	}
	var all []Action
	if rw.Action != nil {
		all = append(all, *rw.Action)
	}
	all = append(all, rw.Actions...)
	all = append(all, r.stepActions[row]...)
	if i := slices.IndexFunc(all, func(a Action) bool { return a.ID == id }); i >= 0 {
		return all[i], nil
	}
	return Action{}, errNoAction
}

// openRun takes the row's run slot, or says another run holds it.
func (r *runner) openRun(row string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.runs[row] {
		return false
	}
	if r.runs == nil {
		r.runs = map[string]bool{}
	}
	r.runs[row] = true
	return true
}

func (r *runner) closeRun(row string) {
	r.mu.Lock()
	delete(r.runs, row)
	r.mu.Unlock()
}

// moduleActionTarget runs the checks both routes share, in the order the docs give, and answers the live connection.
func (s *Service) moduleActionTarget(w http.ResponseWriter, r *http.Request, verb string) (*runner, conn, string, string, bool) {
	key, row, id := r.PathValue("key"), r.PathValue("id"), r.PathValue("action")
	rn := s.Sup.runner(key)
	if rn == nil {
		writeError(w, http.StatusNotFound, "source "+key+" not found")
		return nil, nil, "", "", false
	}
	a, err := rn.moduleAction(row, id)
	switch {
	case err == nil && a.URL != "":
		err = errActionLink
	case err == nil && verb == "form" && a.Confirm != "form":
		err = errNotAForm
	}
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return nil, nil, "", "", false
	}
	p, live := rn.live()
	if !live {
		writeError(w, http.StatusConflict, errSourceDown.Error())
		return nil, nil, "", "", false
	}
	rn.mu.Lock()
	inError := rn.pageStateLocked() == PageError
	rn.mu.Unlock()
	if inError {
		writeError(w, http.StatusConflict, errPageInErr.Error())
		return nil, nil, "", "", false
	}
	return rn, p, row, id, true
}

func (s *Service) serveActionForm(w http.ResponseWriter, r *http.Request) {
	_, p, row, id, ok := s.moduleActionTarget(w, r, "form")
	if !ok {
		return
	}
	res, err := p.call(r.Context(), "action.form", map[string]any{"row": row, "action": id}, callTimeout)
	if err != nil {
		callError(w, err)
		return
	}
	form, err := parseForm(res)
	if err != nil {
		writeError(w, http.StatusBadGateway, cleanMsg(err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, form)
}

func (s *Service) serveActionRun(w http.ResponseWriter, r *http.Request) {
	rn, p, row, id, ok := s.moduleActionTarget(w, r, "run")
	if !ok {
		return
	}
	if !rn.openRun(row) {
		writeError(w, http.StatusConflict, errRunOpen.Error())
		return
	}
	var body struct {
		Values json.RawMessage `json:"values"`
	}
	err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRunBytes)).Decode(&body)
	var values map[string]any
	if err == nil || errors.Is(err, io.EOF) {
		values, err = parseRunValues(body.Values)
	}
	if err != nil {
		rn.closeRun(row)
		writeError(w, http.StatusBadRequest, cleanMsg(err.Error()))
		return
	}
	// The run outlives a client that goes away, and so does its slot.
	res, err := p.call(context.WithoutCancel(r.Context()), "action.run", map[string]any{"row": row, "action": id, "values": values}, runTimeout)
	rn.closeRun(row)
	if err != nil {
		callError(w, err)
		return
	}
	ans, err := parseRunAnswer(res)
	if err != nil {
		writeError(w, http.StatusBadGateway, cleanMsg(err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, ans)
}
