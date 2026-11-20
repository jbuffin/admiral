// Package editors tells a client which editors it can open a ship's folder in.
package editors

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jbuffin/admiral/internal/server"
)

// Service answers GET /api/editors. Local says whether the request comes from this Mac itself;
// VSCode says whether VS Code is installed here, and is the seam tests set.
type Service struct {
	Local  func(*http.Request) bool
	VSCode func() bool
}

func (s *Service) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/editors", func(w http.ResponseWriter, r *http.Request) {
		var a struct {
			Local  bool `json:"local"`
			VSCode struct {
				Installed bool `json:"installed"`
			} `json:"vscode"`
		}
		a.Local = s.Local != nil && s.Local(r)
		// A remote caller never sees whether this Mac has VS Code, and the lookup is skipped for it.
		if a.Local && s.VSCode != nil {
			a.VSCode.Installed = s.VSCode()
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(a)
	})
}

func (s *Service) Connected(*server.Client) {}

var _ server.Module = (*Service)(nil)

const bundleID = "com.microsoft.VSCode"

const lookupTimeout = 2 * time.Second

// VSCodeInstalled looks for VS Code on this Mac without launching or revealing anything: Spotlight by bundle id,
// then the two folders an app is usually dragged into.
func VSCodeInstalled() bool {
	ctx, cancel := context.WithTimeout(context.Background(), lookupTimeout)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "mdfind", "kMDItemCFBundleIdentifier == '"+bundleID+"'").Output(); err == nil && strings.TrimSpace(string(out)) != "" {
		return true
	}
	roots := []string{"/Applications"}
	if home, err := os.UserHomeDir(); err == nil {
		roots = append(roots, filepath.Join(home, "Applications"))
	}
	for _, root := range roots {
		if fi, err := os.Stat(filepath.Join(root, "Visual Studio Code.app")); err == nil && fi.IsDir() {
			return true
		}
	}
	return false
}
