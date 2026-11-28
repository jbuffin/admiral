package auth_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/jbuffin/admiral/internal/auth"
	"github.com/jbuffin/admiral/internal/push"
	"github.com/jbuffin/admiral/internal/server"
)

// whoAmI answers what the server would read off the request: the device and whether it came in by bearer token.
func (g *rig) whoAmI(r req) (device string, bearerTok bool, code int) {
	g.t.Helper()
	mux := http.NewServeMux()
	g.a.Routes(mux)
	mux.HandleFunc("/who", func(w http.ResponseWriter, r *http.Request) {
		id, _ := auth.DeviceID(r)
		_ = json.NewEncoder(w).Encode(map[string]any{"device": id, "bearer": auth.BearerDevice(r)})
	})
	r.target = "/who"
	rq := httptest.NewRequest("GET", r.target, nil)
	rq.RemoteAddr, rq.Host = r.remote, r.host
	for k, v := range r.hdr {
		rq.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	g.a.Middleware(mux).ServeHTTP(rec, rq)
	var out struct {
		Device string
		Bearer bool
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return out.Device, out.Bearer, rec.Code
}

func TestOnlyABearerTokenMakesTheRequestABearerDevice(t *testing.T) {
	g := newRig(t)
	token := g.pairLAN()

	device, viaBearer, code := g.whoAmI(lanReq(bearer(token)))
	if code != 200 || device == "" || !viaBearer {
		t.Errorf("bearer on the LAN: device %q bearer %v code %d, want a device by bearer", device, viaBearer, code)
	}
	device, viaBearer, code = g.whoAmI(lanReq(map[string]string{"Cookie": cookieKey + "=" + token}))
	if code != 200 || device == "" || viaBearer {
		t.Errorf("a paired device's cookie: device %q bearer %v code %d, want the device and not bearer", device, viaBearer, code)
	}
	device, viaBearer, code = g.whoAmI(req{remote: loopback, host: "127.0.0.1:7420"})
	if code != 200 || device != "" || viaBearer {
		t.Errorf("the Local route: device %q bearer %v code %d, want no device and not bearer", device, viaBearer, code)
	}
	device, viaBearer, code = g.whoAmI(req{remote: loopback, host: "127.0.0.1:7420", hdr: bearer(token)})
	if code != 200 || device != "" || viaBearer {
		t.Errorf("a bearer header on the Local route: device %q bearer %v code %d, want no device and not bearer", device, viaBearer, code)
	}
}

func TestRevokingTheDeviceClosesItsFollowingSocket(t *testing.T) {
	g := newRig(t)
	token := g.pairLAN()
	var deviceID string
	for _, d := range g.list(lanReq(bearer(token))) {
		if d.Current {
			deviceID = d.ID
		}
	}
	hub := server.NewHub()
	followers := push.NewFollowers(push.FollowersConfig{Now: time.Now, Ready: func() bool { return true }})
	followers.Snapshot(nil, nil)
	s := &server.Server{Hub: hub, Bearer: auth.BearerDevice, Messages: map[string]server.MessageHandler{"push_follow": followers.Handle}}
	mux := http.NewServeMux()
	g.a.Routes(mux)
	mux.Handle("/", s.Handler())
	guarded := g.a.Middleware(mux)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.RemoteAddr = lan
		guarded.ServeHTTP(w, r)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/ws/v1", &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + token}}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"push_follow","after":null}`)); err != nil {
		t.Fatal(err)
	}
	// The catch-up is the follow's answer: the socket is following before the device is revoked.
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("waiting for the follow's first answer: %v", err)
		}
		var m struct{ Type string }
		_ = json.Unmarshal(data, &m)
		if m.Type == "push_refused" {
			t.Fatalf("a bearer-paired device was refused: %s", data)
		}
		if m.Type == "attention" || m.Type == "push_reset" {
			break
		}
	}

	if rec := g.local("DELETE", "/api/devices/"+deviceID, ""); rec.Code != 200 && rec.Code != 204 {
		t.Fatalf("revoke: %d %s", rec.Code, rec.Body)
	}
	readCtx, stop := context.WithTimeout(ctx, 3*time.Second)
	defer stop()
	if _, _, err := conn.Read(readCtx); err == nil || readCtx.Err() != nil {
		t.Fatalf("the revoked device's following socket should close at once, got %v", err)
	}
}
