package launcher

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/crew"
)

const fixChecker = "fix-checker"

// The launch rule for a member's model, from crew-members.md; the chat's model list does not apply.
var memberModel = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@\[\]-]{0,127}$`)

var modelFlag = map[string]string{"claude": "--model", "codex": "-m", "copilot": "--model"}

const gitTimeout = 10 * time.Second

// CheckMember refuses a member on any launch but Just launch, and a name that is no member name.
func CheckMember(skillKind, member string) *Error {
	switch {
	case member == "":
		return nil
	case skillKind != "none":
		return refuse(http.StatusBadRequest, "a crew member can launch a ship only with Just launch")
	case !crew.ValidName(member):
		return refuse(http.StatusBadRequest, "crew member name %q is not a lowercase slug of at most 64 characters", member)
	}
	return nil
}

type memberLaunch struct {
	Name, Role string
	Harness    string   // the member's lock, else the launch's
	ModelArgs  []string // the member's model flag, empty when it has none or the launch rule dropped it
	Prompt     string
	Warnings   []string
}

type memberAsk struct {
	Name     string
	Fleet    config.Fleet
	Explicit string // the harness the caller asked for; empty takes Default
	Default  string
	Dir      string
	RunDir   string // a boat's ship's run layer; empty for a ship
	Prompt   string // the caller's
	Model    string // the caller's own model, which a member's refuses
	Args     []string
}

// resolveMember is the one path a ship and a crew boat take to become a crew member: it resolves the
// member over the layers, picks its harness, applies the model rule and builds its prompt.
func (l *Launcher) resolveMember(ask memberAsk) (*memberLaunch, *Error) {
	home, err := filepath.Abs(l.Home)
	if err != nil {
		return nil, refuse(http.StatusInternalServerError, "could not resolve %s: %v", l.Home, err)
	}
	if !crew.ValidName(ask.Name) {
		return nil, refuse(http.StatusBadRequest, "crew member name %q is not a lowercase slug of at most 64 characters", ask.Name)
	}
	start := ask.Explicit
	if start == "" {
		start = ask.Default
	}
	var environ []string
	if dock, ok := ask.Fleet.Harness(start); ok {
		environ = dock.Environ()
	}
	if _, err := fleetCrewDir(home, ask.Fleet.Key); err != nil {
		return nil, err.(*Error)
	}
	in := CrewLayers(home, ask.Fleet.Key, start, environ, ask.Dir)
	in.Layers[crew.LayerMemory] = filepath.Join(in.Layers[crew.LayerFleet], "memory")
	if ask.RunDir != "" {
		in.Layers[crew.LayerRun] = ask.RunDir
	}
	slug, _ := originSlug(ask.Dir)
	in.Repo = slug
	res := crew.Resolve(in)
	if res.Exit != 0 {
		return nil, resolveRefusal(res)
	}
	i := slices.IndexFunc(res.Members, func(m crew.Member) bool { return m.Name == ask.Name })
	if i < 0 {
		return nil, refuse(http.StatusBadRequest, "crew member %q not found in the built-in, fleet or repo layer", ask.Name)
	}
	m := res.Members[i]
	out := &memberLaunch{Name: m.Name, Role: m.Role, Harness: start}
	warn := func(format string, a ...any) { out.Warnings = append(out.Warnings, fmt.Sprintf(format, a...)) }
	for _, d := range res.Warnings {
		if d.File != nil && ((d.Layer == m.Layer && *d.File == m.File) || (d.Layer == crew.LayerMemory && (*d.File == m.Name || strings.HasPrefix(*d.File, m.Name+"/")))) {
			warn("%s/%s: %s", d.Layer, *d.File, d.Message)
		}
	}

	fix := m.Role == fixChecker
	if m.Harness != nil {
		switch {
		case fix:
			warn("the fix checker %s runs on the launch's harness; its harness %s is ignored", m.Name, *m.Harness)
		case ask.Explicit != "" && ask.Explicit != *m.Harness:
			return nil, refuse(http.StatusBadRequest, "crew member %q is locked to %s and cannot run on %s", m.Name, *m.Harness, ask.Explicit)
		default:
			out.Harness = *m.Harness
		}
	}
	dock, ok := ask.Fleet.Harness(out.Harness)
	if !ok {
		return nil, refuse(http.StatusBadRequest, "fleet %s has no %s dock, which crew member %q runs on", ask.Fleet.Name, out.Harness, m.Name)
	}

	if m.Model != nil && *m.Model != "" {
		switch {
		case fix:
			warn("the fix checker runs on the implementer's model; model %q of %s is ignored", *m.Model, m.Name)
		case !memberModel.MatchString(*m.Model):
			warn("model %q of %s is not a model name; the dock's default stands", *m.Model, m.Name)
		case ask.Model != "":
			return nil, refuse(http.StatusBadRequest, "model %q is set next to crew member %q, whose model is %q: send one", ask.Model, m.Name, *m.Model)
		case SetsModel(dock.Args) || SetsModel(ask.Args):
			return nil, refuse(http.StatusBadRequest, "crew member %q has model %q but the %s launch already sets a model; remove one", m.Name, *m.Model, out.Harness)
		default:
			out.ModelArgs = []string{modelFlag[out.Harness], *m.Model}
		}
	}
	if len(m.Skills) > 0 {
		warn("crew member %s names skills %s, which a launch as a member does not run", m.Name, strings.Join(m.Skills, ", "))
	}

	var parts []string
	if !fix {
		parts = append(parts, strings.TrimSpace(m.Instructions), strings.TrimRight(crew.NotesBlock(m.Notes, slug), "\n"))
	}
	if strings.TrimSpace(ask.Prompt) != "" {
		parts = append(parts, ask.Prompt)
	}
	out.Prompt = strings.Join(slices.DeleteFunc(parts, func(p string) bool { return p == "" }), "\n\n")
	return out, nil
}

func (l *Launcher) warn(msgs []string) {
	for _, m := range msgs {
		if l.Warn != nil {
			l.Warn(m)
		} else {
			log.Printf("admiral: crew member launch: %s", m)
		}
	}
}

func resolveRefusal(res crew.Result) *Error {
	var msgs []string
	for _, d := range res.Errors {
		where := d.Layer
		if d.File != nil {
			where += "/" + *d.File
		}
		msgs = append(msgs, where+": "+d.Message)
	}
	return refuse(http.StatusBadRequest, "crew members can't be resolved: %s", strings.Join(msgs, "; "))
}

// SetsModel reports whether harness args already choose a model.
func SetsModel(args []string) bool {
	for i, a := range args {
		switch {
		case a == "-m" || a == "--model" || strings.HasPrefix(a, "--model="):
			return true
		case a == "-c" && i+1 < len(args) && strings.HasPrefix(args[i+1], "model="):
			return true
		}
	}
	return false
}

// originSlug reads the dir's origin remote as crew.py does: no shell, a timeout, and no git location
// variables, so a failure of any kind means no repo notes.
func originSlug(dir string) (string, bool) {
	if dir == "" {
		return "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "remote", "get-url", "origin")
	var env []string
	for _, kv := range os.Environ() {
		switch k, _, _ := strings.Cut(kv, "="); k {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE":
		default:
			env = append(env, kv)
		}
	}
	cmd.Env = append(env, "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return crew.OriginSlug(strings.TrimSpace(string(out)))
}
