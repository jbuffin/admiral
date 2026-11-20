package editors_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jbuffin/admiral/internal/auth"
	"github.com/jbuffin/admiral/internal/editors"
)

type answer struct {
	Local  bool `json:"local"`
	VSCode struct {
		Installed bool `json:"installed"`
	} `json:"vscode"`
}

func get(t *testing.T, installed bool, remote string, host string, hdr map[string]string) answer {
	t.Helper()
	mux := http.NewServeMux()
	(&editors.Service{Local: auth.IsLocal, VSCode: func() bool { return installed }}).Routes(mux)
	req := httptest.NewRequest("GET", "/api/editors", nil)
	req.RemoteAddr, req.Host = remote, host
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/editors = %d %s, want 200", rec.Code, rec.Body)
	}
	var a answer
	if err := json.Unmarshal(rec.Body.Bytes(), &a); err != nil {
		t.Fatalf("body %q is not the editors answer: %v", rec.Body, err)
	}
	return a
}

func TestALocalRequestSeesVSCodeInstalled(t *testing.T) {
	a := get(t, true, "127.0.0.1:51000", "127.0.0.1:7420", nil)
	if !a.Local || !a.VSCode.Installed {
		t.Fatalf("answer = %+v, want local and installed", a)
	}
}

func TestALocalRequestSeesVSCodeMissing(t *testing.T) {
	a := get(t, false, "127.0.0.1:51000", "127.0.0.1:7420", nil)
	if !a.Local || a.VSCode.Installed {
		t.Fatalf("answer = %+v, want local and not installed", a)
	}
}

func TestARequestFromAnotherMachineIsNotLocal(t *testing.T) {
	for name, tc := range map[string]struct {
		remote, host string
		hdr          map[string]string
	}{
		"a LAN peer":                  {"192.168.1.20:51000", "192.168.1.5:7420", nil},
		"a proxied loopback request":  {"127.0.0.1:51000", "127.0.0.1:7420", map[string]string{"X-Forwarded-For": "100.64.0.9"}},
		"a tailscale serve Host name": {"127.0.0.1:51000", "mac.tailnet.ts.net", nil},
	} {
		if a := get(t, true, tc.remote, tc.host, tc.hdr); a.Local {
			t.Errorf("%s: answer = %+v, want local false", name, a)
		}
	}
}
