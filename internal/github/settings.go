package github

import (
	"math"
	"regexp"
	"strconv"
	"strings"
)

// View is one list of items: one GitHub search.
type View struct {
	ID    string
	Title string
	Query string
}

// Settings is a fleet's GitHub source settings, read the way the plugin read
// them.
type Settings struct {
	Host           string   // trimmed; DefaultHost when blank
	Account        string   // trimmed; "" for the host's active account
	Views          []View   // the three defaults, then the custom views
	HotLabels      []string // lowercase; the defaults when none are given
	RefreshMinutes int      // 1..1440, default 5
}

const (
	defaultRefreshMinutes = 5
	maxRefreshMinutes     = 24 * 60
)

// defaultViews are always there, ahead of the custom views.
var defaultViews = []View{
	{"assigned-to-me", "Assigned to me", "is:open assignee:@me"},
	{"review-requested", "Review requested", "is:open is:pr review-requested:@me"},
	{"my-prs", "My PRs", "is:open is:pr author:@me"},
}

// ParseSettings reads settings decoded from JSON (map[string]any). The custom
// views list is under the key "views". An entry without a title or a query is
// skipped; a custom view with a default's id replaces that default in place.
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

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// slug is a title as a view id.
func slug(title string) string {
	if s := strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(title), "-"), "-"); s != "" {
		return s
	}
	return "panel"
}

func readViews(v any) []View {
	views := append([]View(nil), defaultViews...)
	list, _ := v.([]any)
	used := map[string]bool{}
	for _, e := range list {
		entry, _ := e.(map[string]any)
		title, _ := entry["title"].(string)
		query, _ := entry["query"].(string)
		title, query = strings.TrimSpace(title), strings.TrimSpace(query)
		if title == "" || query == "" {
			continue
		}
		base := slug(title)
		id := base
		for n := 2; used[id]; n++ {
			id = base + "-" + strconv.Itoa(n)
		}
		used[id] = true
		view := View{id, title, query}
		replaced := false
		for i, d := range views[:len(defaultViews)] {
			if d.ID == id {
				views[i], replaced = view, true
			}
		}
		if !replaced {
			views = append(views, view)
		}
	}
	return views
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
// numbers arrive as float64.
func readMinutes(v any) int {
	f, ok := v.(float64)
	if !ok || f < 1 || f != math.Trunc(f) {
		return defaultRefreshMinutes
	}
	return int(math.Min(f, maxRefreshMinutes))
}
