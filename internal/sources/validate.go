package sources

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/jbuffin/admiral/internal/repofinder"
)

// What Admiral accepts from a plugin. Everything a plugin sends is untrusted.
const (
	MaxOrders    = 5000
	MaxOrderText = 12000 // bytes
	maxPanels    = 100
	maxMessage   = 2000 // runes of an auth message
)

// Order is one order as the UI receives it.
type Order struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Text  string `json:"text"`
	URL   string `json:"url"`
	Hot   bool   `json:"hot"`
	Repo  string `json:"repo"`
}

// Panel is one list of orders in a source.
type Panel struct {
	ID     string  `json:"id"`
	Title  string  `json:"title"`
	Orders []Order `json:"orders"`
}

// Auth is what a plugin asks the engineer to do to sign in.
type Auth struct {
	Message string `json:"message"`
	URL     string `json:"url"`
	Code    string `json:"code"`
}

func isControl(r rune) bool { return r < 0x20 || r == 0x7f }

func hasControl(s string) bool { return strings.IndexFunc(s, isControl) >= 0 }

// oneLine makes a title a single trimmed line: its first non-blank line, with
// any other control character turned into a space.
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

// safeURL keeps an http or https URL and drops anything else.
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

func decodeInto(raw json.RawMessage, v any) error {
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("unexpected shape: %v", err)
	}
	return nil
}

// parsePanels checks a panels.list answer.
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

// parseOrders checks an orders.list answer. One bad order makes the whole list
// a protocol error, so a plugin cannot get half a list shown.
func parseOrders(raw json.RawMessage) ([]Order, error) {
	var items []struct {
		ID    *string `json:"id"`
		Title *string `json:"title"`
		Text  *string `json:"text"`
		URL   *string `json:"url"`
		Hot   *bool   `json:"hot"`
		Repo  *string `json:"repo"`
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
			if strings.IndexFunc(*it.Text, func(r rune) bool { return isControl(r) && r != '\n' && r != '\t' && r != '\r' }) >= 0 {
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
		o.Hot = it.Hot != nil && *it.Hot
		out = append(out, o)
	}
	return out, nil
}

// parseAuth checks the params of auth.required.
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
