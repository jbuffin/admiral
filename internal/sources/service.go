package sources

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/jbuffin/admiral/internal/actor"
	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/github"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/server"
)

// Service's GET handlers never change anything.
type Service struct {
	Home   string
	Config *config.Manager
	Sup    *Supervisor
	Hub    *server.Hub
	// MergeGuard wraps the stack route with its token check; with none the route refuses everything.
	MergeGuard func(http.Handler) http.Handler

	linkMu  sync.Mutex      // guards linking
	linking map[string]bool // sources with a Stack on GitHub under way

	mu     sync.Mutex // one config read-modify-write at a time
	poke   chan struct{}
	pokeMu sync.Once
}

type sourceState struct {
	def       config.Source
	state     string
	readOnly  bool
	errMsg    string
	auth      *Auth
	caps      []string
	panels    []Panel
	warnings  []string
	hasSchema bool
	viewer    string
	conn      *connState
	// refreshing is a built-in GitHub source's cycle in flight; a plugin never sets it.
	refreshing bool
}

func (r *runner) snapshot() sourceState {
	r.mu.Lock()
	defer r.mu.Unlock()
	st := sourceState{def: r.def, state: r.state, readOnly: r.readOnly, errMsg: r.errMsg, caps: slices.Clone(r.caps), hasSchema: r.schema != nil}
	if g, ok := r.p.(*githubConn); ok {
		st.viewer = g.Viewer()
		st.refreshing = g.Refreshing()
	}
	if r.auth != nil {
		a := *r.auth
		st.auth = &a
	}
	st.warnings = slices.Clone(r.warnings)
	if r.conn != nil {
		c := *r.conn
		st.conn = &c
	}
	st.panels = make([]Panel, len(r.panels))
	for i, p := range r.panels {
		p.Orders = slices.Clone(p.Orders)
		st.panels[i] = p
	}
	return st
}

type SourceView struct {
	Key          string   `json:"key"`
	Name         string   `json:"name"`
	Plugin       string   `json:"plugin"`
	Fleet        string   `json:"fleet"`
	IconURL      string   `json:"icon_url"`
	Capabilities []string `json:"capabilities"`
	HasSchema    bool     `json:"has_schema"`
	State        string   `json:"state"`
	ReadOnly     bool     `json:"read_only"`
	Error        *string  `json:"error"`
	Auth         *Auth    `json:"auth"`
	// Viewer is the signed-in GitHub login of a built-in GitHub source, omitted for a plugin and before its first answer.
	Viewer string `json:"viewer,omitempty"`
	// Refreshing is true while the source pulls orders, asked for or on its timer; always false for a plugin.
	Refreshing bool `json:"refreshing"`
	// Warnings are the router's notes for the source's fleet; [] when none.
	Warnings []string `json:"warnings"`
	Views    []Panel  `json:"views"`
	// Connection is a GitHub source's sign-in status; omitted for any other source.
	Connection *Connection `json:"connection,omitempty"`
	// Tabs are a GitHub source's visible views in stored order, For you included; omitted for any other source.
	Tabs *[]Tab `json:"tabs,omitempty"`
	// Events is the source's event catalog, omitted until it has one.
	Events []EventKind `json:"events,omitempty"`
}

// Tab is one tab of the Orders screen a GitHub source draws.
type Tab struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// githubTabs lists the views the Orders tabs show: the visible ones, in the stored order, under their effective titles.
func githubTabs(raw json.RawMessage) *[]Tab {
	tabs := []Tab{}
	for _, v := range githubSettings(raw).Views {
		if !v.Hidden {
			tabs = append(tabs, Tab{ID: v.ID, Title: v.Title})
		}
	}
	return &tabs
}

// View is every source in config order, with every order its panels return; a ship on an order doesn't hide it.
func (s *Service) View() []SourceView {
	out := []SourceView{}
	for _, st := range s.Sup.snapshot() {
		v := SourceView{Key: st.def.Key, Name: st.def.Name, Plugin: st.def.Plugin, Fleet: st.def.Fleet, Capabilities: st.caps, HasSchema: st.hasSchema,
			State: st.state, ReadOnly: st.readOnly, Auth: st.auth, Viewer: st.viewer, Refreshing: st.refreshing, Views: []Panel{}, Warnings: []string{}}
		v.Warnings = append(v.Warnings, st.warnings...)
		v.Connection = connectionOf(st)
		if st.def.Plugin == builtinGitHub {
			if v.Name == "" {
				v.Name = "GitHub"
			}
			v.IconURL = iconURL(builtinGitHub)
			v.Tabs = githubTabs(st.def.Settings)
		} else if m, dir, err := LoadManifest(s.Home, st.def.Plugin); err == nil {
			if v.Name == "" {
				v.Name = m.Name
			}
			if iconPath(m, dir) != "" {
				v.IconURL = iconURL(m.Name)
			}
		}
		if v.Name == "" {
			v.Name = st.def.Key
		}
		if st.errMsg != "" {
			msg := st.errMsg
			v.Error = &msg
		}
		for _, p := range st.panels {
			v.Views = append(v.Views, p)
		}
		if st.def.Plugin != builtinGitHub { // a GitHub source's views list is its order
			SortPanels(v.Views, st.def.PanelOrder)
		}
		if kinds, ok := s.Sup.Events(st.def.Fleet, st.def.Key); ok {
			v.Events = kinds
		}
		out = append(out, v)
	}
	return out
}

