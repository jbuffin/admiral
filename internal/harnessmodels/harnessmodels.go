// Package harnessmodels lists the models each dock's harness offers and keeps the answers.
package harnessmodels

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/jbuffin/admiral/internal/capabilities"
	"github.com/jbuffin/admiral/internal/config"
)

const (
	// The claude initialize answer is one line of about 109KB (it carries the command list).
	maxLine  = 4 << 20
	maxBytes = 16 << 20
	maxPages = 20
	maxNames = 500
)

// List asks one dock's harness for its models, killing it when it answers or the timeout passes.
// It runs the dock's resolved executable with the dock's env and none of the dock's args, which are
// launch flags that can break app-server and --acp. Names that fail config.ValidModelName are dropped.
func List(ctx context.Context, h config.Harness, timeout time.Duration) ([]string, error) {
	var args []string
	var talk func(*conv) ([]string, error)
	switch h.Name {
	case "claude":
		args, talk = []string{"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose"}, listClaude
	case "codex":
		args, talk = []string{"app-server"}, listCodex
	case "copilot":
		args, talk = []string{"--acp"}, listCopilot
	default:
		return nil, fmt.Errorf("no way to list %s's models", h.Name)
	}
	exe, ok := h.Resolve()
	if !ok {
		return nil, fmt.Errorf("%s not found", h.Name)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Env = withoutNesting(h.Environ())
	capabilities.InOwnGroup(cmd)
	cmd.WaitDelay = time.Second // a descendant holding the pipes open must not outlive the timeout
	stderr := &head{max: 2048}
	cmd.Stderr = stderr
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("%s: %w", h.Name, err)
	}
	sc := bufio.NewScanner(io.LimitReader(out, maxBytes))
	sc.Buffer(make([]byte, 0, 64<<10), maxLine)
	models, err := talk(&conv{in: in, sc: sc})
	cancel()
	_ = cmd.Wait()
	if err == nil {
		if models = clean(models); len(models) == 0 {
			err = errors.New("the harness listed no usable model names")
		}
	}
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("%s did not answer within %s", h.Name, timeout)
		}
		if msg := firstLine(stderr.String()); msg != "" {
			err = fmt.Errorf("%w: %s", err, msg)
		}
		return nil, fmt.Errorf("%s: %w", h.Name, err)
	}
	return models, nil
}

// withoutNesting drops the variables that make claude refuse to start under another Claude Code session,
// which Admiral may itself be running in.
func withoutNesting(env []string) []string {
	return slices.DeleteFunc(env, func(kv string) bool {
		return strings.HasPrefix(kv, "CLAUDECODE=") || strings.HasPrefix(kv, "CLAUDE_CODE_CHILD_SESSION=")
	})
}

func clean(in []string) []string {
	var out []string
	for _, m := range in {
		if config.ValidModelName(m) && !slices.Contains(out, m) {
			out = append(out, m)
		}
		if len(out) == maxNames {
			break
		}
	}
	return out
}

func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			if len(l) > 200 {
				l = l[:200]
			}
			return l
		}
	}
	return ""
}

// head keeps the first max bytes written to it and discards the rest.
type head struct {
	buf bytes.Buffer
	max int
}

func (h *head) Write(p []byte) (int, error) {
	if room := h.max - h.buf.Len(); room > 0 {
		h.buf.Write(p[:min(room, len(p))])
	}
	return len(p), nil
}

func (h *head) String() string { return h.buf.String() }

// conv is the line-delimited JSON conversation with one harness process.
type conv struct {
	in io.Writer
	sc *bufio.Scanner
	id int
}

func (c *conv) send(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = c.in.Write(append(b, '\n'))
	return err
}

// next is the next line that is a JSON object; anything else is noise.
func (c *conv) next() (map[string]json.RawMessage, error) {
	for c.sc.Scan() {
		var m map[string]json.RawMessage
		if json.Unmarshal(c.sc.Bytes(), &m) == nil && m != nil {
			return m, nil
		}
	}
	if err := c.sc.Err(); err != nil {
		return nil, err
	}
	return nil, errors.New("the harness closed its output before answering")
}

