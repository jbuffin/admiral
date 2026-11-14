package sources

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
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
		Author    *string         `json:"author"`
		OpenedAt  *string         `json:"opened_at"`
		Excerpt   *string         `json:"excerpt"`
		// A field that fails its check is dropped and the order kept.
		ReviewRequestedAt *string         `json:"review_requested_at"`
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
		o.PR = parseOrderPR(it.PR)
		if it.Author != nil && !hasControl(*it.Author) && utf8.RuneCountInString(*it.Author) <= maxAuthor && strings.TrimSpace(*it.Author) != "" {
			o.Author = *it.Author
		}
		o.OpenedAt = rfc3339(it.OpenedAt)
		o.ReviewRequestedAt = rfc3339(it.ReviewRequestedAt)
		if it.Excerpt != nil {
			o.Excerpt = github.Excerpt(*it.Excerpt)
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

// rfc3339 is s as a UTC RFC 3339 time, or "" when s is not one.
func rfc3339(s *string) string {
	if s == nil {
		return ""
	}
	at, err := time.Parse(time.RFC3339, *s)
	if err != nil {
		return ""
	}
	return at.UTC().Format(time.RFC3339Nano)
}

// parseOrderPR reads a plugin's pr object; anything but a well-formed object is dropped, and a bad failing_check_count
// only costs that count.
func parseOrderPR(raw json.RawMessage) *OrderPR {
	if len(raw) == 0 || raw[0] != '{' {
		return nil
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return nil
	}
	count := positiveInt(fields["failing_check_count"])
	delete(fields, "failing_check_count")
	rest, _ := json.Marshal(fields)
	var pr OrderPR
	if json.Unmarshal(rest, &pr) != nil {
		return nil
	}
	pr.FailingCheckCount = count
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