// SortPanels puts the panels that order names first, in its order; the rest
// keep the plugin's order after them.
func SortPanels(panels []Panel, order []string) {
	if len(order) == 0 {
		return
	}
	rank := func(id string) int {
		if i := slices.Index(order, id); i >= 0 {
			return i
		}
		return len(order)
	}
	slices.SortStableFunc(panels, func(a, b Panel) int { return rank(a.ID) - rank(b.ID) })
}

func (s *Service) message() any {
	return map[string]any{"type": "sources", "sources": s.View()}
}

func (s *Service) Notify() {
	s.pokeMu.Do(func() { s.poke = make(chan struct{}, 1) })
	select {
	case s.poke <- struct{}{}:
	default:
	}
}

func (s *Service) Run(ctx context.Context) {
	s.Notify()
	for {
		select {
		case <-s.poke:
			s.Hub.BroadcastOnce("sources", s.message)
		case <-ctx.Done():
			return
		}
	}
}

func (s *Service) Connected(c *server.Client) { _ = c.SendOnce("sources", s.message) }

func (s *Service) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/sources", s.serveList)
	mux.HandleFunc("POST /api/sources", s.serveAdd)
	mux.HandleFunc("PUT /api/source-order", s.serveSourceOrder)
	mux.HandleFunc("PUT /api/sources/{key}/panel-order", s.servePanelOrder)
	mux.HandleFunc("PUT /api/sources/{key}", s.serveSave)
	mux.HandleFunc("DELETE /api/sources/{key}", s.serveDelete)
	mux.HandleFunc("POST /api/sources/{key}/restart", s.serveRestart)
	mux.HandleFunc("POST /api/sources/{key}/refresh", s.serveRefresh)
	mux.HandleFunc("POST /api/sources/{key}/connection/check", s.serveConnectionCheck)
	mux.HandleFunc("POST /api/sources/{key}/search-count", s.serveSearchCount)
	mux.HandleFunc("GET /api/sources/{key}/schema", s.serveSchema)
	mux.HandleFunc("GET /api/sources/{key}/page", s.servePage)
	mux.HandleFunc("POST /api/sources/{key}/page/refresh", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotImplemented, "not built yet")
	})
	mux.HandleFunc("GET /api/sources/{key}/rows/{id}", s.serveRow)
	mux.HandleFunc("GET /api/sources/{key}/settings", s.serveSettings)
	mux.HandleFunc("GET /api/sources/{key}/repos", s.serveRepos)
	mux.HandleFunc("POST /api/sources/{key}/orders", s.serveAddOrder)
	mux.HandleFunc("PUT /api/sources/{key}/orders/{id}", s.serveEditOrder)
	mux.HandleFunc("DELETE /api/sources/{key}/orders/{id}", s.serveRemoveOrder)
	mux.Handle("POST /api/sources/{key}/stack", s.mergeGuarded(http.HandlerFunc(s.serveStack)))
	mux.HandleFunc("GET /api/plugins", s.servePlugins)
	mux.HandleFunc("GET /api/plugins/{name}/icon", s.serveIcon)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "request is not valid JSON: "+err.Error())
		return false
	}
	return true
}

func (s *Service) serveList(w http.ResponseWriter, r *http.Request) {
	view := s.View()
	if fleet, ok := actor.FleetOf(r.Context()); ok {
		view = slices.DeleteFunc(view, func(v SourceView) bool { return v.Fleet != fleet })
	}
	writeJSON(w, http.StatusOK, view)
}

// FleetOf is the fleet a source belongs to, and false for a key that is no source.
func (s *Service) FleetOf(key string) (string, bool) {
	for _, st := range s.Sup.snapshot() {
		if st.def.Key == key {
			return st.def.Fleet, true
		}
	}
	return "", false
}

func (s *Service) servePlugins(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, ListPlugins(s.Home))
}

