package sources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode"
)

const (
	maxPageRows   = 5000
	maxPageGroups = 100
	maxChips      = 10
	maxActions    = 10
	maxRowDetail  = 1 << 20 // bytes of one row.get answer
	maxTitleRunes = 200
	maxIDRunes    = 200 // every id, and every include value on the row route
	maxInclude    = 10
)

var (
	errNoPage = errors.New("The source has no page")
	errNoRow  = errors.New("The page has no such row")
)

const (
	PageStarting     = "starting"
	PageReady        = "ready"
	PageAuthRequired = "auth_required"
	PageError        = "error"
)

var chipTones = []string{"neutral", "good", "warn", "muted"}

type Chip struct {
	Label string `json:"label"`
	Tone  string `json:"tone"`
}

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
	Auth    *Auth   `json:"auth,omitempty"`
	Error   string  `json:"error,omitempty"`
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

// titleLine is raw cut to its first line, without invisible characters, and capped.
func titleLine(raw json.RawMessage) string {
	s, _ := rawString(raw)
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, oneLine(s))
	return truncate(s, maxTitleRunes)
}

// parseID is raw as an id: a non-empty string with no control character, within the id cap.
func parseID(raw json.RawMessage) (string, bool) {
	id, ok := rawString(raw)
	if !ok || id == "" || hasControl(id) || len([]rune(id)) > maxIDRunes {
		return "", false
	}
	return id, true
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
		label := titleLine(item.Label)
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
	summary, _ := rawString(item.Summary)
	a := Action{ID: titleLine(item.ID), Label: titleLine(item.Label), Confirm: titleLine(item.Confirm), Summary: cleanMsg(summary)}
	if a.ID == "" || a.Label == "" {
		return nil
	}
	if !slices.Contains([]string{"click", "yesno", "form"}, a.Confirm) {
		a.Confirm = "yesno" // an unknown kind asks first rather than running at once
	}
	if len(item.URL) > 0 && string(item.URL) != "null" {
		u, _ := rawString(item.URL)
		if a.URL = safeURL(u); a.URL == "" {
			return nil
		}
	}
	return &a
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
	id, ok := parseID(item.ID)
	if !ok {
		return Row{}, errors.New("a row has no usable id")
	}
	hasMain := len(item.Action) > 0 && string(item.Action) != "null"
	if n := len(item.Actions) + btoi(hasMain); n > maxActions {
		return Row{}, fmt.Errorf("a row has %d actions, over the limit of %d", n, maxActions)
	}
	row := Row{ID: id, Title: titleLine(item.Title), Subtitle: titleLine(item.Subtitle), Reason: titleLine(item.Reason)}
	var err error
	if row.Chips, err = parseChips(item.Chips); err != nil {
		return Row{}, err
	}
	if hasMain {
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
	groupIDs, rowIDs := map[string]bool{}, map[string]bool{}
	for _, g := range res.Groups {
		var item struct {
			ID    json.RawMessage   `json:"id"`
			Title json.RawMessage   `json:"title"`
			Rows  []json.RawMessage `json:"rows"`
		}
		if err := decodeInto(g, &item); err != nil {
			return Page{}, fmt.Errorf("page.get: %w", err)
		}
		id, ok := parseID(item.ID)
		if !ok {
			return Page{}, errors.New("page.get: a group has no usable id")
		}
		if groupIDs[id] {
			return Page{}, fmt.Errorf("page.get: group id %q is used twice", id)
		}
		groupIDs[id] = true
		if total += len(item.Rows); total > maxPageRows {
			return Page{}, fmt.Errorf("page.get: over %d rows", maxPageRows)
		}
		group := Group{ID: id, Title: titleLine(item.Title), Rows: []Row{}}
		for _, r := range item.Rows {
			row, err := parseRow(r)
			if err != nil {
				return Page{}, fmt.Errorf("page.get: %w", err)
			}
			if rowIDs[row.ID] {
				return Page{}, fmt.Errorf("page.get: row id %q is used twice", row.ID)
			}
			rowIDs[row.ID] = true
			group.Rows = append(group.Rows, row)
		}
		page.Groups = append(page.Groups, group)
	}
	return page, nil
}

func parseSection(raw json.RawMessage, stepIDs map[string]bool) (*Section, error) {
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
	kind, _ := rawString(item.Kind)
	sec := Section{Kind: kind, Title: titleLine(item.Title)}
	switch sec.Kind {
	case "links":
		for _, l := range item.Links {
			var it struct{ Label, URL json.RawMessage }
			if json.Unmarshal(l, &it) != nil {
				continue
			}
			raw, _ := rawString(it.URL)
			if u := safeURL(raw); u != "" && titleLine(it.Label) != "" {
				sec.Links = append(sec.Links, Link{Label: titleLine(it.Label), URL: u})
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
			id, ok := parseID(it.ID)
			if !ok {
				continue
			}
			if stepIDs[id] {
				return nil, fmt.Errorf("step id %q is used twice", id)
			}
			stepIDs[id] = true
			step := Step{ID: id, Title: titleLine(it.Title)}
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
			value, _ := rawString(it.Value)
			if label := titleLine(it.Label); label != "" && !hasTextControl(value) {
				sec.Fields = append(sec.Fields, Field{Label: label, Value: value})
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
	stepIDs := map[string]bool{}
	for _, s := range res.Sections {
		sec, err := parseSection(s, stepIDs)
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
		k := MessageKind{Kind: titleLine(d.Kind), Label: titleLine(d.Label), Job: titleLine(d.Job)}
		if k.Kind == "" || k.Label == "" || k.Job == "" || slices.ContainsFunc(kinds, func(o MessageKind) bool { return o.Kind == k.Kind }) {
			continue
		}
		kinds = append(kinds, k)
	}
	return kinds
}

// fetchPage asks for the page and stores the answer; it reports whether the page's view changed. A refused answer only logs and keeps the last good page.
func (r *runner) fetchPage(ctx context.Context, p conn) bool {
	res, err := p.call(ctx, "page.get", map[string]any{}, callTimeout)
	if err != nil {
		r.logf("page.get failed: %v", err)
		var rpc *rpcError
		if !errors.As(err, &rpc) {
			return false
		}
		msg := cleanMsg(rpc.Error())
		if msg == "" {
			msg = "The page couldn't be fetched."
		}
		r.mu.Lock()
		r.pageErr = msg
		r.mu.Unlock()
		return true
	}
	page, err := parsePage(res)
	if err != nil {
		r.logf("protocol error: %v", err)
		return false
	}
	r.mu.Lock()
	r.page, r.hasPage, r.pageErr = page, true, ""
	r.mu.Unlock()
	return true
}

// pullPage keeps the last good page: an answer that fails or is refused changes nothing.
func (r *runner) pullPage(ctx context.Context, p conn) {
	if r.fetchPage(ctx, p) {
		r.pageChanged(nil)
	}
}

// pageChanged moves the page's version on; rows are the ids a client need refetch, none meaning all of them.
func (r *runner) pageChanged(rows []string) {
	r.mu.Lock()
	r.bumpPageLocked(rows)
	r.mu.Unlock()
	r.notify()
}

// r.mu must be held.
func (r *runner) bumpPageLocked(rows []string) {
	r.pageVer++
	r.pageRows = rows
	r.pageState = r.pageStateLocked()
}

// r.mu must be held.
func (r *runner) pageStateLocked() string {
	switch {
	case r.state == StateAuthRequired:
		return PageAuthRequired
	case r.state == StateError, r.pageErr != "":
		return PageError
	case r.hasPage:
		return PageReady
	}
	return PageStarting
}

// Page is the page as it stands; false once the source is known to have none.
func (r *runner) Page() (PageView, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.noPage {
		return PageView{}, false
	}
	v := PageView{State: r.pageStateLocked(), Groups: []Group{}}
	if r.hasPage {
		v.Groups, v.Waiting = r.page.Groups, r.page.Waiting
	}
	switch v.State {
	case PageAuthRequired:
		if r.auth != nil {
			a := *r.auth
			v.Auth = &a
		}
	case PageError:
		v.Error = r.errMsg
		if r.state != StateError {
			v.Error = r.pageErr
		}
	}
	return v, true
}

// pageIDs reads the rows a page.changed names; ids that fail the id rules are dropped, and none left means all rows.
func pageIDs(params json.RawMessage) []string {
	var p struct {
		Rows json.RawMessage `json:"rows"`
	}
	var items []json.RawMessage
	if json.Unmarshal(params, &p) != nil || json.Unmarshal(p.Rows, &items) != nil {
		return nil
	}
	var ids []string
	for _, it := range items {
		if id, ok := parseID(it); ok && !slices.Contains(ids, id) {
			if ids = append(ids, id); len(ids) == maxPageRows {
				break
			}
		}
	}
	return ids
}

// wantPage queues a page.get; rows are what a page.changed named, and none means every row.
func (r *runner) wantPage(changed bool, rows []string) {
	r.mu.Lock()
	r.pulls.page = true
	r.pulls.pageChanged = r.pulls.pageChanged || changed
	if changed && len(rows) > 0 {
		if r.pulls.pageRows == nil {
			r.pulls.pageRows = map[string]bool{}
		}
		for _, id := range rows {
			if len(r.pulls.pageRows) < maxPageRows {
				r.pulls.pageRows[id] = true
			}
		}
	} else {
		r.pulls.pageAll = true
	}
	r.mu.Unlock()
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *runner) pullPageQueued(ctx context.Context, p conn, todo pulls) {
	moved := r.fetchPage(ctx, p)
	if !moved && !todo.pageChanged {
		return
	}
	var rows []string
	if !todo.pageAll && todo.pageChanged {
		rows = slices.Sorted(maps.Keys(todo.pageRows))
	}
	r.pageChanged(rows)
}

// rowDetail asks the plugin for a row of the last good page; an id from anywhere else never reaches it.
func (r *runner) rowDetail(ctx context.Context, id string, include []string) (RowDetail, error) {
	r.mu.Lock()
	noPage, known := r.noPage, false
	for _, g := range r.page.Groups {
		known = known || slices.ContainsFunc(g.Rows, func(row Row) bool { return row.ID == id })
	}
	r.mu.Unlock()
	switch {
	case noPage:
		return RowDetail{}, errNoPage
	case !known:
		return RowDetail{}, errNoRow
	}
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

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

// PageLive is a source's entry for its page in the `sources` message; a client refetches when Version moves.
type PageLive struct {
	State   string   `json:"state"`
	Waiting int      `json:"waiting"`
	Version int      `json:"version"`
	Rows    []string `json:"rows,omitempty"`
}

// PageLive is the live block of a source that declares a page; false for one that does not.
func (r *runner) PageLive() (PageLive, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.pageLiveLocked()
}

// A state change the page was not told about, such as a crash, moves the version here, where a client reads it.
func (r *runner) pageLiveLocked() (PageLive, bool) {
	if !slices.Contains(r.caps, "page") {
		return PageLive{}, false
	}
	if st := r.pageStateLocked(); r.pageState == "" {
		r.pageState = st
	} else if st != r.pageState {
		r.bumpPageLocked(nil)
	}
	return PageLive{State: r.pageState, Waiting: r.page.Waiting, Version: r.pageVer, Rows: slices.Clone(r.pageRows)}, true
}
