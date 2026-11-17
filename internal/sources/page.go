package sources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
)

const (
	maxPageRows   = 5000
	maxPageGroups = 100
	maxChips      = 10
	maxActions    = 10
	maxRowDetail  = 1 << 20 // bytes of one row.get answer
	maxTitleRunes = 200
)

const (
	PageStarting = "starting"
	PageReady    = "ready"
)

var chipTones = []string{"neutral", "good", "warn", "muted"}

type Chip struct {
	Label string `json:"label"`
	Tone  string `json:"tone"`
}

// Action is carried as data; running it is not built yet.
type Action struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Confirm string `json:"confirm"`
	Summary string `json:"summary"`
	URL     string `json:"url,omitempty"`
}

type Row struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Subtitle string   `json:"subtitle"`
	Chips    []Chip   `json:"chips"`
	Reason   string   `json:"reason"`
	Action   *Action  `json:"action,omitempty"`
	Actions  []Action `json:"actions,omitempty"`
}

type Group struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Rows  []Row  `json:"rows"`
}

type Page struct {
	Groups  []Group `json:"groups"`
	Waiting int     `json:"waiting"`
}

// PageView is a source's page as served: the last good answer and whether there has been one.
type PageView struct {
	State   string  `json:"state"`
	Groups  []Group `json:"groups"`
	Waiting int     `json:"waiting"`
}

type Link struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

type Step struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Chips   []Chip   `json:"chips"`
	Actions []Action `json:"actions"`
}

type Field struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

type Section struct {
	Kind   string  `json:"kind"`
	Title  string  `json:"title"`
	Links  []Link  `json:"links,omitempty"`
	Steps  []Step  `json:"steps,omitempty"`
	Fields []Field `json:"fields,omitempty"`
}

type RowDetail struct {
	Sections []Section `json:"sections"`
}

// MessageKind is a message a module may ask Admiral to send AdmiralBot, declared in initialize.
type MessageKind struct {
	Kind  string `json:"kind"`
	Label string `json:"label"`
	Job   string `json:"job"`
}

func line(raw json.RawMessage) string {
	s, _ := rawString(raw)
	return truncate(oneLine(s), maxTitleRunes)
}

func parseChips(raw []json.RawMessage) ([]Chip, error) {
	if len(raw) > maxChips {
		return nil, fmt.Errorf("a row or step has %d chips, over the limit of %d", len(raw), maxChips)
	}
	chips := []Chip{}
	for _, c := range raw {
		var item struct {
			Label json.RawMessage `json:"label"`
			Tone  json.RawMessage `json:"tone"`
		}
		if json.Unmarshal(c, &item) != nil {
			continue
		}
		label := line(item.Label)
		if label == "" {
			continue
		}
		tone, _ := rawString(item.Tone)
		if !slices.Contains(chipTones, tone) {
			tone = "neutral"
		}
		chips = append(chips, Chip{Label: label, Tone: tone})
	}
	return chips, nil
}

// parseAction is nil for an action with no id or label, or whose url is not http(s).
func parseAction(raw json.RawMessage) *Action {
	var item struct {
		ID      json.RawMessage `json:"id"`
		Label   json.RawMessage `json:"label"`
		Confirm json.RawMessage `json:"confirm"`
		Summary json.RawMessage `json:"summary"`
		URL     json.RawMessage `json:"url"`
	}
	if json.Unmarshal(raw, &item) != nil {
		return nil
	}
	a := Action{ID: line(item.ID), Label: line(item.Label), Confirm: line(item.Confirm), Summary: cleanMsg(rawStr(item.Summary))}
	if a.ID == "" || a.Label == "" {
		return nil
	}
	if !slices.Contains([]string{"click", "yesno", "form"}, a.Confirm) {
		a.Confirm = "yesno" // an unknown kind asks first rather than running at once
	}
	if len(item.URL) > 0 && string(item.URL) != "null" {
		if a.URL = safeURL(rawStr(item.URL)); a.URL == "" {
			return nil
		}
	}
	return &a
}

func rawStr(raw json.RawMessage) string {
	s, _ := rawString(raw)
	return s
}

