package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/jbuffin/admiral/internal/chatlog"
	"github.com/jbuffin/admiral/internal/inbox"
	"github.com/jbuffin/admiral/internal/ships"
)

const (
	ordersExitMaybeAdded = 5 // the order may have been added: never send it again
	askExitWaiting       = 3 // chat ask timed out; --wait resumes
	askExitWithdrawn     = 4 // the question is gone
)

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

// Inbox joins the items GET /api/inbox serves with the ships GET /api/ships marks
// as needing you, as the glossary's Inbox does.
func Inbox(args []string, std IO) error {
	fs := newFlags("inbox")
	port := fs.String("port", defaultPort(), "the port Admiral listens on")
	asJSON := fs.Bool("json", false, "print {items, needs_you} as JSON")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usagef("%s", Usage("inbox"))
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

type sourceView struct {
	Key   string `json:"key"`
	Views []struct {
		ID     string `json:"id"`
		Title  string `json:"title"`
		Orders []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"orders"`
	} `json:"views"`
}

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
		return usagef("%s", Usage("orders"))
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
		for _, p := range s.Views {
			row(std.Out, s.Key, p.ID, p.Title)
			for _, o := range p.Orders {
				row(std.Out, "  "+o.ID, o.Title)
			}
		}
	}
	return nil
}

// ordersAdd: a 502, a 504, a 2xx with no id and a connection that broke after the
// request went out may mean the order was added, and ADR 0016 never resends those.
func ordersAdd(args []string, std IO) error {
	fs := newFlags("orders add")
	port := fs.String("port", defaultPort(), "the port Admiral listens on")
	source := fs.String("source", "", "the order source's key")
	panel := fs.String("panel", "", "the panel's id")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if *source == "" || *panel == "" {
		return usagef("%s", Usage("orders"))
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

// askDefaultTimeout is how long `chat ask` waits, so it returns before the Bash
// tool's default of two minutes moves it to the background.
const askDefaultTimeout = 100 * time.Second

const askPoll = 100 * time.Millisecond

func Chat(args []string, std IO) error {
	if len(args) == 0 || args[0] != "ask" {
		return usagef("%s", Usage("chat"))
	}
	fs := newFlags("chat ask")
	timeout := fs.Duration("timeout", askDefaultTimeout, "how long to wait for the answer")
	wait := fs.String("wait", "", "wait on an existing question")
	if err := parseFlags(fs, args[1:]); err != nil {
		return err
	}
	if fs.NArg() > 0 || *timeout <= 0 {
		return usagef("%s", Usage("chat"))
	}
	if os.Getenv("ADMIRAL_SHIP") != "" {
		return usagef("a ship cannot ask in the chat: chat questions are for AdmiralBot")
	}
	// The fleet comes from the chat's environment, never from an argument the model controls.
	fleet := os.Getenv("ADMIRAL_CHAT_FLEET")
	if !chatlog.Valid(fleet) {
		return usagef("chat ask runs only in a fleet's AdmiralBot chat: ADMIRAL_CHAT_FLEET is not set to a fleet key")
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
		if !inbox.QuestionExists(root, fleet, id) {
			return &ExitError{askExitWithdrawn, "the question was withdrawn"}
		}
	} else {
		b, err := io.ReadAll(io.LimitReader(std.In, inbox.MaxItem+1))
		if err != nil {
			return err
		}
		if b, err = askForm(b); err != nil {
			return err
		}
		if id, err = inbox.WriteQuestion(root, fleet, b); err != nil {
			var qe *inbox.QuestionError
			if errors.As(err, &qe) {
				return usagef("%s", qe.Msg)
			}
			return err
		}
	}
	return waitAnswer(root, fleet, id, *timeout, std)
}

// The question's file going away is New chat withdrawing it.
func waitAnswer(root *os.Root, fleet, id string, timeout time.Duration, std IO) error {
	deadline := time.Now().Add(timeout)
	for {
		if b, ok := inbox.ReadQuestionAnswer(root, fleet, id); ok {
			std.Out.Write(b)
			if !bytes.HasSuffix(b, []byte("\n")) {
				fmt.Fprintln(std.Out)
			}
			return nil
		}
		if !inbox.QuestionExists(root, fleet, id) {
			return &ExitError{askExitWithdrawn, "the question was withdrawn"}
		}
		if !time.Now().Before(deadline) {
			fmt.Fprintf(std.Out, "Still waiting for the engineer's answer to question %s. Run `admiral chat ask --wait %s` to keep waiting.\n", id, id)
			return &ExitError{Code: askExitWaiting}
		}
		time.Sleep(askPoll)
	}
}
