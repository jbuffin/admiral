package cli

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jbuffin/admiral/internal/inbox"
	"github.com/jbuffin/admiral/internal/ships"
)

// The commands AdmiralBot runs (ADR 0020): inbox, orders, orders add and chat ask.

// Exit codes of `orders add` and `chat ask`, beside 0, 1 and 2.
const (
	ordersExitMaybeAdded = 5 // the order may have been added: never send it again
	askExitWaiting       = 3 // chat ask timed out; --wait resumes
	askExitWithdrawn     = 4 // the question is gone
)

// age is how long ago t was, to one unit: 42s, 5m, 3h, 2d.
func age(t time.Time, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < 0:
		return "0s"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// Inbox is `admiral inbox [--port <n>] [--json]`: everything waiting on the
// engineer, as the glossary's Inbox says: the items GET /api/inbox serves and the
// ships GET /api/ships marks as needing you.
func Inbox(args []string, std IO) error {
	fs := newFlags("inbox")
	port := fs.String("port", defaultPort(), "the port Admiral listens on")
	asJSON := fs.Bool("json", false, "print {items, needs_you} as JSON")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usagef("usage: admiral inbox [--port <n>] [--json]")
	}
	dir, err := admiralHome()
	if err != nil {
		return err
	}
	base, body, err := serverFor(*port, dir)
	if err != nil {
		return err
	}
	raw, err := getJSON(base, "/api/inbox")
	if err != nil {
		return err
	}
	var inb struct {
		Items json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(raw, &inb); err != nil {
		return fmt.Errorf("GET /api/inbox: %w", err)
	}
	var needs []ships.Ship
	for _, s := range body.Ships {
		if s.Status == ships.StatusNeedsYou {
			needs = append(needs, s)
		}
	}
	if needs == nil {
		needs = []ships.Ship{}
	}
	if inb.Items == nil {
		inb.Items = json.RawMessage("[]")
	}
	if *asJSON {
		out, err := json.Marshal(struct {
			Items    json.RawMessage `json:"items"`
			NeedsYou []ships.Ship    `json:"needs_you"`
		}{inb.Items, needs})
		if err != nil {
			return err
		}
		fmt.Fprintln(std.Out, string(out))
		return nil
	}
	var items []struct {
		Session string `json:"session"`
		Name    string `json:"name"`
		Kind    string `json:"kind"`
		Title   string `json:"title"`
		Created string `json:"created"`
	}
	if err := json.Unmarshal(inb.Items, &items); err != nil {
		return fmt.Errorf("GET /api/inbox: %w", err)
	}
	now := time.Now()
	since := func(s string) string {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return age(t, now)
		}
		return ""
	}
	for _, it := range items {
		ship := it.Name
		if ship == "" {
			ship = it.Session
		}
		row(std.Out, ship, it.Kind, it.Title, since(it.Created))
	}
	for _, s := range needs {
		reason := s.Reason
		if reason == "" {
			reason = "needs you"
		}
		row(std.Out, s.Name, "needs-you", reason, since(s.StopSince))
	}
	return nil
}