func parseActions(raw []json.RawMessage) ([]Action, error) {
	if len(raw) > maxActions {
		return nil, fmt.Errorf("a row or step has %d actions, over the limit of %d", len(raw), maxActions)
	}
	var out []Action
	for _, r := range raw {
		if a := parseAction(r); a != nil {
			out = append(out, *a)
		}
	}
	return out, nil
}

func parseRow(raw json.RawMessage) (Row, error) {
	var item struct {
		ID       json.RawMessage   `json:"id"`
		Title    json.RawMessage   `json:"title"`
		Subtitle json.RawMessage   `json:"subtitle"`
		Chips    []json.RawMessage `json:"chips"`
		Reason   json.RawMessage   `json:"reason"`
		Action   json.RawMessage   `json:"action"`
		Actions  []json.RawMessage `json:"actions"`
	}
	if err := decodeInto(raw, &item); err != nil {
		return Row{}, err
	}
	id, _ := rawString(item.ID)
	if id == "" || hasControl(id) {
		return Row{}, errors.New("a row has no id")
	}
	row := Row{ID: id, Title: line(item.Title), Subtitle: line(item.Subtitle), Reason: line(item.Reason)}
	var err error
	if row.Chips, err = parseChips(item.Chips); err != nil {
		return Row{}, err
	}
	if len(item.Action) > 0 && string(item.Action) != "null" {
		row.Action = parseAction(item.Action)
	}
	if row.Actions, err = parseActions(item.Actions); err != nil {
		return Row{}, err
	}
	return row, nil
}

func parsePage(raw json.RawMessage) (Page, error) {
	var res struct {
		Groups  []json.RawMessage `json:"groups"`
		Waiting json.RawMessage   `json:"waiting"`
	}
	if err := decodeInto(raw, &res); err != nil {
		return Page{}, fmt.Errorf("page.get: %w", err)
	}
	if len(res.Groups) > maxPageGroups {
		return Page{}, fmt.Errorf("page.get: %d groups, over the limit of %d", len(res.Groups), maxPageGroups)
	}
	page := Page{Groups: []Group{}, Waiting: positiveInt(res.Waiting)}
	total := 0
	for _, g := range res.Groups {
		var item struct {
			ID    json.RawMessage   `json:"id"`
			Title json.RawMessage   `json:"title"`
			Rows  []json.RawMessage `json:"rows"`
		}
		if err := decodeInto(g, &item); err != nil {
			return Page{}, fmt.Errorf("page.get: %w", err)
		}
		id, _ := rawString(item.ID)
		if id == "" || hasControl(id) {
			return Page{}, errors.New("page.get: a group has no id")
		}
		if total += len(item.Rows); total > maxPageRows {
			return Page{}, fmt.Errorf("page.get: over %d rows", maxPageRows)
		}
		group := Group{ID: id, Title: line(item.Title), Rows: []Row{}}
		for _, r := range item.Rows {
			row, err := parseRow(r)
			if err != nil {
				return Page{}, fmt.Errorf("page.get: %w", err)
			}
			group.Rows = append(group.Rows, row)
		}
		page.Groups = append(page.Groups, group)
	}
	return page, nil
}

