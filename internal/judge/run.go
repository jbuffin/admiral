package judge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jbuffin/admiral/internal/capabilities"
)

type CallKind int

const (
	Lookout CallKind = iota // the small model
	Strong                  // the stronger model: reviewer, drafter, advisor
)

type Call struct {
	Kind      CallKind
	Prompt    string
	Schema    []byte  // JSON Schema the reply must fit
	BudgetUSD float64 // a cap, used only when the harness enforces one
	Timeout   time.Duration
}

// Cost is what the harness reported for the call, in USD.
type Cost struct {
	USD      float64
	Reported bool
}

// Budget is the cap the call ran under.
type Budget struct {
	USD       float64
	Available bool
}

type Reply struct {
	Fields map[string]any
	Cost   Cost
	Budget Budget
}

type FailureKind string

const (
	KindError       FailureKind = "error"
	KindTimeout     FailureKind = "timeout"
	KindBadReply    FailureKind = "bad reply"
	KindBudgetSpent FailureKind = "budget spent"
)

// Failure is a judge call that gave no usable reply. Cost is set when the harness reported one.
type Failure struct {
	Kind   FailureKind
	Detail string
	Cost   Cost
}

func (f *Failure) Error() string { return string(f.Kind) + ": " + f.Detail }

const defaultTimeout = 2 * time.Minute

// Run makes one judge call on the chosen harness and validates the reply against the
// schema. The prompt goes on stdin so no caller text can be read as a flag.
func Run(ctx context.Context, c Choice, call Call) (Reply, error) {
	var schema map[string]any
	if err := json.Unmarshal(call.Schema, &schema); err != nil {
		return Reply{}, &Failure{Kind: KindError, Detail: "the call's schema is not JSON: " + err.Error()}
	}
	if err := checkSupported(schema, true); err != nil {
		return Reply{}, &Failure{Kind: KindError, Detail: err.Error()}
	}
	root, err := os.MkdirTemp("", "admiral-judge-")
	if err != nil {
		return Reply{}, &Failure{Kind: KindError, Detail: "could not make a temporary directory: " + err.Error()}
	}
	defer os.RemoveAll(root)
	work := filepath.Join(root, "work")
	if err := os.Mkdir(work, 0o700); err != nil {
		return Reply{}, &Failure{Kind: KindError, Detail: "could not make a temporary directory: " + err.Error()}
	}

	pair, ok := models[c.Name]
	if !ok {
		return Reply{}, &Failure{Kind: KindError, Detail: "no judge calls on " + c.Name}
	}
	model := pair[1]
	if call.Kind == Lookout {
		model = pair[0]
	}

	budget := Budget{}
	if c.Name == "claude" && c.CallCap && call.BudgetUSD > 0 {
		budget = Budget{USD: call.BudgetUSD, Available: true}
	}

	prompt := call.Prompt
	if !c.JSONSchema || c.Name == "copilot" {
		prompt += "\n\nReply with one JSON object only, no other text, that fits this JSON Schema:\n" + string(call.Schema)
	}

	var args []string
	outFile := ""
	switch c.Name {
	case "claude":
		args = []string{"-p", "--model", model, "--tools", "", "--strict-mcp-config", "--output-format", "json", "--no-session-persistence", "--setting-sources", ""}
		if c.JSONSchema {
			args = append(args, "--json-schema="+string(call.Schema))
		}
		if budget.Available {
			args = append(args, "--max-budget-usd="+strconv.FormatFloat(budget.USD, 'f', -1, 64))
		}
	case "codex":
		outFile = filepath.Join(root, "reply.txt")
		args = []string{"exec", "-m", model, "--sandbox", "read-only", "--ephemeral", "--skip-git-repo-check", "--ignore-user-config", "-o", outFile}
		for _, f := range codexToolFeatures {
			args = append(args, "--disable", f)
		}
		args = append(args, "-c", `web_search="disabled"`)
		if c.JSONSchema {
			file := filepath.Join(root, "schema.json")
			if err := os.WriteFile(file, call.Schema, 0o600); err != nil {
				return Reply{}, &Failure{Kind: KindError, Detail: "could not write the schema: " + err.Error()}
			}
			args = append(args, "--output-schema", file)
		}
	default:
		// A bare --available-tools does not consume the next flag; whether it leaves no tools is
		// the probe's question. Piped stdin without -p runs headless.
		args = []string{"--model", model, "--silent", "--available-tools", "--disable-builtin-mcps", "--no-custom-instructions"}
	}

	timeout := call.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, c.Exe, args...)
	cmd.Dir = work
	cmd.Env = scrub(c.Env)
	cmd.Stdin = strings.NewReader(prompt)
	capabilities.InOwnGroup(cmd)
	cmd.WaitDelay = time.Second // a descendant holding the pipe open must not outlive the timeout
	stdout, stderr := &cappedBuffer{}, &cappedBuffer{}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	runErr := cmd.Run()

	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return Reply{}, &Failure{Kind: KindTimeout, Detail: fmt.Sprintf("%s did not answer within %s", c.Name, timeout)}
	}
	if ctx.Err() != nil {
		return Reply{}, &Failure{Kind: KindError, Detail: "the call was cancelled"}
	}

	var text string
	var structured any
	cost := Cost{}
	if stdout.over || stderr.over {
		return Reply{}, &Failure{Kind: KindBadReply, Detail: "the harness wrote more than 1 MiB"}
	}
	if c.Name == "claude" {
		var res claudeResult
		if json.Unmarshal(stdout.Bytes(), &res) == nil {
			if res.Cost != nil {
				cost = Cost{USD: *res.Cost, Reported: true}
			}
			switch {
			case res.Subtype == "error_max_budget_usd":
				return Reply{}, &Failure{Kind: KindBudgetSpent, Detail: "claude reached the call's budget", Cost: cost}
			case res.Subtype == "error_max_structured_output_retries":
				return Reply{}, &Failure{Kind: KindBadReply, Detail: "claude could not make its reply fit the schema", Cost: cost}
			case res.IsError || runErr != nil:
				msg := firstLine(strings.Join(res.Errors, " "))
				if msg == "" && res.Subtype != "success" {
					msg = firstLine(res.Subtype)
				}
				if msg == "" {
					msg = firstLine(stderr.String())
				}
				if msg == "" && runErr != nil {
					msg = runErr.Error()
				}
				return Reply{}, &Failure{Kind: KindError, Detail: orElse(msg, "claude reported an error"), Cost: cost}
			}
			text, structured = res.Result, res.Structured
		}
	}
	if runErr != nil && text == "" && structured == nil {
		msg := firstLine(stderr.String())
		if msg == "" {
			msg = firstLine(stdout.String())
		}
		return Reply{}, &Failure{Kind: KindError, Detail: orElse(msg, runErr.Error()), Cost: cost}
	}
	if c.Name != "claude" {
		text = stdout.String()
		if outFile != "" {
			b, tooBig := readCapped(outFile)
			if tooBig {
				return Reply{}, &Failure{Kind: KindBadReply, Detail: "the harness wrote more than 1 MiB"}
			}
			if len(bytes.TrimSpace(b)) > 0 {
				text = string(b)
			}
		}
	}

	if structured == nil {
		if err := json.Unmarshal([]byte(stripFence(text)), &structured); err != nil {
			return Reply{}, &Failure{Kind: KindBadReply, Detail: "the reply is not JSON: " + firstLine(err.Error()), Cost: cost}
		}
	}
	fields, isObj := structured.(map[string]any)
	if !isObj {
		return Reply{}, &Failure{Kind: KindBadReply, Detail: "the reply is not a JSON object", Cost: cost}
	}
	if err := validate(schema, fields, "reply"); err != nil {
		return Reply{}, &Failure{Kind: KindBadReply, Detail: err.Error(), Cost: cost}
	}
	return Reply{Fields: fields, Cost: cost, Budget: budget}, nil
}

