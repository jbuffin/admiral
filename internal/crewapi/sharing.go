package crewapi

import "github.com/jbuffin/admiral/internal/crew"

// Sidecar is the provenance file beside an imported member, to Commodore's crew-members.md schema.
type Sidecar struct {
	Version       int     `json:"version"`
	Host          string  `json:"host"`
	Repo          string  `json:"repo"`
	Path          string  `json:"path"`
	UpstreamName  string  `json:"upstream_name"`
	Commit        string  `json:"commit"`
	Tag           *string `json:"tag"`
	UpstreamBlob  string  `json:"upstream_blob"`
	ContentSHA256 string  `json:"content_sha256"`
	ImportedAt    string  `json:"imported_at"`
	UpdatedAt     string  `json:"updated_at"`
}

// WriteSidecar writes <name>.shared.json in dir, keeping the unknown fields of one already there.
func WriteSidecar(dir, name string, s Sidecar) error { return errNotImplemented }

// SidecarPresent says whether anything, of any kind, a dangling link included, is at <name>.shared.json in dir.
func SidecarPresent(dir, name string) (bool, error) { return false, nil }

// CanonicalMemberBytes is the file Admiral would publish for a resolved member.
func CanonicalMemberBytes(m crew.Member) []byte { return nil }

const (
	CheckPaths         = "paths"
	CheckHidden        = "hidden"
	CheckGitleaksAllow = "gitleaks-allow"
)

// PublishPaths are the places on the Mac a published file must not name.
type PublishPaths struct {
	AdmiralHome string
	Home        string
	CloneRoot   string
	SearchRoots []string
}

// Refusal is the first publish check a file fails; Message names the rule and the line.
type Refusal struct {
	Check   string
	Line    int
	Message string
}

func (r *Refusal) Error() string { return r.Message }

// CheckPublish runs the canonical bytes through the path, hidden-character and gitleaks:allow checks, in that order.
func CheckPublish(data []byte, p PublishPaths) *Refusal {
	return &Refusal{Check: "unimplemented", Message: "unimplemented"}
}
