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

// ViewEntry is the part of a stored views entry that gives it an id.
type ViewEntry struct{ ID, Title, Query string }

// ViewEntryIDs is the id each views entry is served under, "" for one that is no view: a built-in
// entry repeats one already seen, or a custom entry lacks a title or a query. A known built-in id
// names the built-in; any other entry with a title and a query is a custom view, whatever id it
// carries, and takes the slug of its title with -2, -3 ... after the ids taken, built-ins included.
func ViewEntryIDs(entries []ViewEntry) []string {
	ids := make([]string, len(entries))
	used := map[string]bool{}
	for _, id := range GitHubBuiltinViews {
		used[id] = true
	}
	seen := map[string]bool{}
	for i, e := range entries {
		if slices.Contains(GitHubBuiltinViews, e.ID) {
			if !seen[e.ID] {
				seen[e.ID], ids[i] = true, e.ID
			}
			continue
		}
		title, query := strings.TrimSpace(e.Title), strings.TrimSpace(e.Query)
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
	return ids
}

func rawViewEntries(views []json.RawMessage) []ViewEntry {
	out := make([]ViewEntry, len(views))
	for i, e := range views {
		var v struct{ ID, Title, Query string }
		_ = json.Unmarshal(e, &v)
		out[i] = ViewEntry(v)
	}
	return out
}

// oldViewIDs maps the id a custom view had before the built-ins joined the list (the slug of its
// title, numbered among the custom views only) to the id it has now, for the ones that moved.
func oldViewIDs(views []json.RawMessage) map[string]string {
	entries := rawViewEntries(views)
	now := ViewEntryIDs(entries)
	moved := map[string]string{}
	used := map[string]bool{}
	for i, e := range entries {
		title, query := strings.TrimSpace(e.Title), strings.TrimSpace(e.Query)
		if title == "" || query == "" || slices.Contains(GitHubBuiltinViews, e.ID) {
			continue
		}
		base := ViewSlug(title)
		id := base
		for n := 2; used[id]; n++ {
			id = base + "-" + strconv.Itoa(n)
		}
		used[id] = true
		if id != now[i] {
			moved[id] = now[i]
		}
	}
	return moved
}

// foldPanelOrder puts the ids panel_order names, in its order, into the positions
// the views it names hold; the rest keep their positions. It reorders the entries.
func foldPanelOrder(views []json.RawMessage, order []string) []json.RawMessage {
	ids := ViewEntryIDs(rawViewEntries(views))
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

// ViewIDMove says a custom view of a GitHub source changed id when the built-in views joined its list.
type ViewIDMove struct{ Fleet, Source, From, To string }

// migrateGitHubViews gives each GitHub source's views list its built-in entries, folds
// the source's panel_order into the list and drops it. It returns the keys of the sources it
// changed and the custom views whose ids moved, which panel_order is read through.
func migrateGitHubViews(sources *Object) (migrated []string, moves []ViewIDMove) {
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
		renamed := oldViewIDs(views)
		for from, to := range renamed {
			moves = append(moves, ViewIDMove{Fleet: stringOf(src, "fleet"), Source: key, From: from, To: to})
		}
		views = WithGitHubBuiltinViews(views)
		if rawOrder, ok := src.Get("panel_order"); ok {
			var order []string
			if json.Unmarshal(rawOrder, &order) == nil {
				for i, id := range order {
					if to, ok := renamed[id]; ok {
						order[i] = to
					}
				}
				views = foldPanelOrder(views, order)
			}
			src.Delete("panel_order")
		}
		out, _ := json.Marshal(views)
		settings.Set("views", out)
		src.Set("settings", settings.JSON())
		sources.Set(key, src.JSON())
		migrated = append(migrated, key)
	}
	return migrated, moves
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
