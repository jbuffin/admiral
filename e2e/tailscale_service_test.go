//go:build packaging

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `admiral service install` with the mode on and serve set up shows the ts.net URL on its On other devices line.
func TestServiceInstallShowsTheTailnetURL(t *testing.T) {
	s := newSvc(t)
	r := &tsRig{t: t, dir: t.TempDir(), home: s.ah, port: "7420"}
	r.writeStatus(tsStatus(nil))
	r.writeServe(r.serve(443, false))
	r.writeConfig(true)
	fake, err := os.ReadFile(filepath.Join("testdata", "faketailscale", "tailscale"))
	if err != nil {
		t.Fatal(err)
	}
	s.writeLaunchctl()
	writeExec(t, filepath.Join(s.fakes, "tailscale"), string(fake))

	out, errOut, code := run(t, runOpts{dir: s.home, env: append(s.env(), "FAKETS_DIR="+r.dir)}, s.binary, "service", "install")
	if code != 0 {
		t.Fatalf("service install exit %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if want := "On other devices: https://" + tsName + "/"; !strings.Contains(out, want) {
		t.Errorf("output has no %q line:\n%s", want, out)
	}
}