func (s *Service) serveIcon(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("name") == builtinGitHub {
		writeIcon(w, githubIcon, "image/svg+xml")
		return
	}
	m, dir, err := LoadManifest(s.Home, r.PathValue("name"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	p := iconPath(m, dir)
	if p == "" {
		writeError(w, http.StatusNotFound, "plugin "+m.Name+" has no icon")
		return
	}
	data, err := os.ReadFile(p)
	if err != nil {
		writeError(w, http.StatusNotFound, "plugin "+m.Name+" has no icon")
		return
	}
	ct := "image/png"
	if strings.EqualFold(filepath.Ext(p), ".svg") {
		ct = "image/svg+xml"
	}
	writeIcon(w, data, ct)
}

func writeIcon(w http.ResponseWriter, data []byte, ct string) {
	w.Header().Set("Content-Type", ct)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// An SVG opened on its own must not run script with Admiral's origin.
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(data)
}

func (s *Service) view(key string) (SourceView, bool) {
	for _, v := range s.View() {
		if v.Key == key {
			return v, true
		}
	}
	return SourceView{}, false
}

func settingsObject(raw json.RawMessage) (json.RawMessage, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return nil, errors.New("settings must be a JSON object")
	}
	out, _ := json.Marshal(m)
	return out, nil
}

type sourceRequest struct {
	Plugin   *string         `json:"plugin"`
	Fleet    *string         `json:"fleet"`
	Name     *string         `json:"name"`
	Settings json.RawMessage `json:"settings"`
}

func (s *Service) serveAdd(w http.ResponseWriter, r *http.Request) {
	var req sourceRequest
	if !decode(w, r, &req) {
		return
	}
	if req.Plugin == nil || *req.Plugin == "" {
		writeError(w, http.StatusBadRequest, "plugin is required")
		return
	}
	if *req.Plugin != builtinGitHub {
		if _, _, err := LoadManifest(s.Home, *req.Plugin); err != nil {
			status := http.StatusUnprocessableEntity
			if errors.Is(err, errNotInstalled) {
				status = http.StatusNotFound
			}
			writeError(w, status, err.Error())
			return
		}
	}
	def := config.Source{Plugin: *req.Plugin}
	// A source belongs to one fleet; with none named it takes the first, so the
	// add form needs no fleet picker.
	cur := s.Config.Current()
	if len(cur.Fleets) == 0 {
		writeError(w, http.StatusConflict, "add a fleet first: a source belongs to a fleet (admiral setup)")
		return
	}
	def.Fleet = cur.Fleets[0].Key
	if req.Fleet != nil && *req.Fleet != "" {
		if _, ok := cur.Fleet(*req.Fleet); !ok {
			writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("fleet %q is not a fleet in the config", *req.Fleet))
			return
		}
		def.Fleet = *req.Fleet
	}
	if req.Name != nil {
		def.Name = strings.TrimSpace(*req.Name)
	}
	if len(req.Settings) > 0 && string(req.Settings) != "null" {
		var err error
		if def.Settings, err = settingsObject(req.Settings); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	if def.Plugin == builtinGitHub {
		def.Settings = withViews(def.Settings)
	}
	s.mu.Lock()
	cfg := s.Config.Current()
	def.Key = s.newKey(cfg, def.Plugin)
	// The data directory is claimed now, so no later source is given this key.
	if err := os.MkdirAll(filepath.Join(s.Home, "data", def.Key), 0o700); err != nil {
		s.mu.Unlock()
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	err := s.write(append(cfg.SourceList, def))
	s.mu.Unlock()
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	s.Sup.Reconcile()
	v, _ := s.view(def.Key)
	writeJSON(w, http.StatusCreated, v)
}

// dropBuiltinMarks removes the served builtin mark from each views entry, so a settings body read back from GET stores only what the user chose.
func dropBuiltinMarks(settings json.RawMessage) json.RawMessage {
	var m map[string]json.RawMessage
	if json.Unmarshal(settings, &m) != nil || m == nil {
		return settings
	}
	var views []map[string]json.RawMessage
	if json.Unmarshal(m["views"], &views) != nil {
		return settings
	}
	for _, v := range views {
		delete(v, "builtin")
	}
	m["views"], _ = json.Marshal(views)
	out, _ := json.Marshal(m)
	return out
}

func withViews(settings json.RawMessage) json.RawMessage {
	var m map[string]json.RawMessage
	if json.Unmarshal(settings, &m) != nil || m == nil {
		m = map[string]json.RawMessage{}
	}
	m["views"] = config.WithGitHubBuiltinViewsJSON(m["views"])
	out, _ := json.Marshal(m)
	return out
}

func (s *Service) newKey(cfg *config.Config, plugin string) string {
	for n := 1; ; n++ {
		key := plugin
		if n > 1 {
			key = plugin + "-" + strconv.Itoa(n)
		}
		if _, used := cfg.Source(key); used {
			continue
		}
		if _, err := os.Lstat(filepath.Join(s.Home, "data", key)); err == nil {
			continue
		}
		return key
	}
}

// write saves the list through the config writer, which reloads.
func (s *Service) write(list []config.Source) error {
	raw, err := config.SourcesJSON(list)
	if err != nil {
		return err
	}
	return s.Config.SetSources(raw)
}

func (s *Service) serveSave(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	var req sourceRequest
	if !decode(w, r, &req) {
		return
	}
	var settings json.RawMessage
	if len(req.Settings) > 0 && string(req.Settings) != "null" {
		var err error
		if settings, err = settingsObject(req.Settings); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	s.mu.Lock()
	cfg := s.Config.Current()
	list := slices.Clone(cfg.SourceList)
	i := slices.IndexFunc(list, func(d config.Source) bool { return d.Key == key })
	if i < 0 {
		s.mu.Unlock()
		writeError(w, http.StatusNotFound, "source "+key+" not found")
		return
	}
	if req.Fleet != nil && *req.Fleet != list[i].Fleet {
		if _, ok := cfg.Fleet(*req.Fleet); !ok {
			s.mu.Unlock()
			writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("fleet %q is not a fleet in the config", *req.Fleet))
			return
		}
		if list[i].Plugin == builtinGitHub {
			s.mu.Unlock()
			writeError(w, http.StatusUnprocessableEntity, "a GitHub source stays with its fleet: add a GitHub source to the other fleet instead")
			return
		}
		list[i].Fleet = *req.Fleet
	}
	if req.Name != nil {
		list[i].Name = strings.TrimSpace(*req.Name)
	}
	if settings != nil {
		kept, err := keepSecrets(list[i].Settings, settings)
		if err != nil {
			s.mu.Unlock()
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		list[i].Settings = kept
	}
	if list[i].Plugin == builtinGitHub {
		if err := checkGitHubHost(list[i].Settings); err != nil {
			s.mu.Unlock()
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		list[i].Settings = withViews(dropBuiltinMarks(list[i].Settings))
	}
	gen := s.Sup.Generation(key)
	err := s.write(list)
	s.mu.Unlock()
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	s.Sup.ReconcileForce(key, gen)
	v, _ := s.view(key)
	writeJSON(w, http.StatusOK, v)
}

// unchangedMarker stands in for a stored secret in what the settings view
// returns. Saving it back keeps the stored value, so a secret never leaves
// the server and a save that did not touch it does not wipe it.
const unchangedMarker = "__admiral_unchanged__"

func (s *Service) schemaOf(key string) json.RawMessage {
	if rn := s.Sup.runner(key); rn != nil {
		rn.mu.Lock()
		schema := rn.schema
		rn.mu.Unlock()
		if schema != nil {
			return schema
		}
	}
	// The built-in source's schema is fixed, so it masks right before the source has started.
	if def, ok := s.Config.Current().Source(key); ok && def.Plugin == builtinGitHub {
		return githubSchema
	}
	return nil
}

// decodeValue reads JSON keeping numbers exactly as written.
func decodeValue(raw json.RawMessage) any {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	_ = dec.Decode(&v)
	return v
}

// maskSecrets replaces the stored strings and numbers in v with the marker
// unless the schema plainly declares them public. Anything the walk cannot
// follow with certainty is masked, so a schema Admiral cannot read never lets
// a secret out.
func maskSecrets(v any, sc map[string]any) any {
	switch t := v.(type) {
	case string:
		if t != "" && !declaredPlain(sc, false) {
			return unchangedMarker
		}
	case json.Number:
		if !declaredPlain(sc, true) {
			return unchangedMarker
		}
	case []any:
		var items map[string]any
		if followable(sc) {
			items, _ = sc["items"].(map[string]any)
		}
		for i := range t {
			t[i] = maskSecrets(t[i], items)
		}
	case map[string]any:
		var props map[string]any
		if followable(sc) {
			props, _ = sc["properties"].(map[string]any)
		}
		for k, val := range t {
			child, _ := props[k].(map[string]any)
			t[k] = maskSecrets(val, child)
		}
	}
	return v
}

// A schema node with a keyword outside schemaKeywords is masked, with
// everything under it.
var schemaKeywords = map[string]bool{
	"type": true, "properties": true, "items": true, "required": true, "title": true, "description": true,
	"format": true, "enum": true, "const": true, "default": true, "examples": true, "minimum": true,
	"maximum": true, "exclusiveMinimum": true, "exclusiveMaximum": true, "multipleOf": true,
	"minLength": true, "maxLength": true, "pattern": true, "minItems": true, "maxItems": true,
	"uniqueItems": true, "minProperties": true, "maxProperties": true, "$schema": true, "$id": true,
	"$comment": true, "$defs": true, "definitions": true, "additionalProperties": true,
	"readOnly": true, "deprecated": true,
}

func followable(sc map[string]any) bool {
	if sc == nil {
		return false
	}
	for k := range sc {
		if !schemaKeywords[k] {
			return false
		}
	}
	if it, ok := sc["items"]; ok {
		if _, isObj := it.(map[string]any); !isObj {
			return false
		}
	}
	return true
}

func publicScalars(list []any) bool {
	for _, e := range list {
		switch e.(type) {
		case string, json.Number, bool, nil:
		default:
			return false
		}
	}
	return len(list) > 0
}

func declaredPlain(sc map[string]any, number bool) bool {
	if !followable(sc) {
		return false
	}
	if f, _ := sc["format"].(string); strings.EqualFold(f, "password") || sc["writeOnly"] == true {
		return false
	}
	switch t := sc["type"].(type) {
	case string:
		if number {
			return t == "number" || t == "integer"
		}
		return t == "string"
	case []any:
		for _, e := range t {
			if x, _ := e.(string); x != "string" && x != "number" && x != "integer" && x != "null" {
				return false
			}
		}
		return len(t) > 0
	case nil:
		if list, ok := sc["enum"].([]any); ok {
			return publicScalars(list)
		}
		if c, ok := sc["const"]; ok {
			return publicScalars([]any{c})
		}
	}
	return false
}

// restoreSecrets puts the stored value back wherever the engineer left the
// marker, at the same path. A marker with nothing stored is dropped from an
// object; a string that only starts with the marker is refused.
func restoreSecrets(in, old any) (any, error) {
	switch t := in.(type) {
	case string:
		if strings.HasPrefix(t, unchangedMarker) && t != unchangedMarker {
			return nil, errors.New("a setting starts with the reserved text " + unchangedMarker + " but is not exactly that text")
		}
	case []any:
		oldList, _ := old.([]any)
		for i, e := range t {
			if e == unchangedMarker {
				if i >= len(oldList) {
					return nil, errors.New("a list holds " + unchangedMarker + " where nothing is stored")
				}
				t[i] = oldList[i]
				continue
			}
			var prev any
			if i < len(oldList) {
				prev = oldList[i]
			}
			r, err := restoreSecrets(e, prev)
			if err != nil {
				return nil, err
			}
			t[i] = r
		}
	case map[string]any:
		oldMap, _ := old.(map[string]any)
		for k, val := range t {
			if val == unchangedMarker {
				if prev, ok := oldMap[k]; ok {
					t[k] = prev
				} else {
					delete(t, k)
				}
				continue
			}
			r, err := restoreSecrets(val, oldMap[k])
			if err != nil {
				return nil, err
			}
			t[k] = r
		}
	}
	return in, nil
}

var hostPattern = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?(:[0-9]{1,5})?$`)

// checkGitHubHost refuses a host that isn't a hostname with an optional port, because the page shows it inside commands to copy.
func checkGitHubHost(settings json.RawMessage) error {
	m, _ := decodeValue(settings).(map[string]any)
	host, _ := m["host"].(string)
	if host = strings.TrimSpace(host); host != "" && !hostPattern.MatchString(host) {
		return fmt.Errorf("host %q is not a hostname: use letters, digits, dots and dashes, with an optional :port", host)
	}
	return nil
}

func keepSecrets(stored, incoming json.RawMessage) (json.RawMessage, error) {
	v, err := restoreSecrets(decodeValue(incoming), decodeValue(stored))
	if err != nil {
		return nil, err
	}
	return json.Marshal(v)
}

func (s *Service) serveSettings(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	def, ok := s.Config.Current().Source(key)
	if !ok {
		writeError(w, http.StatusNotFound, "source "+key+" not found")
		return
	}
	var schema map[string]any
	_ = json.Unmarshal(s.schemaOf(key), &schema)
	settings := maskSecrets(decodeValue(def.Settings), schema)
	if settings == nil {
		settings = map[string]any{}
	}
	if m, ok := settings.(map[string]any); ok && def.Plugin == builtinGitHub {
		withViewIDs(m)
	}
	name := def.Name
	if name == "" {
		if v, ok := s.view(key); ok {
			name = v.Name
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"name": name, "settings": settings})
}

// withViewIDs gives each views entry that is a view the id the server serves it under, so the page need not work out slugs.
func withViewIDs(settings map[string]any) {
	list, _ := settings["views"].([]any)
	entries := make([]config.ViewEntry, len(list))
	for i, e := range list {
		m, _ := e.(map[string]any)
		entries[i].ID, _ = m["id"].(string)
		entries[i].Title, _ = m["title"].(string)
		entries[i].Query, _ = m["query"].(string)
	}
	for i, id := range config.ViewEntryIDs(entries) {
		if m, ok := list[i].(map[string]any); ok && id != "" {
			m["id"] = id
			if slices.Contains(config.GitHubBuiltinViews, id) {
				m["builtin"] = true
			}
		}
	}
}

// serveRepos lists what routes to the source's fleet, so the page and the routing read the same claims.
func (s *Service) serveRepos(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	cfg := s.Config.Current()
	def, ok := cfg.Source(key)
	switch {
	case !ok:
		writeError(w, http.StatusNotFound, "source "+key+" not found")
		return
	case def.Plugin != builtinGitHub:
		writeError(w, http.StatusBadRequest, "source "+key+" is not a GitHub source")
		return
	}
	router := github.RouterFromFleets(cfg.Fleets)
	c := router.Claims(def.Fleet, githubSettings(def.Settings).Host)
	warnings := append([]string{}, router.Warnings(def.Fleet)...)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"repos": c.Repos, "owners": c.Owners, "default": c.Default, "warnings": warnings})
}

// refuseGitHub answers 409 for a route that would remove or restart a GitHub source, which is always present.
func (s *Service) refuseGitHub(w http.ResponseWriter, key, what string) bool {
	if def, ok := s.Config.Current().Source(key); ok && def.Plugin == builtinGitHub {
		writeError(w, http.StatusConflict, "The GitHub source is always present, so it can't be "+what)
		return true
	}
	return false
}

func (s *Service) serveDelete(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if s.refuseGitHub(w, key, "removed") {
		return
	}
	s.mu.Lock()
	cfg := s.Config.Current()
	list := slices.DeleteFunc(slices.Clone(cfg.SourceList), func(d config.Source) bool { return d.Key == key })
	if len(list) == len(cfg.SourceList) {
		s.mu.Unlock()
		writeError(w, http.StatusNotFound, "source "+key+" not found")
		return
	}
	err := s.write(list)
	s.mu.Unlock()
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	s.Sup.Reconcile()
	writeJSON(w, http.StatusOK, map[string]string{"deleted": key})
}

// The request names every source once, so a list sent before another browser
// added or removed one is refused.
func (s *Service) serveSourceOrder(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Sources []string `json:"sources"`
	}
	if !decode(w, r, &req) {
		return
	}
	s.mu.Lock()
	cfg := s.Config.Current()
	list := make([]config.Source, 0, len(req.Sources))
	for _, key := range req.Sources {
		def, ok := cfg.Source(key)
		if !ok || slices.ContainsFunc(list, func(d config.Source) bool { return d.Key == key }) {
			break
		}
		list = append(list, def)
	}
	if len(list) != len(req.Sources) || len(list) != len(cfg.SourceList) {
		s.mu.Unlock()
		writeError(w, http.StatusConflict, "The sources changed while you were moving one; try again")
		return
	}
	err := s.write(list)
	s.mu.Unlock()
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	s.Sup.Reconcile()
	writeJSON(w, http.StatusOK, map[string][]string{"sources": req.Sources})
}

func (s *Service) servePanelOrder(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	var req struct {
		Panels []string `json:"panels"`
	}
	if !decode(w, r, &req) {
		return
	}
	for i, id := range req.Panels {
		if id == "" || hasControl(id) || slices.Contains(req.Panels[:i], id) {
			writeError(w, http.StatusBadRequest, "panels must be distinct view IDs")
			return
		}
	}
	s.mu.Lock()
	list := slices.Clone(s.Config.Current().SourceList)
	i := slices.IndexFunc(list, func(d config.Source) bool { return d.Key == key })
	if i < 0 {
		s.mu.Unlock()
		writeError(w, http.StatusNotFound, "source "+key+" not found")
		return
	}
	if list[i].Plugin == builtinGitHub {
		list[i].Settings = config.ReorderGitHubViews(list[i].Settings, req.Panels)
		list[i].PanelOrder = nil
	} else {
		list[i].PanelOrder = req.Panels
	}
	err := s.write(list)
	s.mu.Unlock()
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	s.Sup.Reconcile()
	writeJSON(w, http.StatusOK, map[string][]string{"panels": req.Panels})
}

func (s *Service) serveRestart(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if s.refuseGitHub(w, key, "restarted") {
		return
	}
	if !s.Sup.Restart(key) {
		writeError(w, http.StatusNotFound, "source "+key+" not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"restarting": key})
}

func (s *Service) serveSchema(w http.ResponseWriter, r *http.Request) {
	rn := s.Sup.runner(r.PathValue("key"))
	if rn == nil {
		writeError(w, http.StatusNotFound, "source "+r.PathValue("key")+" not found")
		return
	}
	rn.mu.Lock()
	schema := rn.schema
	rn.mu.Unlock()
	if schema == nil {
		writeError(w, http.StatusConflict, "the source has not started yet, so it has not sent its settings schema")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(append(slices.Clone(schema), '\n'))
}

func (s *Service) servePage(w http.ResponseWriter, r *http.Request) {
	rn := s.Sup.runner(r.PathValue("key"))
	if rn == nil {
		writeError(w, http.StatusNotFound, "source "+r.PathValue("key")+" not found")
		return
	}
	v, ok := rn.Page()
	if !ok {
		writeError(w, http.StatusNotFound, errNoPage.Error())
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Service) serveRow(w http.ResponseWriter, r *http.Request) {
	rn := s.Sup.runner(r.PathValue("key"))
	if rn == nil {
		writeError(w, http.StatusNotFound, "source "+r.PathValue("key")+" not found")
		return
	}
	include := r.URL.Query()["include"]
	if len(include) > maxInclude || slices.ContainsFunc(include, func(v string) bool { return len([]rune(v)) > maxIDRunes }) {
		writeError(w, http.StatusBadRequest, "include takes at most "+strconv.Itoa(maxInclude)+" values of "+strconv.Itoa(maxIDRunes)+" characters each")
		return
	}
	d, err := rn.rowDetail(r.Context(), r.PathValue("id"), include)
	switch {
	case errors.Is(err, errNoPage), errors.Is(err, errNoRow):
		writeError(w, http.StatusNotFound, err.Error())
	case err != nil:
		writeError(w, http.StatusBadGateway, cleanMsg(err.Error()))
	default:
		writeJSON(w, http.StatusOK, d)
	}
}

func (s *Service) mutable(w http.ResponseWriter, key, capability string) (*runner, conn, bool) {
	rn, p, fail := s.mutableFor(key, capability)
	if fail != nil {
		writeError(w, fail.Status, fail.Msg)
		return nil, nil, false
	}
	return rn, p, true
}

type RequestError struct {
	Status int
	Msg    string
}

func (e *RequestError) Error() string { return e.Msg }

func (s *Service) mutableFor(key, capability string) (*runner, conn, *RequestError) {
	rn := s.Sup.runner(key)
	if rn == nil {
		return nil, nil, &RequestError{Status: http.StatusNotFound, Msg: "source " + key + " not found"}
	}
	rn.mu.Lock()
	readOnly, state, caps := rn.readOnly, rn.state, rn.caps
	rn.mu.Unlock()
	p, live := rn.live()
	switch {
	case readOnly:
		return nil, nil, &RequestError{Status: http.StatusConflict, Msg: "The source is read-only until it runs again"}
	case !live:
		return nil, nil, &RequestError{Status: http.StatusConflict, Msg: "The source is " + state + ", not running yet"}
	case !slices.Contains(caps, capability):
		return nil, nil, &RequestError{Status: http.StatusConflict, Msg: "The source cannot " + capability + " orders"}
	}
	return rn, p, nil
}

func callFailure(err error) *RequestError {
	var rpc *rpcError
	var unsent *notSentError
	switch {
	case errors.As(err, &rpc):
		return &RequestError{Status: http.StatusUnprocessableEntity, Msg: rpc.Error()} // the plugin refused: nothing changed
	case errors.As(err, &unsent):
		return &RequestError{Status: http.StatusBadGateway, Msg: err.Error()} // the request never went out
	case errors.Is(err, errTimeout):
		return &RequestError{Status: http.StatusGatewayTimeout, Msg: err.Error()}
	}
	return &RequestError{Status: http.StatusBadGateway, Msg: err.Error()}
}

func callError(w http.ResponseWriter, err error) {
	f := callFailure(err)
	writeError(w, f.Status, f.Msg)
}

func (s *Service) serveRefresh(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Panel string `json:"panel"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "request is not valid JSON: "+err.Error())
		return
	}
	rn, p, ok := s.mutable(w, r.PathValue("key"), "refresh")
	if !ok {
		return
	}
	params := map[string]any{}
	if req.Panel != "" {
		params["panel"] = req.Panel
	}
	if _, err := p.call(r.Context(), "orders.refresh", params, callTimeout); err != nil {
		callError(w, err)
		return
	}
	rn.wantPull(false, req.Panel)
	writeJSON(w, http.StatusOK, map[string]string{"refreshing": rn.key})
}

func checkText(text string) string {
	switch {
	case strings.TrimSpace(text) == "":
		return "An order cannot be empty"
	case hasTextControl(text):
		return "An order cannot hold control characters other than line breaks and tabs, such as the escape codes in pasted terminal output"
	case len(text) > MaxOrderText:
		return fmt.Sprintf("An order can hold at most 12,000 bytes, and this one is %d", len(text))
	}
	return ""
}

// panelOf finds the panel an order is in, or the only panel when none is named.
func panelOf(rn *runner, panel, orderID string) (string, bool) {
	rn.mu.Lock()
	defer rn.mu.Unlock()
	for _, p := range rn.panels {
		switch {
		case orderID != "":
			for _, o := range p.Orders {
				if o.ID == orderID {
					return p.ID, true
				}
			}
		case panel != "" && p.ID == panel:
			return p.ID, true
		}
	}
	if orderID == "" && panel == "" && len(rn.panels) == 1 {
		return rn.panels[0].ID, true
	}
	return "", false
}

func (s *Service) serveAddOrder(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Panel string `json:"panel"`
		Text  string `json:"text"`
	}
	if !decode(w, r, &req) {
		return
	}
	id, fail := s.AddOrder(r.Context(), r.PathValue("key"), req.Panel, req.Text)
	if fail != nil {
		writeError(w, fail.Status, fail.Msg)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": id})
}

func (s *Service) AddOrder(ctx context.Context, key, panel, text string) (string, *RequestError) {
	rn, p, fail := s.mutableFor(key, "add")
	if fail != nil {
		return "", fail
	}
	if msg := checkText(text); msg != "" {
		return "", &RequestError{Status: http.StatusBadRequest, Msg: msg}
	}
	id, found := panelOf(rn, panel, "")
	if !found {
		return "", &RequestError{Status: http.StatusNotFound, Msg: "view " + strconv.Quote(panel) + " not found"}
	}
	panel = id
	res, err := p.call(ctx, "orders.add", map[string]any{"panel": panel, "text": text}, callTimeout)
	if err != nil {
		return "", callFailure(err)
	}
	var added struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(res, &added); err != nil || added.ID == "" || hasControl(added.ID) {
		rn.logf("protocol error: orders.add answered %s", cleanMsg(string(res)))
		return "", &RequestError{Status: http.StatusBadGateway, Msg: "the order source did not answer with the new order's id"}
	}
	rn.wantPull(false, panel)
	return added.ID, nil
}

func (s *Service) serveEditOrder(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Text string `json:"text"`
	}
	if !decode(w, r, &req) {
		return
	}
	rn, p, ok := s.mutable(w, r.PathValue("key"), "edit")
	if !ok {
		return
	}
	if msg := checkText(req.Text); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	id := r.PathValue("id")
	panel, found := panelOf(rn, "", id)
	if !found {
		writeError(w, http.StatusNotFound, "order "+strconv.Quote(id)+" not found")
		return
	}
	if _, err := p.call(r.Context(), "orders.edit", map[string]any{"panel": panel, "id": id, "text": req.Text}, callTimeout); err != nil {
		callError(w, err)
		return
	}
	rn.wantPull(false, panel)
	writeJSON(w, http.StatusOK, map[string]string{"id": id})
}

