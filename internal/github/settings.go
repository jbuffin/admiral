package github

import (
	"encoding/json"
	"github.com/jbuffin/admiral/internal/config"
	"math"
	"strconv"
	"strings"
)

// View is one list of items: one GitHub search.
type View struct {
	ID      string
	Title   string
	Query   string // empty for For you, which is no search
	Hidden  bool
	Builtin bool
}

// Settings is a fleet's GitHub source settings, read the way the plugin read
// them.
type Settings struct {
	Host           string   // trimmed; DefaultHost when blank
	Account        string   // trimmed; "" for the host's active account
	Views          []View   // the stored list in tab order, built-ins included
	HotLabels      []string // lowercase; the defaults when none are given
	RefreshMinutes int      // 1..1440, default 5
}

// SearchViews are the views that are panels: visible, and a search.
func (s Settings) SearchViews() []View {
	var out []View
	for _, v := range s.Views {
		if !v.Hidden && v.Query != "" {
			out = append(out, v)
		}
	}
	return out
}

const (
	defaultRefreshMinutes = 5
	maxRefreshMinutes     = 24 * 60
)

// defaultViews are the built-in views, in their default order.
var defaultViews = []View{
	{ID: "for-you", Title: "For you", Builtin: true},
	{ID: "assigned-to-me", Title: "Assigned to me", Query: "is:open assignee:@me", Builtin: true},
	{ID: "review-requested", Title: "Review requested", Query: "is:open is:pr review-requested:@me", Builtin: true},
	{ID: "my-prs", Title: "My PRs", Query: "is:open is:pr author:@me", Builtin: true},
}

// ParseSettings reads settings decoded from JSON (map[string]any). The views
// list is under the key "views": built-in entries {id, hidden, title?} and
// custom entries {title, query}. A custom entry without a title or a query is
// skipped, and so is a built-in entry with an unknown id.
func ParseSettings(m map[string]any) Settings {
	return Settings{
		Host:           textSetting(m["host"], DefaultHost),
		Account:        textSetting(m["account"], ""),
		Views:          readViews(m["views"]),
		HotLabels:      readHotLabels(m["hotLabels"]),
		RefreshMinutes: readMinutes(m["refreshMinutes"]),
	}
}

func textSetting(v any, def string) string {
	if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
		return strings.TrimSpace(s)
	}
	return def
}

func readViews(v any) []View {
	list, _ := v.([]any)
	entries := make([]config.ViewEntry, len(list))
	for i, e := range list {
		m, _ := e.(map[string]any)
		entries[i].ID, _ = m["id"].(string)
		entries[i].Title, _ = m["title"].(string)
		entries[i].Query, _ = m["query"].(string)
	}
	ids := config.ViewEntryIDs(entries)
	builtins := map[string]View{}
	for _, d := range defaultViews {
		builtins[d.ID] = d
	}
	var views []View
	seen := map[string]bool{}
	for i, e := range list {
		id := ids[i]
		if id == "" {
			continue
		}
		entry, _ := e.(map[string]any)
		hidden, _ := entry["hidden"].(bool)
		title := strings.TrimSpace(entries[i].Title)
		if d, ok := builtins[id]; ok {
			seen[id] = true
			if title != "" {
				d.Title = title
			}
			d.Hidden = hidden
			views = append(views, d)
			continue
		}
		views = append(views, View{ID: id, Title: title, Query: strings.TrimSpace(entries[i].Query), Hidden: hidden})
	}
	// A built-in the list lacks comes back visible after the entries present.
	var missing []View
	for _, d := range defaultViews {
		if !seen[d.ID] {
			missing = append(missing, d)
		}
	}
	if len(missing) == len(defaultViews) {
		// An old list holds only custom views: the built-ins come first.
		return append(missing, views...)
	}
	return append(views, missing...)
}

func readHotLabels(v any) []string {
	var labels []string
	list, _ := v.([]any)
	for _, e := range list {
		if s, ok := e.(string); ok && strings.TrimSpace(s) != "" {
			labels = append(labels, strings.ToLower(strings.TrimSpace(s)))
		}
	}
	if len(labels) == 0 {
		for _, l := range defaultHotLabels {
			labels = append(labels, strings.ToLower(l))
		}
	}
	return labels
}

// readMinutes takes a whole number of 1 or more, clamped to a day. JSON
// numbers arrive as float64 or json.Number.
func readMinutes(v any) int {
	f, ok := v.(float64)
	if n, isNum := v.(json.Number); isNum { // the server decodes with UseNumber
		f, ok = parseNumber(n)
	}
	if !ok || f < 1 || f != math.Trunc(f) {
		return defaultRefreshMinutes
	}
	return int(math.Min(f, maxRefreshMinutes))
}

func parseNumber(n json.Number) (float64, bool) {
	f, err := strconv.ParseFloat(string(n), 64)
	return f, err == nil
}
