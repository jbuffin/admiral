package config

// View never carries env or args, which may hold secrets.
type View struct {
	Type           string      `json:"type"`
	Fleets         []FleetView `json:"fleets"`
	FavoriteSkills []string    `json:"favorite_skills"`
	Error          *ErrorView  `json:"error"` // the banner; null when the file is fine
}

// ChatView is a fleet's chat settings with their defaults filled in; Effort is empty for the
// harness default and ContextThreshold is a token count or "never".
type ChatView struct {
	Harness          string   `json:"harness"`
	Model            string   `json:"model"`
	Effort           string   `json:"effort"`
	ContextThreshold any      `json:"context_threshold"`
	On               bool     `json:"on"`
	OffReason        string   `json:"off_reason"`
	Harnesses        []string `json:"harnesses"`
	// HarnessOptions has one entry per candidate harness the fleet has a dock for.
	HarnessOptions []ChatHarnessOption `json:"harness_options"`
	// Models and Efforts are the lists for the fleet's chat harness.
	Models  []string `json:"models"`
	Efforts []string `json:"efforts"`
}

type FleetView struct {
	Key   string `json:"key"`
	Name  string `json:"name"`
	Color string `json:"color"`
	Ink   string `json:"ink"`
	// ColorIndex places the fleet in a color sequence; the client wraps it.
	// Absent for a custom color.
	ColorIndex    *int       `json:"color_index,omitempty"`
	SearchRoots   []string   `json:"search_roots"`
	Owners        []string   `json:"owners"`
	WakeReviewers []string   `json:"wake_reviewers"`
	CloneRoot     string     `json:"clone_root"` // expanded like SearchRoots; empty when unset
	CanLaunch     bool       `json:"can_launch"`
	Docks         []DockView `json:"docks"`
	Chat          ChatView   `json:"chat"`
	Notify        bool       `json:"notify"`
}

type DockView struct {
	Harness         string   `json:"harness"`
	Executable      string   `json:"executable"`
	ExecutableFound bool     `json:"executable_found"`
	Warnings        []string `json:"warnings"`
}

type ErrorView struct {
	File    string `json:"file"`
	Message string `json:"message"`
}

func (m *Manager) View() View {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.viewLocked()
}

func (m *Manager) viewLocked() View {
	v := View{Type: "config", Fleets: []FleetView{}, FavoriteSkills: m.cur.FavoriteSkills}
	for _, f := range m.cur.Fleets {
		fv := FleetView{Key: f.Key, Name: f.Name, Color: f.Color, Ink: f.Ink, ColorIndex: f.ColorIndex,
			SearchRoots: f.SearchRoots, Owners: nonNil(f.Owners), WakeReviewers: nonNil(f.WakeReviewers), CloneRoot: f.CloneRoot,
			CanLaunch: f.CanLaunch(), Notify: f.Notify, Docks: []DockView{},
			Chat: ChatView{f.Chat.Harness, f.Chat.Model, f.Chat.Effort, f.Chat.ThresholdJSON(), m.cur.ChatOffReason(f) == "", m.cur.ChatOffReason(f), m.cur.ChatHarnessesOffered(f), m.cur.ChatHarnessOptions(f), ChatModelsFor(f.Chat.Harness), ChatEffortsFor(f.Chat.Harness)}}
		for _, h := range f.Harnesses {
			warnings := h.Warnings
			if warnings == nil {
				warnings = []string{}
			}
			fv.Docks = append(fv.Docks, DockView{h.Name, h.Executable, h.Found, warnings})
		}
		v.Fleets = append(v.Fleets, fv)
	}
	if m.errMsg != "" {
		v.Error = &ErrorView{File: m.path, Message: m.errMsg}
	}
	return v
}
