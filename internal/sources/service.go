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
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/server"
	"github.com/jbuffin/admiral/internal/ships"
	"github.com/jbuffin/admiral/internal/state"
)

// Service is the sources' HTTP and WebSocket surface. Its GET handlers never
// change anything.
type Service struct {
	Home   string
	Config *config.Manager
	Sup    *Supervisor
	Ships  func() []ships.Ship
	Hub    *server.Hub
	// State keeps which panels the engineer paused.
	State *state.Store
	// Hot says autopilot gave up on an order; nil says none.
	Hot func(source, orderID string) bool
	// OnLaunched runs when an order has been launched, by hand or by autopilot.
	OnLaunched func(source, orderID string)

	mu     sync.Mutex // one config read-modify-write at a time
	poke   chan struct{}
	pokeMu sync.Once
}

// sourceState is a runner's state, copied.
type sourceState struct {
	def       config.Source
	state     string
	readOnly  bool
	errMsg    string
	auth      *Auth
	caps      []string
	panels    []Panel
	hasSchema bool
}

func (r *runner) snapshot() sourceState {
	r.mu.Lock()
	defer r.mu.Unlock()
	st := sourceState{def: r.def, state: r.state, readOnly: r.readOnly, errMsg: r.errMsg, caps: slices.Clone(r.caps), hasSchema: r.schema != nil}
	if r.auth != nil {
		a := *r.auth
		st.auth = &a
	}
	st.panels = make([]Panel, len(r.panels))
	for i, p := range r.panels {
		p.Orders = slices.Clone(p.Orders)
		st.panels[i] = p
	}
	return st
}

// SourceView is one source as the UI receives it.
type SourceView struct {
	Key          string   `json:"key"`
	Name         string   `json:"name"`
	Plugin       string   `json:"plugin"`
	IconURL      string   `json:"icon_url"`
	Capabilities []string `json:"capabilities"`
	HasSchema    bool     `json:"has_schema"`
	State        string   `json:"state"`
	ReadOnly     bool     `json:"read_only"`
	Error        *string  `json:"error"`
	Auth         *Auth    `json:"auth"`
	Panels       []Panel  `json:"panels"`
}

// View is every source in config order, without the orders that have a ship.
func (s *Service) View() []SourceView {
	hidden := ships.HiddenOrders(s.Ships())
	out := []SourceView{}
	for _, st := range s.Sup.snapshot() {
		v := SourceView{Key: st.def.Key, Name: st.def.Name, Plugin: st.def.Plugin, Capabilities: st.caps, HasSchema: st.hasSchema,
			State: st.state, ReadOnly: st.readOnly, Auth: st.auth, Panels: []Panel{}}
		if m, dir, err := LoadManifest(s.Home, st.def.Plugin); err == nil {
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
			orders := make([]Order, 0, len(p.Orders))
			for _, o := range p.Orders {
				if !hidden[ships.OrderKey{Source: st.def.Key, OrderID: o.ID}] {
					// A plugin's hot mark stays; Admiral's is added to it.
					o.Hot = o.Hot || (s.Hot != nil && s.Hot(st.def.Key, o.ID))
					orders = append(orders, o)
				}
			}
			p.Orders = orders
			p.Autopilot = nil
			if e, ok := st.def.Autopilot[p.ID]; ok {
				p.Autopilot = &AutopilotView{Enabled: e.Enabled, Paused: s.State.Paused(st.def.Key, p.ID), MaxShips: e.MaxShips}
			}
			v.Panels = append(v.Panels, p)
		}
		SortPanels(v.Panels, st.def.PanelOrder)
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

// Notify asks Run to push the sources to every client.
func (s *Service) Notify() {
	s.pokeMu.Do(func() { s.poke = make(chan struct{}, 1) })
	select {
	case s.poke <- struct{}{}:
	default:
	}
}

// Run pushes the sources to every client after each change until ctx ends.
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

// Connected sends a new client the sources.
func (s *Service) Connected(c *server.Client) { _ = c.SendOnce("sources", s.message) }

// Routes registers the sources and plugins API.
func (s *Service) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/sources", s.serveList)
	mux.HandleFunc("POST /api/sources", s.serveAdd)
	mux.HandleFunc("PUT /api/source-order", s.serveSourceOrder)
	mux.HandleFunc("PUT /api/sources/{key}/panel-order", s.servePanelOrder)
	mux.HandleFunc("PUT /api/sources/{key}/panels/{panel}/autopilot", s.servePause)
	mux.HandleFunc("PUT /api/sources/{key}", s.serveSave)
	mux.HandleFunc("DELETE /api/sources/{key}", s.serveDelete)
	mux.HandleFunc("POST /api/sources/{key}/restart", s.serveRestart)
	mux.HandleFunc("POST /api/sources/{key}/refresh", s.serveRefresh)
	mux.HandleFunc("GET /api/sources/{key}/schema", s.serveSchema)
	mux.HandleFunc("GET /api/sources/{key}/settings", s.serveSettings)
	mux.HandleFunc("POST /api/sources/{key}/orders", s.serveAddOrder)
	mux.HandleFunc("PUT /api/sources/{key}/orders/{id}", s.serveEditOrder)
	mux.HandleFunc("DELETE /api/sources/{key}/orders/{id}", s.serveRemoveOrder)
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
	writeJSON(w, http.StatusOK, s.View())
}

func (s *Service) servePlugins(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, ListPlugins(s.Home))
}

