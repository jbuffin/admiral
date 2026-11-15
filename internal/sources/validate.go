package sources

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jbuffin/admiral/internal/github"
	"github.com/jbuffin/admiral/internal/repofinder"
)

// Everything a plugin sends is untrusted.
const (
	MaxOrders    = 5000
	MaxOrderText = 12000 // bytes
	maxPanels    = 100
	maxMessage   = 2000 // runes of an auth message
)

type Order struct {
	ID     string   `json:"id"`
	Title  string   `json:"title"`
	Text   string   `json:"text"`
	URL    string   `json:"url"`
	Hot    bool     `json:"hot"`
	Repo   string   `json:"repo"`
	Labels []string `json:"labels,omitempty"`
	// SubIssues is the count of sub-issues an issue has; 0 when none or unknown.
	SubIssues int `json:"sub_issues,omitempty"`
	// PR is a pull request's state, as a ship's prs[] entry gives it; nil for any other order.
	PR *OrderPR `json:"pr,omitempty"`
	// Parent is an issue's GitHub parent, matched to an order by id; never set on a pull request.
	Parent *OrderParent `json:"parent,omitempty"`
	// Stack is a pull request's GitHub stack; never set on an issue.
	Stack *OrderStack `json:"stack,omitempty"`
	// Author is who opened it, OpenedAt when (RFC 3339) and Excerpt the start of its body as one line.
	Author   string `json:"author,omitempty"`
	OpenedAt string `json:"opened_at,omitempty"`
	Excerpt  string `json:"excerpt,omitempty"`
	// ReviewRequestedAt is when the viewer was last asked to review a pull request. ReviewedSincePush says their latest
	// submitted review is on its head; nil when unknown.
	ReviewRequestedAt string `json:"review_requested_at,omitempty"`
	ReviewedSincePush *bool  `json:"reviewed_since_push,omitempty"`
}

// OrderPR is moor.PR without its url, which is the order's own.
type OrderPR struct {
	State     string `json:"state"`
	Review    string `json:"review"`
	Checks    string `json:"checks"`
	Mergeable string `json:"mergeable"`
	Conflicts bool   `json:"conflicts"`
	Ready     bool   `json:"ready"`
	// FailingCheckCount is how many checks fail; left out when none.
	FailingCheckCount int `json:"failing_check_count,omitempty"`
}

// OrderParent is an issue order's GitHub parent; ID is in the form of an order's own id.
type OrderParent struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	URL   string `json:"url"`
	Repo  string `json:"repo,omitempty"`
}

// OrderStack is the GitHub stack a pull request order sits in; Position is its layer, ascending from the bottom, and
// Size is GitHub's own count of layers.
type OrderStack struct {
	Number   int    `json:"number"`
	URL      string `json:"url"`
	Position int    `json:"position"`
	Size     int    `json:"size"`
}

type Panel struct {
	ID     string  `json:"id"`
	Title  string  `json:"title"`
	Orders []Order `json:"orders"`
	// Admiral sets Error and RefreshedAt, never the plugin. RefreshedAt is the time of the view's last successful answer.
	Error       string    `json:"error,omitempty"`
	RefreshedAt time.Time `json:"refreshed_at,omitzero"`
}

type Auth struct {
	Message string `json:"message"`
	URL     string `json:"url"`
	Code    string `json:"code"`
}

func isControl(r rune) bool { return r < 0x20 || r == 0x7f }

func hasControl(s string) bool { return strings.IndexFunc(s, isControl) >= 0 }

// Line breaks, tabs and the CR of pasted CRLF text are allowed.
func hasTextControl(s string) bool {
	return strings.IndexFunc(s, func(r rune) bool { return isControl(r) && r != '\n' && r != '\t' && r != '\r' }) >= 0
}

const maxErrorRunes = 300

func cleanMsg(s string) string { return truncate(oneLine(s), maxErrorRunes) }