const maxOutput = 1 << 20

// cappedBuffer keeps the first maxOutput bytes and notes that more came.
type cappedBuffer struct {
	bytes.Buffer
	over bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if room := maxOutput - b.Len(); len(p) > room {
		b.Buffer.Write(p[:room])
		b.over = true
	} else {
		b.Buffer.Write(p)
	}
	return len(p), nil
}

func readCapped(path string) ([]byte, bool) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	b, _ := io.ReadAll(io.LimitReader(f, maxOutput+1))
	return b[:min(len(b), maxOutput)], len(b) > maxOutput
}

// codexToolFeatures are the features of codex 0.167 that hand the model a tool, connector,
// plugin, browser, computer, sub-agent or hook; --sandbox read-only covers only the shell.
var codexToolFeatures = []string{"shell_tool", "unified_exec", "unified_exec_tty", "apps", "plugins", "remote_plugin",
	"browser_use", "browser_use_external", "browser_use_full_cdp_access", "in_app_browser", "computer_use",
	"multi_agent", "hooks", "image_generation", "view_image", "tool_suggest", "skill_search"}

type claudeResult struct {
	Subtype    string   `json:"subtype"`
	IsError    bool     `json:"is_error"`
	Result     string   `json:"result"`
	Structured any      `json:"structured_output"`
	Cost       *float64 `json:"total_cost_usd"`
	Errors     []string `json:"errors"`
}

func scrub(env []string) []string {
	out := make([]string, 0, len(env))
next:
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		for _, s := range capabilities.ScrubbedEnv {
			if k == s {
				continue next
			}
		}
		out = append(out, kv)
	}
	return out
}

// stripFence unwraps a reply the model put in a Markdown code fence.
func stripFence(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") && strings.HasSuffix(s, "```") && len(s) >= 6 {
		s = strings.TrimSuffix(strings.TrimPrefix(s, "```"), "```")
		if i := strings.IndexByte(s, '\n'); i >= 0 && !strings.HasPrefix(strings.TrimSpace(s), "{") {
			s = s[i+1:]
		}
	}
	return s
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

func orElse(s, d string) string {
	if s == "" {
		return d
	}
	return s
}