func (s *Service) serveIcon(w http.ResponseWriter, r *http.Request) {
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
	w.Header().Set("Content-Type", ct)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// An SVG opened on its own must not run script with Admiral's origin.
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(data)
}

// view finds one source in the current list.
func (s *Service) view(key string) (SourceView, bool) {
	for _, v := range s.View() {
		if v.Key == key {
			return v, true
		}
	}
	return SourceView{}, false
}

// settingsObject checks that raw is a JSON object and returns it compacted.
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
	if _, _, err := LoadManifest(s.Home, *req.Plugin); err != nil {
		status := http.StatusUnprocessableEntity
		if errors.Is(err, errNotInstalled) {
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return
	}
	def := config.Source{Plugin: *req.Plugin}
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

// newKey makes a key from the plugin's name that no source and no data
// directory uses.
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
	rn := s.Sup.runner(key)
	if rn == nil {
		return nil
	}
	rn.mu.Lock()
	defer rn.mu.Unlock()
	return rn.schema
}

// decodeValue reads JSON keeping numbers exactly as written.
func decodeValue(raw json.RawMessage) any {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	_ = dec.Decode(&v)
	return v
}

// maskSecrets replaces the stored strings and numbers in v with the marker,
// except those the schema sc declares as plain values: reached only through
// "properties" and a single "items" schema, of type string, number or integer,
// and not "format": "password". Anything the walk cannot follow with certainty
// (no schema, "$ref", combinators, "additionalProperties", keys the schema
// does not list) is masked, so a schema Admiral cannot read never lets a
// secret out. Empty strings, booleans and nulls hold no secret and stay.
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

// schemaKeywords are the only keywords a schema node may use and still be
// followed. A node with any other keyword (prefixItems, dependentSchemas,
// $ref, oneOf…) and everything under it is masked.
var schemaKeywords = map[string]bool{
	"type": true, "properties": true, "items": true, "required": true, "title": true, "description": true,
	"format": true, "enum": true, "const": true, "default": true, "examples": true, "minimum": true,
	"maximum": true, "exclusiveMinimum": true, "exclusiveMaximum": true, "multipleOf": true,
	"minLength": true, "maxLength": true, "pattern": true, "minItems": true, "maxItems": true,
	"uniqueItems": true, "minProperties": true, "maxProperties": true, "$schema": true, "$id": true,
	"$comment": true, "$defs": true, "definitions": true, "additionalProperties": true,
	"readOnly": true, "deprecated": true,
}

// followable says the schema node uses only keywords maskSecrets understands.
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

// publicScalars reports whether the list holds only strings, numbers, booleans and nulls.
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

// declaredPlain says the schema node makes a string (or, with number set, a
// number) public: it declares that type, or a type list of scalars, or an
// enum or const of scalars with no type; and it is not marked a secret
// ("format": "password" in any case, or "writeOnly": true).
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

// keepSecrets is restoreSecrets over raw JSON settings.
func keepSecrets(stored, incoming json.RawMessage) (json.RawMessage, error) {
	v, err := restoreSecrets(decodeValue(incoming), decodeValue(stored))
	if err != nil {
		return nil, err
	}
	return json.Marshal(v)
}

// serveSettings is the name and settings the form starts from, with each
// stored secret hidden behind the marker.
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
	name := def.Name
	if name == "" {
		if v, ok := s.view(key); ok {
			name = v.Name
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"name": name, "settings": settings})
}

func (s *Service) serveDelete(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
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

// serveSourceOrder rearranges the sources. The request names every source
// once, so a list sent before another browser added or removed one is refused.
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

// servePanelOrder keeps the order of one source's panels.
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
			writeError(w, http.StatusBadRequest, "panels must be distinct panel IDs")
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
	list[i].PanelOrder = req.Panels
	err := s.write(list)
	s.mu.Unlock()
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	s.Sup.Reconcile()
	writeJSON(w, http.StatusOK, map[string][]string{"panels": req.Panels})
}

