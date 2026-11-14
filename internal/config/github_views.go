package config

import (
	"encoding/json"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// GitHubBuiltinViews are the ids of the GitHub source's built-in views, in default order.
var GitHubBuiltinViews = []string{"for-you", "assigned-to-me", "review-requested", "my-prs"}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// ViewSlug is a view title as a view id.
func ViewSlug(title string) string {
	if s := strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(title), "-"), "-"); s != "" {
		return s
	}
	return "panel"
}

func builtinEntry(id string) json.RawMessage {
	return json.RawMessage(`{"id":"` + id + `","hidden":false}`)
}

func builtinEntries() []json.RawMessage {
	out := make([]json.RawMessage, 0, len(GitHubBuiltinViews))
	for _, id := range GitHubBuiltinViews {
		out = append(out, builtinEntry(id))
	}
	return out
}

// viewEntryBuiltinID is the built-in id a views entry names, else "".
func viewEntryBuiltinID(e json.RawMessage) string {
	var v struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(e, &v) == nil && slices.Contains(GitHubBuiltinViews, v.ID) {
		return v.ID
	}
	return ""
}

// WithGitHubBuiltinViews is a GitHub source's views list as it is stored: a list
// with no built-in entry gets all four in front, a partial one gets the missing ones
// after the entries present, and a complete one is returned as it is.
func WithGitHubBuiltinViews(views []json.RawMessage) []json.RawMessage {
	have := map[string]bool{}
	for _, e := range views {
		if id := viewEntryBuiltinID(e); id != "" {
			have[id] = true
		}
	}
	if len(have) == len(GitHubBuiltinViews) {
		return views
	}
	var missing []json.RawMessage
	for _, id := range GitHubBuiltinViews {
		if !have[id] {
			missing = append(missing, builtinEntry(id))
		}
	}
	if len(have) == 0 {
		return append(missing, views...)
	}
	return append(slices.Clone(views), missing...)
}

// WithGitHubBuiltinViewsJSON is WithGitHubBuiltinViews on a raw list; anything but
// a list reads as an empty one.
func WithGitHubBuiltinViewsJSON(raw json.RawMessage) json.RawMessage {
	var list []json.RawMessage
	_ = json.Unmarshal(raw, &list)
	out, _ := json.Marshal(WithGitHubBuiltinViews(list))
	return out
}

// foldPanelOrder puts the ids panel_order names, in its order, into the positions
// the views it names hold; the rest keep their positions. It reorders the entries.
func foldPanelOrder(views []json.RawMessage, order []string) []json.RawMessage {
	ids := make([]string, len(views))
	used := map[string]bool{}
	for _, id := range GitHubBuiltinViews {
		used[id] = true
	}
	for i, e := range views {
		var v struct {
			ID, Title, Query string
		}
		if json.Unmarshal(e, &v) != nil {
			continue
		}
		switch {
		case viewEntryBuiltinID(e) != "":
			ids[i] = v.ID
		case v.ID != "":
		default:
			title, query := strings.TrimSpace(v.Title), strings.TrimSpace(v.Query)
			if title == "" || query == "" {
				continue
			}
			base := ViewSlug(title)
			id := base
			for n := 2; used[id]; n++ {
				id = base + "-" + strconv.Itoa(n)
			}
			used[id] = true
			ids[i] = id
		}
	}
	var named []string
	var positions []int
	for _, id := range order {
		if i := slices.Index(ids, id); id != "" && i >= 0 && !slices.Contains(named, id) {
			named = append(named, id)
			positions = append(positions, i)
		}
	}
	slices.Sort(positions)
	out := slices.Clone(views)
	for k, pos := range positions {
		out[pos] = views[slices.Index(ids, named[k])]
	}
	return out
}

// migrateGitHubViews gives each GitHub source's views list its built-in entries, folds
// the source's panel_order into the list and drops it.
func migrateGitHubViews(sources *Object) (changed bool) {
	for _, key := range sources.Keys() {
		raw, _ := sources.Get(key)
		src, err := ParseObject(raw)
		if err != nil || stringOf(src, "plugin") != "github" {
			continue
		}
		settings := NewObject()
		if rs, ok := src.Get("settings"); ok {
			if settings, err = ParseObject(rs); err != nil {
				continue
			}
		}
		rawViews, ok := settings.Get("views")
		if !ok {
			continue
		}
		var views []json.RawMessage
		if json.Unmarshal(rawViews, &views) != nil {
			continue
		}
		complete := true
		for _, id := range GitHubBuiltinViews {
			if !slices.ContainsFunc(views, func(e json.RawMessage) bool { return viewEntryBuiltinID(e) == id }) {
				complete = false
			}
		}
		if complete {
			continue
		}
		views = WithGitHubBuiltinViews(views)
		if rawOrder, ok := src.Get("panel_order"); ok {
			var order []string
			if json.Unmarshal(rawOrder, &order) == nil {
				views = foldPanelOrder(views, order)
			}
			src.Delete("panel_order")
		}
		out, _ := json.Marshal(views)
		settings.Set("views", out)
		src.Set("settings", settings.JSON())
		sources.Set(key, src.JSON())
		changed = true
	}
	return changed
}

// ReorderGitHubViews is a GitHub source's settings with its views list reordered by
// the ids order names, as panel_order once ordered them.
func ReorderGitHubViews(settings json.RawMessage, order []string) json.RawMessage {
	obj, err := ParseObject(settings)
	if err != nil {
		return settings
	}
	var views []json.RawMessage
	if raw, ok := obj.Get("views"); !ok || json.Unmarshal(raw, &views) != nil {
		return settings
	}
	out, _ := json.Marshal(foldPanelOrder(views, order))
	obj.Set("views", out)
	return obj.JSON()
}
