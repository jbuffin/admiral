package launcher

import "testing"

func TestScrubStderrHidesURLCredentials(t *testing.T) {
	in := "fatal: unable to access 'https://user:fake-token-work-2@github.com/acme/x.git/':\n  could not resolve host\n"
	want := "fatal: unable to access 'https://***@github.com/acme/x.git/': could not resolve host"
	if got := scrubStderr(in); got != want {
		t.Errorf("scrubStderr = %q, want %q", got, want)
	}
}