func oneLine(s string) string {
	for _, line := range strings.Split(strings.ReplaceAll(s, "\r", "\n"), "\n") {
		line = strings.Map(func(r rune) rune {
			if isControl(r) {
				return ' '
			}
			return r
		}, line)
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

func safeURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || hasControl(raw) || strings.ContainsAny(raw, " \\") {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	return raw
}

func positiveInt(raw json.RawMessage) int {
	var n int
	if json.Unmarshal(raw, &n) != nil || n < 0 {
		return 0
	}
	return n
}

func decodeInto(raw json.RawMessage, v any) error {
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("unexpected shape: %v", err)
	}
	return nil
}

func parsePanels(raw json.RawMessage) ([]Panel, error) {
	var items []struct {
		ID    *string `json:"id"`
		Title *string `json:"title"`
	}
	if err := decodeInto(raw, &items); err != nil {
		return nil, fmt.Errorf("panels.list: %w", err)
	}
	if len(items) > maxPanels {
		return nil, fmt.Errorf("panels.list: %d panels, at most %d allowed", len(items), maxPanels)
	}
	seen := map[string]bool{}
	out := make([]Panel, 0, len(items))
	for i, it := range items {
		if it.ID == nil || *it.ID == "" {
			return nil, fmt.Errorf("panels.list: panel %d has no id", i)
		}
		if hasControl(*it.ID) {
			return nil, fmt.Errorf("panels.list: panel id %q holds a control character", *it.ID)
		}
		if seen[*it.ID] {
			return nil, fmt.Errorf("panels.list: panel id %q is listed twice", *it.ID)
		}
		seen[*it.ID] = true
		title := *it.ID
		if it.Title != nil && oneLine(*it.Title) != "" {
			title = oneLine(*it.Title)
		}
		out = append(out, Panel{ID: *it.ID, Title: title, Orders: []Order{}})
	}
	return out, nil
}

// One bad order makes the whole list a protocol error, so a plugin cannot get
// half a list shown.
func parseOrders(raw json.RawMessage) ([]Order, error) {
	var items []struct {
		ID     *string  `json:"id"`
		Title  *string  `json:"title"`
		Text   *string  `json:"text"`
		URL    *string  `json:"url"`
		Hot    *bool    `json:"hot"`
		Repo   *string  `json:"repo"`
		Labels []string `json:"labels"`
		// Sub-issues is the count a GitHub issue has, read by the built-in source only.
		SubIssues json.RawMessage `json:"sub_issues"` // anything but a positive integer is dropped
		PR        json.RawMessage `json:"pr"`         // anything but a well-formed object is dropped
		Parent    json.RawMessage `json:"parent"`     // a bad parent is dropped and the order kept
		Stack     json.RawMessage `json:"stack"`      // a bad GitHub stack is dropped and the order kept
		// A field of the wrong type or that fails its check is dropped and the order kept.
		Author            json.RawMessage `json:"author"`
		OpenedAt          json.RawMessage `json:"opened_at"`
		Excerpt           json.RawMessage `json:"excerpt"`
		ReviewRequestedAt json.RawMessage `json:"review_requested_at"`
		ReviewedSincePush json.RawMessage `json:"reviewed_since_push"`
	}
	if err := decodeInto(raw, &items); err != nil {
		return nil, fmt.Errorf("orders.list: %w", err)
	}
	if len(items) > MaxOrders {
		return nil, fmt.Errorf("orders.list: %d orders, at most %d allowed", len(items), MaxOrders)
	}
	seen := map[string]bool{}
	out := make([]Order, 0, len(items))
	for i, it := range items {
		if it.ID == nil || *it.ID == "" {
			return nil, fmt.Errorf("orders.list: order %d has no id", i)
		}
		id := *it.ID
		if hasControl(id) {
			return nil, fmt.Errorf("orders.list: order id %q holds a control character", id)
		}
		if seen[id] {
			return nil, fmt.Errorf("orders.list: order id %q is listed twice", id)
		}
		seen[id] = true
		o := Order{ID: id}
		if it.Text != nil {
			if len(*it.Text) > MaxOrderText {
				return nil, fmt.Errorf("orders.list: the text of order %q is %d bytes, at most %d allowed", id, len(*it.Text), MaxOrderText)
			}
			if hasTextControl(*it.Text) {
				return nil, fmt.Errorf("orders.list: the text of order %q holds a control character", id)
			}
			o.Text = *it.Text
		}
		if it.Title != nil {
			o.Title = oneLine(*it.Title)
		}
		if o.Title == "" {
			if o.Title = oneLine(o.Text); o.Title == "" {
				o.Title = id
			}
		}
		if it.URL != nil {
			o.URL = safeURL(*it.URL)
		}
		if it.Repo != nil {
			if _, ok := repofinder.Identity(*it.Repo); ok {
				o.Repo = strings.TrimSpace(*it.Repo)
			}
		}
		for _, l := range it.Labels {
			if l != "" && !hasControl(l) {
				o.Labels = append(o.Labels, l)
			}
		}
		o.Hot = it.Hot != nil && *it.Hot
		if n := positiveInt(it.SubIssues); n > 0 {
			o.SubIssues = n
		}
		if o.PR = parseOrderPR(it.PR); o.PR == nil {
			o.Parent = parseOrderParent(it.Parent)
		} else {
			o.Stack = parseOrderStack(it.Stack)
		}
		if a, ok := rawString(it.Author); ok && !hasControl(a) && !hasInvisible(a) && utf8.RuneCountInString(a) <= maxAuthor && strings.TrimSpace(a) != "" {
			o.Author = a
		}
		o.OpenedAt = rfc3339(it.OpenedAt)
		o.ReviewRequestedAt = rfc3339(it.ReviewRequestedAt)
		if e, ok := rawString(it.Excerpt); ok {
			o.Excerpt = github.Excerpt(e)
		}
		var seen bool
		if json.Unmarshal(it.ReviewedSincePush, &seen) == nil && len(it.ReviewedSincePush) > 0 && it.ReviewedSincePush[0] != 'n' {
			o.ReviewedSincePush = &seen
		}
		out = append(out, o)
	}
	return out, nil
}

const maxAuthor = 100

// rawString is raw as a JSON string; false for any other type, null or nothing.
func rawString(raw json.RawMessage) (string, bool) {
	var s string
	if len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &s) != nil {
		return "", false
	}
	return s, true
}

