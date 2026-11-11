package launcher

import (
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/crew"
	"github.com/jbuffin/admiral/internal/skillindex"
)

const runCrewName = "run-crew"

// CheckPins refuses the pins that need no disk to judge: any pin off a Commodore launch, a bad name, a repeat.
func CheckPins(skillKind string, names []string) *Error {
	if len(names) == 0 {
		return nil
	}
	if skillKind != "commodore" {
		return refuse(http.StatusBadRequest, "crew members can be pinned only on a Commodore launch")
	}
	seen := map[string]bool{}
	for _, n := range names {
		switch {
		case !crew.ValidName(n):
			return refuse(http.StatusBadRequest, "crew member name %q is not a lowercase slug of at most 64 characters", n)
		case seen[n]:
			return refuse(http.StatusBadRequest, "crew member %q is pinned twice", n)
		}
		seen[n] = true
	}
	return nil
}

// CrewLayers is what a launch resolves pinned members over: the installed Commodore's crew/,
// the fleet's crew directory and, with a dir, the repo's .commodore/crew/.
func CrewLayers(home, fleetKey, harness string, environ []string, dir string) crew.Input {
	layers := map[string]string{crew.LayerFleet: filepath.Join(home, "fleets", fleetKey, "crew")}
	if d, ok := skillindex.CommodoreDir(harness, environ, dir); ok {
		layers[crew.LayerBuiltIn] = filepath.Join(d, "crew")
	}
	if dir != "" {
		layers[crew.LayerRepo] = filepath.Join(dir, ".commodore", "crew")
	}
	return crew.Input{Admiral: true, Layers: layers}
}

// pinnedFile is a pinned member's resolved file, read before the launch holds its lock.
type pinnedFile struct {
	name string
	data []byte
}

func resolvePins(req Request, dock config.Harness, home, fleetKey string) ([]pinnedFile, error) {
	if _, err := fleetCrewDir(home, fleetKey); err != nil {
		return nil, err
	}
	in := CrewLayers(home, fleetKey, req.Harness, dock.Environ(), req.Dir)
	res := crew.Resolve(in)
	if res.Exit != 0 {
		var msgs []string
		for _, d := range res.Errors {
			where := d.Layer
			if d.File != nil {
				where += "/" + *d.File
			}
			msgs = append(msgs, where+": "+d.Message)
		}
		return nil, refuse(http.StatusBadRequest, "crew members can't be resolved: %s", strings.Join(msgs, "; "))
	}
	byName := map[string]crew.Member{}
	for _, m := range res.Members {
		byName[m.Name] = m
	}
	var out []pinnedFile
	for _, name := range req.Crew {
		m, ok := byName[name]
		if !ok {
			return nil, refuse(http.StatusBadRequest, "crew member %q not found in the built-in, fleet or repo layer", name)
		}
		if m.Harness != nil && *m.Harness != req.Harness {
			return nil, refuse(http.StatusBadRequest, "crew member %q is locked to %s, not %s", name, *m.Harness, req.Harness)
		}
		data, err := crew.ReadFile(in.Layers[m.Layer], m.File)
		if err != nil {
			return nil, refuse(http.StatusBadRequest, "crew member %q can't be read: %v", name, err)
		}
		out = append(out, pinnedFile{name, data})
	}
	return out, nil
}

// RunCrewDir is the run layer of a ship under the home, the directory ADMIRAL_RUN_CREW names.
func RunCrewDir(home, session string) (string, bool) {
	if session == "." || !filepath.IsLocal(session) || strings.ContainsRune(session, filepath.Separator) {
		return "", false
	}
	return filepath.Join(home, runCrewName, session), true
}

// removeRunCrew deletes a ship's run layer, skipped unless run-crew/ is a real directory and not a link.
func removeRunCrew(home, session string) error {
	if _, ok := RunCrewDir(home, session); !ok {
		return nil
	}
	root, err := os.OpenRoot(home)
	if err != nil {
		return err
	}
	defer root.Close()
	if !realDir(root, runCrewName) {
		return nil
	}
	return root.RemoveAll(filepath.Join(runCrewName, session))
}

func writeRunCrew(home, session string, pins []pinnedFile) (string, error) {
	dir, ok := RunCrewDir(home, session)
	if !ok {
		return "", refuse(http.StatusInternalServerError, "%q is not a name a ship can have", session)
	}
	if _, err := ensureDirs(home, runCrewName); err != nil {
		return "", err
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		return "", refuse(http.StatusInternalServerError, "could not create %s: %v", dir, err)
	}
	for _, p := range pins {
		if err := os.WriteFile(filepath.Join(dir, p.name+".md"), p.data, 0o600); err != nil {
			return "", refuse(http.StatusInternalServerError, "could not write %s: %v", filepath.Join(dir, p.name+".md"), err)
		}
	}
	return dir, nil
}

// ensureDirs makes each level under home one at a time, checking it with Lstat, because
// MkdirAll would follow a link that stays inside home.
func ensureDirs(home string, parts ...string) (string, error) {
	dir := home
	for _, part := range parts {
		dir = filepath.Join(dir, part)
		fi, err := os.Lstat(dir)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			if err := os.Mkdir(dir, 0o700); err != nil {
				return "", refuse(http.StatusInternalServerError, "could not create %s: %v", dir, err)
			}
		case err != nil:
			return "", refuse(http.StatusInternalServerError, "could not inspect %s: %v", dir, err)
		case !fi.IsDir():
			return "", refuse(http.StatusInternalServerError, "%s is not a directory", dir)
		}
	}
	return dir, nil
}
