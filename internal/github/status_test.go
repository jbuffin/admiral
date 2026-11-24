package github

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestHTTPStatusReadsTheStatusGhPrintsInEitherForm(t *testing.T) {
	cases := map[string]struct {
		stderr string
		want   int
	}{
		"real 404":     {"gh: Not Found (HTTP 404)", 404},
		"fake 404":     {"gh: HTTP 404", 404},
		"real 409":     {"gh: Conflict (HTTP 409)", 409},
		"real 422":     {"gh: Validation Failed (HTTP 422)", 422},
		"real 403":     {"gh: API rate limit exceeded (HTTP 403)", 403},
		"real 429":     {"gh: Too Many Requests (HTTP 429)", 429},
		"colon form":   {"HTTP 422: Validation Failed", 422},
		"no status":    {"dial tcp: lookup api.github.com: no such host", 0},
		"empty stderr": {"exit status 1", 0},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			g := newRestGH(t, "")
			if err := os.WriteFile(filepath.Join(g.dir, "fail"), []byte(c.stderr), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := g.client("github.com", "").IssueRecord(context.Background(), "acme", "widgets", 1)
			if err == nil {
				t.Fatal("IssueRecord succeeded, want gh's failure")
			}
			if got := HTTPStatus(err); got != c.want {
				t.Errorf("HTTPStatus(%q) = %d, want %d", err, got, c.want)
			}
		})
	}
}

func TestHTTPStatusIsZeroForNoErrorOrAForeignError(t *testing.T) {
	if HTTPStatus(nil) != 0 || HTTPStatus(errors.New("boom")) != 0 {
		t.Error("HTTPStatus of nil or a plain error is not 0")
	}
}