func (s *Service) serveRemoveOrder(w http.ResponseWriter, r *http.Request) {
	rn, p, ok := s.mutable(w, r.PathValue("key"), "remove")
	if !ok {
		return
	}
	id := r.PathValue("id")
	panel, found := panelOf(rn, "", id)
	if !found {
		writeError(w, http.StatusNotFound, "order "+strconv.Quote(id)+" not found")
		return
	}
	if _, err := p.call(r.Context(), "orders.remove", map[string]any{"panel": panel, "id": id}, callTimeout); err != nil {
		callError(w, err)
		return
	}
	rn.wantPull(false, panel)
	writeJSON(w, http.StatusOK, map[string]string{"removed": id})
}

// A source that is down still serves its last list, and an order a ship hides
// is still found.
func (s *Service) Order(source, id string) (text, title, repo, url, panel string, err *launcher.Error) {
	rn := s.Sup.runner(source)
	if rn == nil {
		return "", "", "", "", "", &launcher.Error{Status: http.StatusBadRequest, Msg: "unknown order source " + strconv.Quote(source)}
	}
	rn.mu.Lock()
	defer rn.mu.Unlock()
	for _, p := range rn.panels {
		for _, o := range p.Orders {
			if o.ID == id {
				if o.Text != "" {
					return o.Text, o.Title, o.Repo, o.URL, p.ID, nil
				}
				return o.Title, o.Title, o.Repo, o.URL, p.ID, nil
			}
		}
	}
	return "", "", "", "", "", &launcher.Error{Status: http.StatusNotFound, Msg: "order " + strconv.Quote(id) + " not found in source " + source}
}

// Stack is the GitHub stack the order sits in; ok is false for an order in none or one not found.
func (s *Service) Stack(source, id string) (number int, url string, ok bool) {
	rn := s.Sup.runner(source)
	if rn == nil {
		return 0, "", false
	}
	rn.mu.Lock()
	defer rn.mu.Unlock()
	for _, p := range rn.panels {
		for _, o := range p.Orders {
			if o.ID == id && o.Stack != nil {
				return o.Stack.Number, o.Stack.URL, true
			}
		}
	}
	return 0, "", false
}

func (s *Service) Launched(source, id string) {
	if rn := s.Sup.runner(source); rn != nil {
		rn.launched(id)
	}
}
