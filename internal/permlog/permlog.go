// Package permlog is each ship's permission log: every permission prompt the ship and its crew boats hit, and
// how it was answered. Writers append events to one file per ship; Read folds them into records.
package permlog

import (
	"bufio"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jbuffin/admiral/internal/repokey"
)

// Dir is the folder under Admiral's home that holds the logs.
const Dir = "permissions"

const (
	maxLine     = 16 << 10
	maxFile     = 8 << 20
	maxText     = 4 << 10
	maxListItem = 512
	maxList     = 20
)

// Answer is what became of a prompt.
type Answer string

const (
	Allow Answer = "allow"
	Deny  Answer = "deny"
	Open  Answer = "" // not answered yet, or the log never learned the answer
)

// By is who answered.
type By string

const (
	Person   By = "person"   // in the terminal or the in-place card
	Reviewer By = "reviewer" // the harness's own reviewer: Codex's auto review, Copilot's assisted mode
	Timeout  By = "timeout"  // the harness's reviewer ran out of time
	// Unattended is a harness that denied because it had no one to ask (Copilot's unattended fallback).
	Unattended By = "unattended"
	// Unknown is an answer whose answerer the harness's data does not say: a Codex crew boat's hooks fire before its reviewer.
	Unknown By = "unknown"
)

// Via is where a person's answer came from.
type Via string

const (
	Terminal Via = "terminal"
	Card     Via = "card"
)

// Call is a prompt's call, in one shape across harnesses. Each harness fills what its data holds.
type Call struct {
	Tool    string   `json:"tool"`              // the harness's own name for the tool
	Kind    string   `json:"kind"`              // shell, write, read, url, mcp or other
	Command string   `json:"command,omitempty"` // the full command text, or an MCP call's arguments
	Paths   []string `json:"paths,omitempty"`
	URLs    []string `json:"urls,omitempty"`
	Cwd     string   `json:"cwd,omitempty"`
	// The harness's own narrowest forms of a lasting rule for the call.
	Suggestions json.RawMessage `json:"suggestions,omitempty"` // Claude Code's permission_suggestions
	Identifiers []string        `json:"identifiers,omitempty"` // Copilot's command identifiers, such as "gh pr"
	Amendment   []string        `json:"amendment,omitempty"`   // Codex's proposed exec policy amendment
}

// Record is one prompt and its answer.
type Record struct {
	ID       string `json:"id"`
	At       string `json:"at"`
	Ship     string `json:"ship"`
	Crew     string `json:"crew,omitempty"`
	Harness  string `json:"harness"`
	Fleet    string `json:"fleet,omitempty"`
	Repo     string `json:"repo,omitempty"`
	Launched string `json:"launched,omitempty"` // the launch of the ship the prompt came in, so a reader can tell rounds apart
	Call     Call   `json:"call"`
	Answer   Answer `json:"answer,omitempty"`
	By       By     `json:"by,omitempty"`
	Via      Via    `json:"via,omitempty"`
	Answered string `json:"answered,omitempty"`
}

// Event is one line of the file.
type Event struct {
	Ev   string `json:"ev"` // launch, ask, answer or card
	At   string `json:"at"`
	ID   string `json:"id,omitempty"`
	Crew string `json:"crew,omitempty"`
	// launch
	Harness string `json:"harness,omitempty"` // on an ask, the boat's harness when it is not the ship's
	Fleet   string `json:"fleet,omitempty"`
	Repo    string `json:"repo,omitempty"`
	// ask
	Call *Call `json:"call,omitempty"`
	// answer, and a card's choice
	Answer Answer `json:"answer,omitempty"`
	By     By     `json:"by,omitempty"`
}

// Path is the ship's log, or false for a name that would reach outside the folder.
func Path(home, ship string) (string, bool) {
	if ship == "" || ship == "." || !filepath.IsLocal(ship) || strings.ContainsRune(ship, filepath.Separator) {
		return "", false
	}
	return filepath.Join(home, Dir, ship+".jsonl"), true
}

// Now is the time format of every event.
func Now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

