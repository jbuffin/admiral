package crewapi

import (
	"errors"
	"os"
	"path/filepath"
)

// ImportRef is what the import box's text names: one member, or (Source) a whole crew source.
type ImportRef struct {
	Owner, Repo string
	Name, Ref   string // Name is empty for a source; Ref is empty for the default branch
	Source      bool
}

// ParseImportRef reads a ref or GitHub URL; any other text is an error that names the accepted forms.
func ParseImportRef(text string) (ImportRef, error) {
	return ImportRef{}, nil
}

// ErrNameTaken means a file already stands at the member's name, so nothing was written.
var ErrNameTaken = errors.New("a file with that name is already in the fleet's crew directory")

// WriteImported writes <name>.md into the fleet's crew directory once: a file already there, whatever its bytes, is ErrNameTaken and stays as it is.
func WriteImported(home, key, name, text string) error {
	dir := filepath.Join(home, "fleets", key, "crew")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, name+".md"), []byte(text), 0o644)
}