// sourceView is the part of a source GET /api/sources serves that Orders reads.
type sourceView struct {
	Key    string `json:"key"`
	Panels []struct {
		ID     string `json:"id"`
		Title  string `json:"title"`
		Orders []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"orders"`
	} `json:"panels"`
}

// Orders is `admiral orders [--port <n>] [--json]` and `admiral orders add`: the
// sources, their panels and each panel's orders; and the one write AdmiralBot has.
func Orders(args []string, std IO) error {
	if len(args) > 0 && args[0] == "add" {
		return ordersAdd(args[1:], std)
	}
	fs := newFlags("orders")
	port := fs.String("port", defaultPort(), "the port Admiral listens on")
	asJSON := fs.Bool("json", false, "print the server's body unchanged")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usagef("usage: admiral orders [--port <n>] [--json]   |   admiral orders add --source <key> --panel <id> [--port <n>] [-- <text>]")
	}
	dir, err := admiralHome()
	if err != nil {
		return err
	}
	base, _, err := serverFor(*port, dir)
	if err != nil {
		return err
	}
	raw, err := getJSON(base, "/api/sources")
	if err != nil {
		return err
	}
	if *asJSON {
		std.Out.Write(bytes.TrimRight(raw, "\n"))
		fmt.Fprintln(std.Out)
		return nil
	}
	var sources []sourceView
	if err := json.Unmarshal(raw, &sources); err != nil {
		return fmt.Errorf("GET /api/sources: %w", err)
	}
	for _, s := range sources {
		for _, p := range s.Panels {
			row(std.Out, s.Key, p.ID, p.Title)
			for _, o := range p.Orders {
				row(std.Out, "  "+o.ID, o.Title)
			}
		}
	}
	return nil
}

// ordersAdd is `admiral orders add --source <key> --panel <id> [--port <n>] [-- <text>]`.
// The text comes from stdin without `-- <text>`. A 502, a 504, a 2xx with no id and
// a connection that broke after the request went out may mean the order was added,
// and ADR 0016 never sends those again: exit 5.
func ordersAdd(args []string, std IO) error {
	fs := newFlags("orders add")
	port := fs.String("port", defaultPort(), "the port Admiral listens on")
	source := fs.String("source", "", "the order source's key")
	panel := fs.String("panel", "", "the panel's id")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if *source == "" || *panel == "" {
		return usagef("usage: admiral orders add --source <key> --panel <id> [--port <n>] [-- <text>]")
	}
	var text string
	if fs.NArg() > 0 {
		text = strings.Join(fs.Args(), " ")
	} else {
		b, err := io.ReadAll(io.LimitReader(std.In, 1<<20))
		if err != nil {
			return err
		}
		text = string(b)
	}
	if strings.TrimSpace(text) == "" {
		return usagef("the order's text is empty")
	}
	dir, err := admiralHome()
	if err != nil {
		return err
	}
	base, _, err := serverFor(*port, dir)
	if err != nil {
		return err
	}
	maybe := &ExitError{ordersExitMaybeAdded, "the order may have been added; check with `admiral orders` and do not add it again"}
	body, _ := json.Marshal(map[string]string{"panel": *panel, "text": text})
	client := &http.Client{Timeout: 40 * time.Second}
	resp, err := client.Post(base+"/api/sources/"+url.PathEscape(*source)+"/orders", "application/json", bytes.NewReader(body))
	if err != nil {
		return maybe
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch {
	case resp.StatusCode == http.StatusBadGateway || resp.StatusCode == http.StatusGatewayTimeout:
		return maybe
	case resp.StatusCode/100 != 2:
		return fmt.Errorf("%s", errorText(raw))
	case err != nil:
		return maybe
	}
	var added struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(raw, &added) != nil || added.ID == "" {
		return maybe
	}
	fmt.Fprintln(std.Out, printable(added.ID))
	return nil
}

// ---- chat ask ----

// askDefaultTimeout is how long `chat ask` waits, so it returns before the Bash
// tool's default of two minutes moves it to the background.
const askDefaultTimeout = 100 * time.Second

const askPoll = 100 * time.Millisecond

// Chat is `admiral chat ask [--timeout <dur>] [--wait <id>]` (ADR 0020).
func Chat(args []string, std IO) error {
	if len(args) == 0 || args[0] != "ask" {
		return usagef("usage: admiral chat ask [--timeout <dur>] [--wait <id>]")
	}
	fs := newFlags("chat ask")
	timeout := fs.Duration("timeout", askDefaultTimeout, "how long to wait for the answer")
	wait := fs.String("wait", "", "wait on an existing question")
	if err := parseFlags(fs, args[1:]); err != nil {
		return err
	}
	if fs.NArg() > 0 || *timeout <= 0 {
		return usagef("usage: admiral chat ask [--timeout <dur>] [--wait <id>]")
	}
	if os.Getenv("ADMIRAL_SHIP") != "" {
		return usagef("a ship cannot ask in the chat: chat questions are for AdmiralBot")
	}
	dir, err := admiralHome()
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()

	id := *wait
	if id != "" {
		if !inbox.ValidID(id) {
			return usagef("%q is not a question id", id)
		}
		if !questionExists(root, id) {
			return &ExitError{askExitWithdrawn, "the question was withdrawn"}
		}
	} else {
		b, err := io.ReadAll(io.LimitReader(std.In, inbox.MaxItem+1))
		if err != nil {
			return err
		}
		if id, err = writeQuestion(root, b); err != nil {
			return err
		}
	}
	return waitAnswer(root, id, *timeout, std)
}

// newQuestionID is a time and a random tail, which matches ADR 0016's id rule.
func newQuestionID() (string, error) {
	var r [4]byte
	if _, err := rand.Read(r[:]); err != nil {
		return "", err
	}
	return time.Now().UTC().Format("20060102T150405") + "-" + hex.EncodeToString(r[:]), nil
}

// questionsRoot opens chat/questions under root, making the directories. None of
// them may be a link.
func questionsRoot(root *os.Root) (*os.Root, error) {
	dir := ""
	for _, part := range []string{"chat", "questions"} {
		dir = filepath.Join(dir, part)
		fi, err := root.Lstat(dir)
		switch {
		case os.IsNotExist(err):
			if err := root.Mkdir(dir, 0o700); err != nil {
				return nil, err
			}
		case err != nil:
			return nil, err
		case !fi.IsDir():
			return nil, fmt.Errorf("%s is not a directory", dir)
		}
	}
	return root.OpenRoot(dir)
}

// writeQuestion checks the question on stdin and writes it as a question item. A
// bad question is a usage error and writes nothing.
func writeQuestion(root *os.Root, stdin []byte) (string, error) {
	if len(stdin) > inbox.MaxItem {
		return "", usagef("the question is over %d bytes", inbox.MaxItem)
	}
	var in struct {
		Title     string          `json:"title"`
		Questions json.RawMessage `json:"questions"`
	}
	if err := json.Unmarshal(stdin, &in); err != nil {
		return "", usagef("the question is not valid JSON: %v", err)
	}
	id, err := newQuestionID()
	if err != nil {
		return "", err
	}
	item, err := json.Marshal(map[string]any{"version": 1, "id": id, "kind": inbox.KindQuestion,
		"created": time.Now().UTC().Format(time.RFC3339), "title": in.Title,
		"question": map[string]json.RawMessage{"questions": in.Questions}})
	if err != nil {
		return "", usagef("the question is not valid: %v", err)
	}
	if in.Questions == nil {
		return "", usagef("the question has no questions")
	}
	if _, err := inbox.ParseItem(item, "_chat", id); err != nil {
		return "", usagef("the question is not valid: it needs a title and questions of {id, text, multi, options: [{label}]}, each id once")
	}
	dir, err := questionsRoot(root)
	if err != nil {
		return "", err
	}
	defer dir.Close()
	tmp := "." + id + ".json.tmp"
	f, err := dir.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	_, err = f.Write(append(item, '\n'))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = dir.Rename(tmp, id+".json")
	}
	if err != nil {
		_ = dir.Remove(tmp)
		return "", err
	}
	return id, nil
}

// questionExists says whether the question's file is there.
func questionExists(root *os.Root, id string) bool {
	_, err := root.Lstat(filepath.Join("chat", "questions", id+".json"))
	return err == nil
}

// readAnswerFile is the answer file's bytes when it can be trusted: a regular
// file, not a link, at most 1 MiB, holding a JSON object.
func readAnswerFile(root *os.Root, id string) ([]byte, bool) {
	name := filepath.Join("chat", "questions", inbox.AnswerName(id))
	fi, err := root.Lstat(name)
	if err != nil || !fi.Mode().IsRegular() {
		return nil, false
	}
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, inbox.MaxItem+1))
	var obj map[string]json.RawMessage
	if err != nil || len(b) > inbox.MaxItem || json.Unmarshal(b, &obj) != nil || obj == nil {
		return nil, false
	}
	return b, true
}

// waitAnswer polls for the answer, and prints it. The question's file going away
// is New chat withdrawing it.
func waitAnswer(root *os.Root, id string, timeout time.Duration, std IO) error {
	deadline := time.Now().Add(timeout)
	for {
		if b, ok := readAnswerFile(root, id); ok {
			std.Out.Write(b)
			if !bytes.HasSuffix(b, []byte("\n")) {
				fmt.Fprintln(std.Out)
			}
			return nil
		}
		if !questionExists(root, id) {
			return &ExitError{askExitWithdrawn, "the question was withdrawn"}
		}
		if !time.Now().Before(deadline) {
			fmt.Fprintf(std.Out, "Still waiting for the engineer's answer to question %s. Run `admiral chat ask --wait %s` to keep waiting.\n", id, id)
			return &ExitError{Code: askExitWaiting}
		}
		time.Sleep(askPoll)
	}
}