func parseSection(raw json.RawMessage) (*Section, error) {
	var item struct {
		Kind   json.RawMessage   `json:"kind"`
		Title  json.RawMessage   `json:"title"`
		Links  []json.RawMessage `json:"links"`
		Steps  []json.RawMessage `json:"steps"`
		Fields []json.RawMessage `json:"fields"`
	}
	if err := decodeInto(raw, &item); err != nil {
		return nil, err
	}
	sec := Section{Kind: rawStr(item.Kind), Title: line(item.Title)}
	switch sec.Kind {
	case "links":
		for _, l := range item.Links {
			var it struct{ Label, URL json.RawMessage }
			if json.Unmarshal(l, &it) != nil {
				continue
			}
			if u := safeURL(rawStr(it.URL)); u != "" && line(it.Label) != "" {
				sec.Links = append(sec.Links, Link{Label: line(it.Label), URL: u})
			}
		}
	case "steps":
		for _, s := range item.Steps {
			var it struct {
				ID      json.RawMessage   `json:"id"`
				Title   json.RawMessage   `json:"title"`
				Chips   []json.RawMessage `json:"chips"`
				Actions []json.RawMessage `json:"actions"`
			}
			if json.Unmarshal(s, &it) != nil {
				continue
			}
			step := Step{ID: rawStr(it.ID), Title: line(it.Title)}
			if step.ID == "" {
				continue
			}
			var err error
			if step.Chips, err = parseChips(it.Chips); err != nil {
				return nil, err
			}
			if step.Actions, err = parseActions(it.Actions); err != nil {
				return nil, err
			}
			sec.Steps = append(sec.Steps, step)
		}
	case "fields":
		for _, f := range item.Fields {
			var it struct{ Label, Value json.RawMessage }
			if json.Unmarshal(f, &it) != nil {
				continue
			}
			if label := line(it.Label); label != "" {
				sec.Fields = append(sec.Fields, Field{Label: label, Value: rawStr(it.Value)})
			}
		}
	default:
		return nil, nil
	}
	return &sec, nil
}

func parseRowDetail(raw json.RawMessage) (RowDetail, error) {
	if len(raw) > maxRowDetail {
		return RowDetail{}, fmt.Errorf("row.get: %d bytes, over the limit of %d", len(raw), maxRowDetail)
	}
	var res struct {
		Sections []json.RawMessage `json:"sections"`
	}
	if err := decodeInto(raw, &res); err != nil {
		return RowDetail{}, fmt.Errorf("row.get: %w", err)
	}
	d := RowDetail{Sections: []Section{}}
	for _, s := range res.Sections {
		sec, err := parseSection(s)
		if err != nil {
			return RowDetail{}, fmt.Errorf("row.get: %w", err)
		}
		if sec != nil {
			d.Sections = append(d.Sections, *sec)
		}
	}
	return d, nil
}

// parseMessageKinds keeps each declaration with a kind, a label and a job; a bad one drops itself only.
func parseMessageKinds(raw json.RawMessage) []MessageKind {
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return nil
	}
	var kinds []MessageKind
	for _, it := range items {
		var d struct{ Kind, Label, Job json.RawMessage }
		if json.Unmarshal(it, &d) != nil {
			continue
		}
		k := MessageKind{Kind: line(d.Kind), Label: line(d.Label), Job: line(d.Job)}
		if k.Kind == "" || k.Label == "" || k.Job == "" || slices.ContainsFunc(kinds, func(o MessageKind) bool { return o.Kind == k.Kind }) {
			continue
		}
		kinds = append(kinds, k)
	}
	return kinds
}

// pullPage keeps the last good page: an answer that fails or is refused changes nothing.
func (r *runner) pullPage(ctx context.Context, p conn) {
	res, err := p.call(ctx, "page.get", map[string]any{}, callTimeout)
	if err != nil {
		r.logf("page.get failed: %v", err)
		return
	}
	page, err := parsePage(res)
	if err != nil {
		r.logf("protocol error: %v", err)
		return
	}
	r.mu.Lock()
	r.page, r.hasPage = page, true
	r.mu.Unlock()
	r.notify()
}

func (r *runner) hasPageCap() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Contains(r.caps, "page")
}

// Page is the page as it stands; false when the source declares none.
func (r *runner) Page() (PageView, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v := PageView{State: PageStarting, Groups: []Group{}}
	if r.hasPage {
		v = PageView{State: PageReady, Groups: r.page.Groups, Waiting: r.page.Waiting}
	}
	return v, true
}

func (r *runner) rowDetail(ctx context.Context, id string, include []string) (RowDetail, error) {
	p, ok := r.live()
	if !ok {
		return RowDetail{}, errors.New("the source is not running")
	}
	params := map[string]any{"id": id}
	if len(include) > 0 {
		params["include"] = include
	}
	res, err := p.call(ctx, "row.get", params, callTimeout)
	if err != nil {
		return RowDetail{}, err
	}
	return parseRowDetail(res)
}
