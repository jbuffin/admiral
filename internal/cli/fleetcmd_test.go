package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestFleetDefaultWithNoFleetsSaysSoAndPrintsNothing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ADMIRAL_HOME", dir)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"version":1,"fleets":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	err := Fleet([]string{"default"}, IO{In: bytes.NewReader(nil), Out: &out, Err: &errb})
	if err == nil || out.Len() != 0 || errb.Len() != 0 {
		t.Errorf("err %v, stdout %q, stderr %q; want an error saying there are no fleets and no output", err, out.String(), errb.String())
	}
}
