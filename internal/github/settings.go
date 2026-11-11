package github

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

// ParseSettings reads settings decoded from JSON (map[string]any). The
// custom views list is under the key "panels", as in the plugin.
func ParseSettings(m map[string]any) Settings { return Settings{} }
