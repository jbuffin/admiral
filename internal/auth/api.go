package auth

import (
	"encoding/json"
	"net/http"
	"time"
)

// Routes registers the device API on unversioned paths; the server strips /v1 after the guard.
func (a *Auth) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/devices", a.serveList)
	mux.HandleFunc("PATCH /api/devices/{id}", a.serveRename)
	mux.HandleFunc("DELETE /api/devices/{id}", a.serveRevoke)
	mux.HandleFunc("DELETE /api/devices", a.serveRevokeAll)
	mux.HandleFunc("POST /api/pairing-codes", a.serveMint)
	mux.HandleFunc("GET /api/push/key", a.servePushKey)
	mux.HandleFunc("PUT /api/push/subscription", a.serveSubscribe)
	mux.HandleFunc("DELETE /api/push/subscription", a.serveUnsubscribe)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type deviceView struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Login    string    `json:"login,omitempty"`
	PairedAt time.Time `json:"pairedAt"`
	LastSeen time.Time `json:"lastSeen"`
	Current  bool      `json:"current"`
	// Notifications is whether the device holds a push subscription.
	Notifications bool `json:"notifications"`
}

func (a *Auth) serveList(w http.ResponseWriter, r *http.Request) {
	current, _ := DeviceID(r)
	local := IsLocal(r)
	a.mu.Lock()
	if a.prune() {
		_ = a.save()
	}
	out := make([]deviceView, 0, len(a.devices))
	for _, d := range a.devices {
		isCurrent := (current != "" && d.ID == current) || (local && d.ThisMac)
		out = append(out, deviceView{d.ID, d.Name, d.Login, d.PairedAt, d.LastSeen, isCurrent, len(d.Subscriptions) > 0})
	}
	a.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"devices": out})
}

func (a *Auth) serveRename(w http.ResponseWriter, r *http.Request) {
	var body struct{ Name string }
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body) != nil || cleanName(body.Name) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": `expected {"name": "…"}`})
		return
	}
	a.mu.Lock()
	d := a.find(r.PathValue("id"))
	if d == nil {
		a.mu.Unlock()
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such device"})
		return
	}
	old := d.Name
	d.Name = cleanName(body.Name)
	id, name := d.ID, d.Name
	if err := a.save(); err != nil {
		d.Name = old
		a.mu.Unlock()
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not save the device list"})
		return
	}
	a.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]string{"id": id, "name": name})
}

func (a *Auth) serveRevoke(w http.ResponseWriter, r *http.Request) {
	found, err := a.revoke(r.PathValue("id"))
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such device"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not save the device list"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *Auth) serveRevokeAll(w http.ResponseWriter, r *http.Request) {
	if err := a.revokeAll(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not save the device list"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *Auth) serveMint(w http.ResponseWriter, r *http.Request) {
	code, expires, err := a.mintCode()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not make a code"})
		return
	}
	base := ""
	if a.baseURL != nil {
		base = a.baseURL()
	}
	if base == "" {
		base = "http://" + r.Host
	}
	writeJSON(w, http.StatusOK, map[string]any{"code": code, "url": base + "/pair?code=" + code, "expires": expires})
}
