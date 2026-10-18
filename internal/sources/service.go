package sources

import (
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
)

// Service is the sources' HTTP and WebSocket surface. Its GET handlers never
// change anything.
type Service struct {
	Home   string
	Config *config.Manager
	Sup    *Supervisor
	Ships  func() []ships.Ship
	Hub    *server.Hub

	mu     sync.Mutex // one config read-modify-write at a time
	poke   chan struct{}
	pokeMu sync.Once
}

// sourceState is a runner's state, copied.
type sourceState struct {
	def      config.Source
	state    string
	readOnly bool
	errMsg   string
	auth     *Auth
	caps     []string
	panels   []Panel
}

func (r *runner) snapshot() sourceState {
	r.mu.Lock()
	defer r.mu.Unlock()
	st := sourceState{def: r.def, state: r.state, readOnly: r.readOnly, errMsg: r.errMsg, caps: slices.Clone(r.caps)}
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
		v := SourceView{Key: st.def.Key, Name: st.def.Name, Plugin: st.def.Plugin, Capabilities: st.caps,
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
					orders = append(orders, o)
				}
			}
			p.Orders = orders
			v.Panels = append(v.Panels, p)
		}
		out = append(out, v)
	}
	return out
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
	mux.HandleFunc("PUT /api/sources/{key}", s.serveSave)
	mux.HandleFunc("DELETE /api/sources/{key}", s.serveDelete)
	mux.HandleFunc("POST /api/sources/{key}/restart", s.serveRestart)
	mux.HandleFunc("POST /api/sources/{key}/refresh", s.serveRefresh)
	mux.HandleFunc("GET /api/sources/{key}/schema", s.serveSchema)
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
		list[i].Settings = settings
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

// Order is what a launch needs from an order: the text to send and the repo to
// look for. A launch from a source that is down still works, from the last
// list Admiral saw. The order is looked up whether or not a ship hides it.
func (s *Service) Order(source, id string) (text, repo string, err *launcher.Error) {
	rn := s.Sup.runner(source)
	if rn == nil {
		return "", "", &launcher.Error{Status: http.StatusBadRequest, Msg: "unknown order source " + strconv.Quote(source)}
	}
	rn.mu.Lock()
	defer rn.mu.Unlock()
	for _, p := range rn.panels {
		for _, o := range p.Orders {
			if o.ID == id {
				if o.Text != "" {
					return o.Text, o.Repo, nil
				}
				return o.Title, o.Repo, nil
			}
		}
	}
	return "", "", &launcher.Error{Status: http.StatusNotFound, Msg: "order " + strconv.Quote(id) + " not found in source " + source}
}

// Launched tells the source its order was launched.
func (s *Service) Launched(source, id string) {
	if rn := s.Sup.runner(source); rn != nil {
		rn.launched(id)
	}
}
