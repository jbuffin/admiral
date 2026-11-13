package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// MaxItemBody caps the body ItemText returns, so one long issue can't fill a judge call.
const MaxItemBody = 4096

// ItemText reads the title and body of the issue or pull request a github.com-style URL names.
func (c *Client) ItemText(ctx context.Context, token, ref string) (title, body string, err error) {
	u, perr := url.Parse(ref)
	if perr != nil || !strings.EqualFold(u.Hostname(), c.host) {
		return "", "", errors.New("not an item of this host")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 4 || (parts[2] != "pull" && parts[2] != "issues") {
		return "", "", errors.New("not an issue or pull request")
	}
	n, nerr := strconv.Atoi(parts[3])
	if nerr != nil || n <= 0 {
		return "", "", errors.New("not an issue or pull request")
	}
	path := fmt.Sprintf("repos/%s/%s/issues/%d", url.PathEscape(parts[0]), url.PathEscape(parts[1]), n)
	out, stderr, rerr := c.gh(ctx, []string{"api", "--hostname", c.host, path}, c.tokenVar(), token)
	if rerr != nil {
		return "", "", fmt.Errorf("item read failed: %s", redact(strings.TrimSpace(stderr), token))
	}
	var it struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	if json.Unmarshal([]byte(out), &it) != nil {
		return "", "", errors.New("item read failed: the answer was not JSON")
	}
	if len(it.Body) > MaxItemBody {
		it.Body = strings.ToValidUTF8(it.Body[:MaxItemBody], "")
	}
	return it.Title, it.Body, nil
}
