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
	maxPageRows    = 5000
	maxPageGroups  = 100
	maxChips       = 10
	maxActions     = 10
	maxRowDetail   = 1 << 20 // bytes of one row.get answer
	maxTitleRunes  = 200
	maxNotices     = 20
	maxNoticeRunes = 300
	maxIDRunes     = 200 // every id, and every include value on the row route
	maxInclude     = 10

	maxFormBytes     = 64 << 10 // bytes of one action.form answer
	maxFormFields    = 20
	maxFormOptions   = 100 // per field
	maxRunValues     = 50  // fields in one run's values
	maxRunItems      = 100 // items in one checklist value
	maxRunValueRunes = 1000
	maxRunURLBytes   = 2000    // a run answer's url
	maxRunBytes      = 1 << 20 // bytes of a run request's body

	maxMessageLinkBytes = 500 // a message action's link
	maxMessageRefRunes  = 40
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

var (
	chipTones   = []string{"neutral", "good", "warn", "muted"}
	noticeTones = []string{"info", "warn", "error"}
)

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
	// Builtin is "open", "launch" or "message" for an action Admiral handles itself, and empty for a module action.
	Builtin string `json:"builtin,omitempty"`
	// Kind, Ref, Title and Link are the fields of a message action.
	Kind  string `json:"kind,omitempty"`
	Ref   string `json:"ref,omitempty"`
	Title string `json:"title,omitempty"`
	Link  string `json:"link,omitempty"`
	// Step is the id of the step in the row's detail a module action acts on.
	Step string `json:"step,omitempty"`
	// Menu puts a module action in its group's overflow menu instead of a button.
	Menu bool `json:"menu,omitempty"`
}

type Row struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Subtitle string   `json:"subtitle"`
	URL      string   `json:"url,omitempty"`
	Chips    []Chip   `json:"chips"`
	Reason   string   `json:"reason"`
	Target   *Target  `json:"target,omitempty"`
	Action   *Action  `json:"action,omitempty"`
	Actions  []Action `json:"actions,omitempty"`
}

// Target names what a row's module actions act on, such as one step of its detail.
type Target struct {
	Label string `json:"label"`
	Chips []Chip `json:"chips"`
	Step  string `json:"step,omitempty"`
}

type Group struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Rows  []Row  `json:"rows"`
}

// Notice is a line a module says about itself, drawn above its groups.
type Notice struct {
	ID     string  `json:"id"`
	Text   string  `json:"text"`
	Tone   string  `json:"tone"`
	Link   *Link   `json:"link,omitempty"`
	Action *Action `json:"action,omitempty"`
}

type Page struct {
	Groups  []Group  `json:"groups"`
	Waiting int      `json:"waiting"`
	Notices []Notice `json:"notices"`
	Loading bool     `json:"-"`
}

// PageView is a source's page as served: the last good answer, or before one a loading answer's notices.
type PageView struct {
	State   string   `json:"state"`
	Groups  []Group  `json:"groups"`
	Waiting int      `json:"waiting"`
	Notices []Notice `json:"notices"`
	Auth    *Auth    `json:"auth,omitempty"`
	Error   string   `json:"error,omitempty"`
}

type Link struct {
	Label    string `json:"label"`
	URL      string `json:"url"`
	Subtitle string `json:"subtitle,omitempty"`
	Chips    []Chip `json:"chips,omitempty"`
}

type Step struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Subtitle string   `json:"subtitle,omitempty"`
	Chips    []Chip   `json:"chips"`
	Actions  []Action `json:"actions"`
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
func titleLine(raw json.RawMessage) string { return cappedLine(raw, maxTitleRunes) }

func cappedLine(raw json.RawMessage, runes int) string {
	s, _ := rawString(raw)
	return cleanLine(s, runes)
}

// invisible is a rune that no text sent on to a chat may hold: a control, a format character or a line or paragraph separator.
func invisible(r rune) bool {
	return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == '\u2028' || r == '\u2029'
}

func cleanLine(s string, runes int) string {
	s = strings.Map(func(r rune) rune {
		if invisible(r) {
			return -1
		}
		return r
	}, oneLine(s))
	return truncate(s, runes)
}

