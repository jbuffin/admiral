package config

// View is what the UI receives: the body of GET /api/config and of the
// WebSocket `config` message. It never carries env or args, which may hold
// secrets.
type View struct {
	Type           string      `json:"type"`
	Fleets         []FleetView `json:"fleets"`
	FavoriteSkills []string    `json:"favorite_skills"`
	Error          *ErrorView  `json:"error"` // the banner; null when the file is fine
}

type FleetView struct {
	Key         string     `json:"key"`
	Name        string     `json:"name"`
	Color       string     `json:"color"`
	Ink         string     `json:"ink"`
	SearchRoots []string   `json:"search_roots"`
	CanLaunch   bool       `json:"can_launch"`
	Docks       []DockView `json:"docks"`
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

// View reports the current state.
func (m *Manager) View() View {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.viewLocked()
}

func (m *Manager) viewLocked() View {
	v := View{Type: "config", Fleets: []FleetView{}, FavoriteSkills: m.cur.FavoriteSkills}
	for _, f := range m.cur.Fleets {
		fv := FleetView{Key: f.Key, Name: f.Name, Color: f.Color, Ink: f.Ink,
			SearchRoots: f.SearchRoots, CanLaunch: f.CanLaunch(), Docks: []DockView{}}
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