// Append adds events to the ship's log. A line over the cap loses the call's long parts, and a full file takes no more.
func Append(home, ship string, evs ...Event) error {
	p, ok := Path(home, ship)
	if !ok {
		return errors.New("permlog: not a ship name: " + ship)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	if st, err := os.Lstat(p); err == nil && (!st.Mode().IsRegular() || st.Size() > maxFile) {
		return nil
	}
	var buf []byte
	for _, ev := range evs {
		if ev.At == "" {
			ev.At = Now()
		}
		if ev.Call != nil {
			c := capCall(*ev.Call)
			ev.Call = &c
		}
		line, err := json.Marshal(ev)
		if err == nil && len(line) > maxLine && ev.Call != nil {
			c := *ev.Call
			c.Command, c.Suggestions, c.Paths, c.URLs = cut(c.Command, 512), nil, nil, nil
			ev.Call = &c
			line, err = json.Marshal(ev)
		}
		if err != nil || len(line) > maxLine {
			continue
		}
		buf = append(append(buf, line...), '\n')
	}
	if len(buf) == 0 {
		return nil
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	// One write per call, so lines from the hook and the server never interleave.
	_, err = f.Write(buf)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// Repo is how a log names a checkout's repo: the key launch looks allow rules up by.
func Repo(dir string) string { return repokey.Key(dir) }

// Launched starts a launch's part of the ship's log: what every prompt after it shares.
func Launched(home, ship, harness, fleet, dir string) error {
	return Append(home, ship, Event{Ev: "launch", Harness: harness, Fleet: fleet, Repo: Repo(dir)})
}

func capCall(c Call) Call {
	c.Tool = cut(c.Tool, 128)
	c.Kind = cut(c.Kind, 16)
	c.Command = cut(c.Command, maxText)
	c.Cwd = cut(c.Cwd, maxListItem)
	c.Paths, c.URLs = capList(c.Paths), capList(c.URLs)
	c.Identifiers, c.Amendment = capList(c.Identifiers), capList(c.Amendment)
	if len(c.Suggestions) > maxText || !json.Valid(c.Suggestions) {
		c.Suggestions = nil
	}
	return c
}

func capList(in []string) []string {
	var out []string
	for _, s := range in {
		if len(out) == maxList {
			break
		}
		if s = cut(s, maxListItem); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s + "…"
}

// Read folds the ship's log into its prompts, oldest first. A missing log is no prompts.
func Read(home, ship string) ([]Record, error) {
	evs, err := readEvents(home, ship)
	return Fold(ship, evs), err
}

// ReadRound is the prompts of the ship's newest launch only. The log outlives the ship, so it also holds earlier
// rounds and earlier ships of the same name.
func ReadRound(home, ship string) ([]Record, error) {
	evs, err := readEvents(home, ship)
	last := ""
	for _, ev := range evs {
		if ev.Ev == "launch" {
			last = ev.At
		}
	}
	var out []Record
	for _, r := range Fold(ship, evs) {
		if r.Launched == last {
			out = append(out, r)
		}
	}
	return out, err
}

func readEvents(home, ship string) ([]Event, error) {
	p, ok := Path(home, ship)
	if !ok {
		return nil, errors.New("permlog: not a ship name: " + ship)
	}
	f, err := os.Open(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var evs []Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), maxLine+1)
	for sc.Scan() {
		var ev Event
		if json.Unmarshal(sc.Bytes(), &ev) == nil {
			evs = append(evs, ev)
		}
	}
	return evs, sc.Err()
}

// Fold turns a log's events into its prompts. A person's answer counts as the card's when the card answered that
// boat between the prompt and its answer, which the harness itself cannot tell from a key typed in the terminal.
func Fold(ship string, evs []Event) []Record {
	var out []Record
	byID := map[string]int{}
	var head Event
	var cards []Event
	for _, ev := range evs {
		switch ev.Ev {
		case "launch":
			head = ev
		case "ask":
			if ev.ID == "" || ev.Call == nil {
				continue
			}
			if _, dup := byID[ev.ID]; dup {
				continue
			}
			h := ev.Harness
			if h == "" {
				h = head.Harness
			}
			byID[ev.ID] = len(out)
			out = append(out, Record{ID: ev.ID, At: ev.At, Ship: ship, Crew: ev.Crew, Harness: h, Fleet: head.Fleet,
				Repo: head.Repo, Launched: head.At, Call: *ev.Call})
		case "answer":
			i, ok := byID[ev.ID]
			if !ok || out[i].Answer != Open || (ev.Answer != Allow && ev.Answer != Deny) {
				continue
			}
			out[i].Answer, out[i].By, out[i].Answered = ev.Answer, ev.By, ev.At
			if ev.By == Person {
				out[i].Via = Terminal
			}
		case "card":
			cards = append(cards, ev)
		}
	}
	for i := range out {
		r := &out[i]
		// A card's deny ends Claude Code's turn with no hook to say so, so the card is the answer.
		if r.Answer == Open {
			for _, c := range cards {
				if c.Crew == r.Crew && c.Answer == Deny && !before(c.At, r.At) {
					r.Answer, r.By, r.Via, r.Answered = Deny, Person, Card, c.At
					break
				}
			}
			continue
		}
		if r.By != Person {
			continue
		}
		for _, c := range cards {
			if c.Crew == r.Crew && c.Answer == r.Answer && !before(c.At, r.At) && !before(r.Answered, c.At) {
				r.Via = Card
				break
			}
		}
	}
	return out
}

func before(a, b string) bool {
	ta, err1 := time.Parse(time.RFC3339Nano, a)
	tb, err2 := time.Parse(time.RFC3339Nano, b)
	if err1 != nil || err2 != nil {
		return a < b
	}
	return ta.Before(tb)
}