// parseID is raw as an id: a non-empty string with no control character, within the id cap.
func parseID(raw json.RawMessage) (string, bool) {
	id, ok := rawString(raw)
	if !ok || id == "" || hasControl(id) || len([]rune(id)) > maxIDRunes {
		return "", false
	}
	return id, true
}

func parseChips(raw []json.RawMessage, holder string) ([]Chip, error) {
	if len(raw) > maxChips {
		return nil, fmt.Errorf("%s has %d chips, over the limit of %d", holder, len(raw), maxChips)
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

// plainURL is whether a link holds no invisible rune and no whitespace of any script, which safeURL leaves in.
func plainURL(u string) bool {
	return strings.IndexFunc(u, func(r rune) bool { return invisible(r) || unicode.IsSpace(r) }) < 0
}

// messageRef is a reference as one short identifier: single spaces only, at most maxMessageRefRunes.
func messageRef(raw json.RawMessage) string {
	return truncate(strings.Join(strings.Fields(titleLine(raw)), " "), maxMessageRefRunes)
}

// keepDeclared is actions without the message actions whose kind the plugin did not declare.
func keepDeclared(actions []Action, kinds []MessageKind) []Action {
	return slices.DeleteFunc(actions, func(a Action) bool {
		return a.Builtin == "message" && !slices.ContainsFunc(kinds, func(k MessageKind) bool { return k.Kind == a.Kind })
	})
}

// dropUndeclared removes the message actions of page whose kind is not in kinds.
func dropUndeclared(page *Page, kinds []MessageKind) {
	for gi := range page.Groups {
		rows := page.Groups[gi].Rows
		for ri := range rows {
			if a := rows[ri].Action; a != nil && len(keepDeclared([]Action{*a}, kinds)) == 0 {
				rows[ri].Action = nil
			}
			rows[ri].Actions = keepDeclared(rows[ri].Actions, kinds)
		}
	}
}

// parseAction is nil for an action with no id or label, a built-in action without its fields, or a url that is not http(s).
func parseAction(raw json.RawMessage) *Action {
	var item struct {
		ID      json.RawMessage `json:"id"`
		Label   json.RawMessage `json:"label"`
		Confirm json.RawMessage `json:"confirm"`
		Summary json.RawMessage `json:"summary"`
		URL     json.RawMessage `json:"url"`
		Builtin json.RawMessage `json:"builtin"`
		Kind    json.RawMessage `json:"kind"`
		Ref     json.RawMessage `json:"ref"`
		Title   json.RawMessage `json:"title"`
		Link    json.RawMessage `json:"link"`
		Step    json.RawMessage `json:"step"`
		Menu    json.RawMessage `json:"menu"`
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
		if a.URL = safeURL(u); a.URL == "" || !plainURL(a.URL) {
			return nil
		}
	}
	if len(item.Builtin) > 0 && string(item.Builtin) != "null" {
		var ok bool
		if a.Builtin, ok = rawString(item.Builtin); !ok {
			return nil
		}
	}
	if a.Builtin == "" && a.URL != "" {
		a.Builtin = "open"
	}
	switch a.Builtin {
	case "":
	case "open", "launch":
		if a.URL == "" {
			return nil
		}
	case "message":
		a.URL = ""
		a.Confirm = "yesno" // a message always asks first
		a.Kind, a.Ref, a.Title = titleLine(item.Kind), messageRef(item.Ref), titleLine(item.Title)
		link, _ := rawString(item.Link)
		if a.Link = safeURL(link); a.Link == "" || len(a.Link) > maxMessageLinkBytes || !plainURL(a.Link) || a.Kind == "" || a.Ref == "" || a.Title == "" {
			return nil
		}
	default:
		return nil
	}
	if a.Builtin == "" {
		a.Step, _ = parseID(item.Step)
		a.Menu = string(item.Menu) == "true"
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
		URL      json.RawMessage   `json:"url"`
		Chips    []json.RawMessage `json:"chips"`
		Reason   json.RawMessage   `json:"reason"`
		Target   json.RawMessage   `json:"target"`
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
	if row.Chips, err = parseChips(item.Chips, "a row"); err != nil {
		return Row{}, err
	}
	if u, _ := rawString(item.URL); safeURL(u) != "" && plainURL(safeURL(u)) {
		row.URL = safeURL(u)
	}
	if row.Target, err = parseTarget(item.Target); err != nil {
		return Row{}, err
	}
	if hasMain {
		if row.Action = parseAction(item.Action); row.Action != nil {
			row.Action.Menu = false // a row's main action is its one button
		}
	}
	if row.Actions, err = parseActions(item.Actions); err != nil {
		return Row{}, err
	}
	return row, nil
}

// parseTarget is nil for no target or one with no label; only too many chips refuse the page.
func parseTarget(raw json.RawMessage) (*Target, error) {
	var it struct {
		Label json.RawMessage   `json:"label"`
		Chips []json.RawMessage `json:"chips"`
		Step  json.RawMessage   `json:"step"`
	}
	if len(raw) == 0 || string(raw) == "null" || json.Unmarshal(raw, &it) != nil {
		return nil, nil
	}
	chips, err := parseChips(it.Chips, "a target")
	if err != nil {
		return nil, err
	}
	label := titleLine(it.Label)
	if label == "" {
		return nil, nil
	}
	step, _ := parseID(it.Step)
	return &Target{Label: label, Chips: chips, Step: step}, nil
}

// parseSectionLink is a link of a links section: parseLink plus its subtitle and chips; only too many chips refuse it.
func parseSectionLink(raw json.RawMessage) (*Link, bool, error) {
	var it struct {
		Subtitle json.RawMessage   `json:"subtitle"`
		Chips    []json.RawMessage `json:"chips"`
	}
	_ = json.Unmarshal(raw, &it)
	chips, err := parseChips(it.Chips, "a link")
	if err != nil {
		return nil, false, err
	}
	link, ok := parseLink(raw)
	if !ok {
		return nil, false, nil
	}
	link.Subtitle = titleLine(it.Subtitle)
	if len(chips) > 0 {
		link.Chips = chips
	}
	return link, true, nil
}

// parseLink is raw as a link; false for one with no label or a url that is not http(s).
func parseLink(raw json.RawMessage) (*Link, bool) {
	var it struct{ Label, URL json.RawMessage }
	if json.Unmarshal(raw, &it) != nil {
		return nil, false
	}
	s, _ := rawString(it.URL)
	if u, label := safeURL(s), titleLine(it.Label); u != "" && label != "" {
		return &Link{Label: label, URL: u}, true
	}
	return nil, false
}

// parseNotices keeps the notices in order; a notice with no usable id or a repeated one refuses the list, and one with no text or a bad link drops itself or its link.
func parseNotices(raw []json.RawMessage) ([]Notice, error) {
	if len(raw) > maxNotices {
		return nil, fmt.Errorf("%d notices, over the limit of %d", len(raw), maxNotices)
	}
	out := []Notice{}
	seen := map[string]bool{}
	for _, n := range raw {
		var item struct {
			ID     json.RawMessage `json:"id"`
			Text   json.RawMessage `json:"text"`
			Tone   json.RawMessage `json:"tone"`
			Link   json.RawMessage `json:"link"`
			Action json.RawMessage `json:"action"`
		}
		if err := decodeInto(n, &item); err != nil {
			return nil, err
		}
		id, ok := parseID(item.ID)
		if !ok {
			return nil, errors.New("a notice has no usable id")
		}
		if seen[id] {
			return nil, fmt.Errorf("notice id %q is used twice", id)
		}
		seen[id] = true
		text := cappedLine(item.Text, maxNoticeRunes)
		if text == "" {
			continue
		}
		tone, _ := rawString(item.Tone)
		if !slices.Contains(noticeTones, tone) {
			tone = "info"
		}
		notice := Notice{ID: id, Text: text, Tone: tone}
		notice.Link, _ = parseLink(item.Link)
		// Built-in actions hang on a row; a notice keeps only a module action and links through its link.
		if a := parseAction(item.Action); a != nil && a.Builtin == "" {
			a.Step, a.Menu = "", false
			notice.Action = a
		}
		out = append(out, notice)
	}
	return out, nil
}

func parsePage(raw json.RawMessage) (Page, error) {
	var res struct {
		Groups  []json.RawMessage `json:"groups"`
		Waiting json.RawMessage   `json:"waiting"`
		Notices []json.RawMessage `json:"notices"`
		Loading bool              `json:"loading"`
	}
	if err := decodeInto(raw, &res); err != nil {
		return Page{}, fmt.Errorf("page.get: %w", err)
	}
	if len(res.Groups) > maxPageGroups {
		return Page{}, fmt.Errorf("page.get: %d groups, over the limit of %d", len(res.Groups), maxPageGroups)
	}
	notices, err := parseNotices(res.Notices)
	if err != nil {
		return Page{}, fmt.Errorf("page.get: %w", err)
	}
	page := Page{Groups: []Group{}, Waiting: positiveInt(res.Waiting), Notices: notices, Loading: res.Loading}
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
			link, ok, err := parseSectionLink(l)
			if err != nil {
				return nil, err
			}
			if ok {
				sec.Links = append(sec.Links, *link)
			}
		}
	case "steps":
		for _, s := range item.Steps {
			var it struct {
				ID       json.RawMessage   `json:"id"`
				Title    json.RawMessage   `json:"title"`
				Subtitle json.RawMessage   `json:"subtitle"`
				Chips    []json.RawMessage `json:"chips"`
				Actions  []json.RawMessage `json:"actions"`
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
			step := Step{ID: id, Title: titleLine(it.Title), Subtitle: titleLine(it.Subtitle)}
			var err error
			if step.Chips, err = parseChips(it.Chips, "a step"); err != nil {
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

// fetchPage asks for the page and stores a good answer; an RPC error sets the page error, and a refusal or timeout only logs.
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
	dropUndeclared(&page, r.kinds)
	switch {
	case !page.Loading:
		r.page, r.hasPage = page, true
		r.forgetStepsLocked()
	case !r.hasPage:
		// Only its notices show until the first answer without loading.
		r.page = Page{Groups: []Group{}, Notices: page.Notices}
	}
	r.pageErr = ""
	r.mu.Unlock()
	return true
}

// pullPage refetches the page; an RPC error marks it error and keeps its groups, and a refusal or timeout only logs.
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
	v := PageView{State: r.pageStateLocked(), Groups: []Group{}, Notices: []Notice{}}
	if r.hasPage {
		v.Groups, v.Waiting = r.page.Groups, r.page.Waiting
	}
	if r.page.Notices != nil {
		v.Notices = r.page.Notices
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

// pageIDs reads the rows a page.changed names; ids that fail the id rules are dropped, and none left, or a list past the row limit, means all rows.
func pageIDs(params json.RawMessage) []string {
	var p struct {
		Rows json.RawMessage `json:"rows"`
	}
	var items []json.RawMessage
	if json.Unmarshal(params, &p) != nil || json.Unmarshal(p.Rows, &items) != nil || len(items) > maxPageRows {
		return nil
	}
	var ids []string
	seen := map[string]bool{}
	for _, it := range items {
		if id, ok := parseID(it); ok && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
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
			if len(r.pulls.pageRows) < maxPageRows || r.pulls.pageRows[id] {
				r.pulls.pageRows[id] = true
			} else {
				r.pulls.pageAll = true
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
	r.mu.Lock()
	declared := slices.Contains(r.caps, "page")
	r.mu.Unlock()
	if !declared {
		return
	}
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
	d, err := parseRowDetail(res)
	if err == nil {
		r.mu.Lock()
		kinds := r.kinds
		r.mu.Unlock()
		for i := range d.Sections {
			for j := range d.Sections[i].Steps {
				d.Sections[i].Steps[j].Actions = keepDeclared(d.Sections[i].Steps[j].Actions, kinds)
			}
		}
		r.rememberSteps(id, d)
	}
	return d, err
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