// servePause pauses or resumes autopilot on one panel. Pausing stops new
// launches and leaves the running ships alone.
func (s *Service) servePause(w http.ResponseWriter, r *http.Request) {
	key, panel := r.PathValue("key"), r.PathValue("panel")
	var req struct {
		Paused *bool `json:"paused"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Paused == nil {
		writeError(w, http.StatusBadRequest, "paused is required")
		return
	}
	def, ok := s.Config.Current().Source(key)
	if !ok {
		writeError(w, http.StatusNotFound, "source "+key+" not found")
		return
	}
	if _, ok := def.Autopilot[panel]; !ok {
		writeError(w, http.StatusNotFound, "panel "+strconv.Quote(panel)+" of source "+key+" has no autopilot entry")
		return
	}
	s.State.SetPaused(key, panel, *req.Paused)
	s.Sup.changed() // pushes the sources and wakes autopilot
	writeJSON(w, http.StatusOK, map[string]any{"paused": *req.Paused})
}

// Listing is one source's raw listing, for autopilot: the orders a ship hides
// are still in it. Listed says the source is ok and every panel has had a
// successful orders.list since the source started, so a missing order is
// really gone.
type Listing struct {
	Key    string
	Listed bool
	Panels []Panel
}

// Listings is every source's raw listing, in config order.
func (s *Service) Listings() []Listing {
	var out []Listing
	for _, st := range s.Sup.snapshot() {
		listed := st.state == StateOK
		for _, p := range st.panels {
			listed = listed && p.Listed
		}
		out = append(out, Listing{Key: st.def.Key, Listed: listed, Panels: st.panels})
	}
	return out
}

func (s *Service) serveRestart(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
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

// mutable finds the source and its process for an operation that needs the
// capability cap; it answers the request itself when it cannot.
func (s *Service) mutable(w http.ResponseWriter, key, capability string) (*runner, *proc, bool) {
	rn := s.Sup.runner(key)
	if rn == nil {
		writeError(w, http.StatusNotFound, "source "+key+" not found")
		return nil, nil, false
	}
	rn.mu.Lock()
	readOnly, state, caps := rn.readOnly, rn.state, rn.caps
	rn.mu.Unlock()
	p, live := rn.live()
	switch {
	case readOnly:
		writeError(w, http.StatusConflict, "The source is read-only until it runs again")
	case !live:
		writeError(w, http.StatusConflict, "The source is "+state+", not running yet")
	case !slices.Contains(caps, capability):
		writeError(w, http.StatusConflict, "The source cannot "+capability+" orders")
	default:
		return rn, p, true
	}
	return nil, nil, false
}

// callError answers a failed request to a plugin.
func callError(w http.ResponseWriter, err error) {
	var rpc *rpcError
	switch {
	case errors.As(err, &rpc):
		writeError(w, http.StatusUnprocessableEntity, rpc.Error())
	case errors.Is(err, errTimeout):
		writeError(w, http.StatusGatewayTimeout, err.Error())
	default:
		writeError(w, http.StatusBadGateway, err.Error())
	}
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

// checkText is the rule for the text of a new or edited order.
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
	rn, p, ok := s.mutable(w, r.PathValue("key"), "add")
	if !ok {
		return
	}
	if msg := checkText(req.Text); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	panel, found := panelOf(rn, req.Panel, "")
	if !found {
		writeError(w, http.StatusNotFound, "panel "+strconv.Quote(req.Panel)+" not found")
		return
	}
	res, err := p.call(r.Context(), "orders.add", map[string]any{"panel": panel, "text": req.Text}, callTimeout)
	if err != nil {
		callError(w, err)
		return
	}
	var added struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(res, &added); err != nil || added.ID == "" || hasControl(added.ID) {
		rn.logf("protocol error: orders.add answered %s", cleanMsg(string(res)))
		writeError(w, http.StatusBadGateway, "the order source did not answer with the new order's id")
		return
	}
	rn.wantPull(false, panel)
	writeJSON(w, http.StatusCreated, map[string]string{"id": added.ID})
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

// Order is what a launch needs from an order: the text to send, the title the
// ship shows, the repo to look for, the order's web URL and the id of the first
// panel that lists it. A launch from a
// source that is down still works, from the last list Admiral saw. The order is
// looked up whether or not a ship hides it.
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

// Launched tells the source its order was launched.
func (s *Service) Launched(source, id string) {
	if s.OnLaunched != nil {
		s.OnLaunched(source, id)
	}
	if rn := s.Sup.runner(source); rn != nil {
		rn.launched(id)
	}
}