// hasInvisible reports a C1 control or a Unicode format character (zero-width, bidi override or isolate, BOM), which
// show nothing or reorder what is around them.
func hasInvisible(s string) bool {
	return strings.IndexFunc(s, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }) >= 0
}

// rfc3339 is raw as a UTC RFC 3339 time, or "" when it is not a string holding one.
func rfc3339(raw json.RawMessage) string {
	s, ok := rawString(raw)
	if !ok {
		return ""
	}
	at, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return ""
	}
	return at.UTC().Format(time.RFC3339Nano)
}

// parseOrderParent reads a parent object; one that isn't well-formed is dropped, and a bad repo costs only the repo.
func parseOrderParent(raw json.RawMessage) *OrderParent {
	if len(raw) == 0 || raw[0] != '{' {
		return nil
	}
	var in struct {
		ID    *string         `json:"id"`
		Title json.RawMessage `json:"title"`
		URL   *string         `json:"url"`
		Repo  json.RawMessage `json:"repo"`
	}
	if json.Unmarshal(raw, &in) != nil || in.ID == nil || *in.ID == "" || hasControl(*in.ID) || in.URL == nil {
		return nil
	}
	p := &OrderParent{ID: *in.ID, URL: safeURL(*in.URL)}
	if p.URL == "" {
		return nil
	}
	if t, ok := rawString(in.Title); ok {
		p.Title = oneLine(t)
	}
	if p.Title == "" {
		p.Title = p.ID
	}
	if repo, ok := rawString(in.Repo); ok {
		if _, ok := repofinder.Identity(repo); ok {
			p.Repo = strings.TrimSpace(repo)
		}
	}
	return p
}

// parseOrderStack reads a GitHub stack object; one that isn't well-formed is dropped.
func parseOrderStack(raw json.RawMessage) *OrderStack {
	if len(raw) == 0 || raw[0] != '{' {
		return nil
	}
	var in struct {
		Number   *int    `json:"number"`
		URL      *string `json:"url"`
		Position *int    `json:"position"`
		Size     *int    `json:"size"`
	}
	if json.Unmarshal(raw, &in) != nil || in.Number == nil || in.URL == nil || in.Position == nil || in.Size == nil ||
		*in.Number < 1 || *in.Position < 0 || *in.Size < 1 {
		return nil
	}
	u := safeURL(*in.URL)
	if u == "" {
		return nil
	}
	return &OrderStack{Number: *in.Number, URL: u, Position: *in.Position, Size: *in.Size}
}

// parseOrderPR reads a plugin's pr object; anything but a well-formed object is dropped, and a bad failing_check_count
// only costs that count.
func parseOrderPR(raw json.RawMessage) *OrderPR {
	if len(raw) == 0 || raw[0] != '{' {
		return nil
	}
	// The outer field shadows OrderPR's own, so a bad count costs only the count, whatever its key's case.
	var in struct {
		OrderPR
		FailingCheckCount json.RawMessage `json:"failing_check_count"`
	}
	if json.Unmarshal(raw, &in) != nil {
		return nil
	}
	pr := in.OrderPR
	pr.FailingCheckCount = positiveInt(in.FailingCheckCount)
	return &pr
}

func parseAuth(raw json.RawMessage) (*Auth, error) {
	var p struct {
		Message *string `json:"message"`
		URL     *string `json:"url"`
		Code    *string `json:"code"`
	}
	if err := decodeInto(raw, &p); err != nil {
		return nil, fmt.Errorf("auth.required: %w", err)
	}
	if p.Message == nil || oneLine(*p.Message) == "" {
		return nil, fmt.Errorf("auth.required: message is required")
	}
	a := &Auth{Message: truncate(oneLine(*p.Message), maxMessage)}
	if p.URL != nil {
		a.URL = safeURL(*p.URL)
	}
	if p.Code != nil {
		a.Code = truncate(oneLine(*p.Code), 200)
	}
	return a, nil
}

func truncate(s string, runes int) string {
	if r := []rune(s); len(r) > runes {
		return string(r[:runes])
	}
	return s
}