// call sends a JSON-RPC request and returns the result of the response with its id, skipping notifications and requests.
func (c *conv) call(method string, params any, version bool) (json.RawMessage, error) {
	c.id++
	req := map[string]any{"id": c.id, "method": method, "params": params}
	if version {
		req["jsonrpc"] = "2.0"
	}
	if err := c.send(req); err != nil {
		return nil, err
	}
	for {
		m, err := c.next()
		if err != nil {
			return nil, err
		}
		if _, isRequest := m["method"]; isRequest || string(m["id"]) != fmt.Sprint(c.id) {
			continue
		}
		if e, ok := m["error"]; ok && string(e) != "null" {
			var re struct{ Message string }
			_ = json.Unmarshal(e, &re)
			return nil, fmt.Errorf("%s failed: %s", method, firstLine(re.Message))
		}
		return m["result"], nil
	}
}

func listClaude(c *conv) ([]string, error) {
	const id = "admiral-models"
	if err := c.send(map[string]any{"type": "control_request", "request_id": id, "request": map[string]any{"subtype": "initialize"}}); err != nil {
		return nil, err
	}
	for {
		m, err := c.next()
		if err != nil {
			return nil, err
		}
		var typ string
		_ = json.Unmarshal(m["type"], &typ)
		if typ != "control_response" {
			continue
		}
		var outer struct {
			Subtype   string `json:"subtype"`
			RequestID string `json:"request_id"`
			Error     string `json:"error"`
			Response  struct {
				Models []struct {
					Value string `json:"value"`
				} `json:"models"`
			} `json:"response"`
		}
		if json.Unmarshal(m["response"], &outer) != nil || outer.RequestID != id {
			continue
		}
		if outer.Subtype == "error" {
			return nil, fmt.Errorf("initialize failed: %s", firstLine(outer.Error))
		}
		var out []string
		for _, e := range outer.Response.Models {
			if e.Value != "default" {
				out = append(out, e.Value)
			}
		}
		return out, nil
	}
}

func listCodex(c *conv) ([]string, error) {
	if _, err := c.call("initialize", map[string]any{"clientInfo": map[string]any{"name": "admiral", "title": "Admiral", "version": "0"}}, false); err != nil {
		return nil, err
	}
	if err := c.send(map[string]any{"method": "initialized"}); err != nil {
		return nil, err
	}
	var out []string
	params := map[string]any{}
	for range maxPages {
		raw, err := c.call("model/list", params, false)
		if err != nil {
			return nil, err
		}
		var page struct {
			Data []struct {
				ID     string `json:"id"`
				Model  string `json:"model"`
				Hidden bool   `json:"hidden"`
			} `json:"data"`
			NextCursor *string `json:"nextCursor"`
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			return nil, fmt.Errorf("model/list answered something else: %w", err)
		}
		for _, e := range page.Data {
			if e.Hidden {
				continue
			}
			out = append(out, cmp(e.Model, e.ID))
		}
		if page.NextCursor == nil || *page.NextCursor == "" {
			return out, nil
		}
		params = map[string]any{"cursor": *page.NextCursor}
	}
	return out, nil
}

func cmp(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func listCopilot(c *conv) ([]string, error) {
	if _, err := c.call("initialize", map[string]any{"protocolVersion": 1, "clientCapabilities": map[string]any{}}, true); err != nil {
		return nil, err
	}
	cwd, err := os.UserHomeDir()
	if err != nil {
		cwd = os.TempDir()
	}
	raw, err := c.call("session/new", map[string]any{"cwd": cwd, "mcpServers": []any{}}, true)
	if err != nil {
		return nil, err
	}
	var res struct {
		Models struct {
			Available []struct {
				ModelID string `json:"modelId"`
			} `json:"availableModels"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("session/new answered something else: %w", err)
	}
	var out []string
	for _, e := range res.Models.Available {
		out = append(out, e.ModelID)
	}
	return out, nil
}
