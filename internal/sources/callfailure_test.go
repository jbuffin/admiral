package sources

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jbuffin/admiral/internal/inbox"
)

// A resume order is sent again only when the plugin cannot have done it: the
// kind of a failed call decides, so every way a call fails has its kind here.
func TestCallFailureKind(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want inbox.AddKind
	}{
		{"the plugin refused", &rpcError{}, inbox.AddRetryable},
		{"the request line never went out", &notSentError{errors.New("broken pipe")}, inbox.AddRetryable},
		{"the plugin was gone before the write", &notSentError{errors.New("the order source exited")}, inbox.AddRetryable},
		{"no answer in time", errTimeout, inbox.AddUncertain},
		{"the plugin exited after the write", errors.New("the order source exited"), inbox.AddUncertain},
		{"the caller gave up", fmt.Errorf("wait: %w", errors.New("context canceled")), inbox.AddUncertain},
	}
	for _, c := range cases {
		if got := callFailure(c.err).AddKind(); got != c.want {
			t.Errorf("%s: kind %v, want %v", c.name, got, c.want)
		}
	}
}
