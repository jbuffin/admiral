package crewapi

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jbuffin/admiral/internal/crew"
)

type sidecarKind int

const (
	noSidecar     sidecarKind = iota // no member, or no sidecar beside it
	unusableSide                     // a sidecar the resolver or Admiral's update checks refuse
	usableSidecar                    // attached, or detached when shared.Detached
)

// sidecarRead is a member's sidecar as every reader of it sees it.
type sidecarRead struct {
	kind    sidecarKind
	problem string
	shared  *crew.Shared
	repo    string // owner/name as the sidecar spells it
	path    string // crew/<stem>.md
	stem    string // upstream_name
	blob    string
	at      string // imported_at
	member  []byte
}

// sidecarStems are the names of the members in dir that have a sidecar file beside them, sorted.
func sidecarStems(dir string) []string {
	entries, _ := os.ReadDir(dir)
	var out []string
	for _, e := range entries {
		if stem, ok := strings.CutSuffix(e.Name(), crew.SidecarSuffix); ok && crew.ValidName(stem) {
			out = append(out, stem)
		}
	}
	slices.Sort(out)
	return out
}

// readSidecar reads the member and its sidecar through the resolver, then holds the sidecar to what updates need: a blob and a path whose stem is the upstream name.
func readSidecar(dir, name string) sidecarRead {
	member, found, err := readRegular(filepath.Join(dir, name+".md"))
	if err != nil || !found {
		return sidecarRead{}
	}
	layerReal, rerr := filepath.EvalSymlinks(dir)
	if rerr != nil {
		layerReal = filepath.Clean(dir)
	}
	shared, raw, perr := crew.LoadSidecar(dir, layerReal, name, member)
	bad := func(why string) sidecarRead {
		return sidecarRead{kind: unusableSide, problem: why, member: member}
	}
	switch {
	case perr != nil:
		return bad(perr.Error())
	case shared == nil:
		return sidecarRead{member: member}
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return bad("it is not a JSON object")
	}
	text := func(key string) string {
		var s string
		if v, ok := fields[key]; ok && json.Unmarshal(bytes.TrimSpace(v), &s) == nil {
			return s
		}
		return ""
	}
	out := sidecarRead{
		kind: usableSidecar, shared: shared, member: member,
		repo: shared.Owner + "/" + shared.Repo, path: text("path"), stem: text("upstream_name"),
		blob: text("upstream_blob"), at: text("imported_at"),
	}
	file, ok := strings.CutPrefix(out.path, crewDir+"/")
	upstream, isMd := strings.CutSuffix(file, ".md")
	switch {
	case !ok || !isMd || !crew.ValidName(upstream):
		return bad("its path is not a file in " + crewDir + "/")
	case out.stem != upstream:
		return bad("its upstream name does not match its path")
	case !blobPattern.MatchString(out.blob):
		return bad("its upstream blob is not a Git object id")
	}
	return out
}
