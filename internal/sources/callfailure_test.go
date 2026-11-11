package sources

import (
	"errors"
	"testing"

	"github.com/jbuffin/admiral/internal/inbox"
)

// A resume order is sent again only when the plugin cannot have done it.
func TestCallFailureKind(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want inbox.AddKind
	}{
		{"the plugin refused", &rpcError{}, inbox.AddRetryable},
		{"the request line never went out", &notSentError{errors.New("broken pipe")}, inbox.AddRetryable},
		{"no answer in time", errTimeout, inbox.AddUncertain},
		{"the plugin exited after the write", errors.New("the order source exited"), inbox.AddUncertain},
	}
	for _, c := range cases {
		if got := callFailure(c.err).AddKind(); got != c.want {
			t.Errorf("%s: kind %v, want %v", c.name, got, c.want)
		}
	}
}
