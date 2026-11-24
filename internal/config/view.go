package config

import "github.com/jbuffin/admiral/internal/standing"

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
	ColorIndex    *int                `json:"color_index,omitempty"`
	SearchRoots   []string            `json:"search_roots"`
	Owners        []string            `json:"owners"`
	WakeReviewers []string            `json:"wake_reviewers"`
	CloneRoot     string              `json:"clone_root"` // expanded like SearchRoots; empty when unset
	CanLaunch     bool                `json:"can_launch"`
	Docks         []DockView          `json:"docks"`
	Chat          ChatView            `json:"chat"`
	Notify        bool                `json:"notify"`
	Preset        string              `json:"preset"`
	Limits        standing.Limits     `json:"limits"`
	Sentences     []standing.Sentence `json:"sentences"`
	// Overridden lists the Limits this fleet stores, as the field paths the sentences carry; the rest read from the preset.
	Overridden  []string        `json:"overridden"`
	CrewSharing CrewSharingView `json:"crew_sharing"`
}

type CrewSharingView struct {
	Enabled   bool     `json:"enabled"`
	ShareRepo string   `json:"share_repo"`
	Sources   []string `json:"sources"`
}

type DockView struct {
	Harness         string   `json:"harness"`
	Executable      string   `json:"executable"`
	ExecutableFound bool     `json:"executable_found"`
	Warnings        []string `json:"warnings"`
	// Models is the dock's list, listed by its harness or built in; ModelsSource says which.
	Models       []string `json:"models"`
	ModelsSource string   `json:"models_source"`
}

type ErrorView struct {
	File    string `json:"file"`
	Message string `json:"message"`
}

func (m *Manager) View() View {
	m.mu.Lock()
	v := m.viewLocked()
	namer := m.RoutineNamer
	m.mu.Unlock()
	// the routines store takes its own lock and calls back into the manager, so it is read with mu released
	if namer != nil {
		for i := range v.Fleets {
			f := &v.Fleets[i]
			if hasScoped(f.Limits.Skip) {
				f.Sentences = standing.SentencesNamed(f.Limits, namer(f.Key))
			}
		}
	}
	return v
}

func hasScoped(s standing.Skip) bool {
	for _, list := range [][]standing.Scoped{s.Labels, s.Senders, s.DedupeKeys} {
		for _, e := range list {
			if e.Routine != "" {
				return true
			}
		}
	}
	return false
}

func (m *Manager) viewLocked() View {
	v := View{Type: "config", Fleets: []FleetView{}, FavoriteSkills: m.cur.FavoriteSkills}
	for _, f := range m.cur.Fleets {
		opts := m.cur.ChatHarnessOptions(f)
		off := m.cur.chatOffReason(f, opts)
		fv := FleetView{Key: f.Key, Name: f.Name, Color: f.Color, Ink: f.Ink, ColorIndex: f.ColorIndex,
			SearchRoots: f.SearchRoots, Owners: nonNil(f.Owners), WakeReviewers: nonNil(f.WakeReviewers), CloneRoot: f.CloneRoot,
			CanLaunch: f.CanLaunch(), Notify: f.Notify, Docks: []DockView{},
			Chat:   ChatView{f.Chat.Harness, f.Chat.Model, f.Chat.Effort, f.Chat.ThresholdJSON(), off == "", off, offered(opts), opts, chatModels(m.cur, f), ChatEffortsFor(f.Chat.Harness)},
			Preset: string(f.Preset), Limits: f.Limits.Clone(), Sentences: standing.Sentences(f.Limits), Overridden: nonNil(f.Overridden),
			CrewSharing: CrewSharingView{f.CrewSharing.Enabled, f.CrewSharing.ShareRepo, nonNil(f.CrewSharing.Sources)}}
		for _, h := range f.Harnesses {
			warnings := h.Warnings
			if warnings == nil {
				warnings = []string{}
			}
			models, listed := m.cur.ModelsFor(f.Key, h.Name)
			source := "built_in"
			if listed {
				source = "listed"
			}
			fv.Docks = append(fv.Docks, DockView{h.Name, h.Executable, h.Found, warnings, models, source})
		}
		v.Fleets = append(v.Fleets, fv)
	}
	if m.errMsg != "" {
		v.Error = &ErrorView{File: m.path, Message: m.errMsg}
	}
	return v
}

func chatModels(c *Config, f Fleet) []string {
	models, _ := c.ModelsFor(f.Key, f.Chat.Harness)
	return models
}
