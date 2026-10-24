package cli

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/outcomes"
)

var outcomeFile = regexp.MustCompile(`^(\d{4}-\d{2})\.jsonl$`)

// parseSince reads a date (UTC midnight) or an RFC 3339 time.
func parseSince(s string) (time.Time, error) {
	if t, err := time.Parse(time.DateOnly, s); err == nil {
		return t, nil
	}
	return time.Parse(time.RFC3339, s)
}

// Outcomes is `admiral outcomes [--since <date>] [--ship <name>]` (ADR 0015):
// the outcome log's lines as stored, month files ascending, read straight from
// the files so the server need not run. A line is skipped unless it is a JSON
// object with a ship and a time (a crash can leave one cut short), and when it
// is longer than maxOutcomeLine.
func Outcomes(args []string, std IO) error {
	fs := newFlags("outcomes")
	sinceArg := fs.String("since", "", "only records at or after this date (YYYY-MM-DD, UTC) or RFC 3339 time")
	shipArg := fs.String("ship", "", "only this ship's records, by session name or display name")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usagef("unexpected %q: outcomes takes --since and --ship, no arguments", fs.Arg(0))
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	var since time.Time
	if set["since"] {
		t, err := parseSince(*sinceArg)
		if err != nil {
			return usagef("--since %q: want YYYY-MM-DD or an RFC 3339 time", *sinceArg)
		}
		since = t
	}
	// A display name is mapped the way its session name was made. A name that
	// maps to nothing matches no ship.
	var ships map[string]bool
	if set["ship"] {
		ships = map[string]bool{*shipArg: true}
		if slug := launcher.Slug(*shipArg); slug != "" {
			ships[slug] = true
		}
	}
	dir, err := admiralHome()
	if err != nil {
		return err
	}
	files, err := filepath.Glob(filepath.Join(dir, "outcomes", "*.jsonl"))
	if err != nil {
		return err
	}
	sort.Strings(files)
	for _, path := range files {
		m := outcomeFile.FindStringSubmatch(filepath.Base(path))
		if fi, err := os.Lstat(path); m == nil || err != nil || !fi.Mode().IsRegular() {
			continue // a FIFO or a device would hang the read
		}
		if month, err := time.Parse("2006-01", m[1]); err == nil && !since.IsZero() && !month.AddDate(0, 1, 0).After(since) {
			continue // every record in it is older than since
		}
		if err := printOutcomes(path, since, ships, std); err != nil {
			return err
		}
	}
	return nil
}

// shiplessEvents are the events with no ship: autopilot's, about an order or a panel. A
// --ship filter drops them, since none matches an empty ship.
var shiplessEvents = map[string]bool{"skipped": true, "launch_failed": true, "resource_wait": true, "retro_queued": true}

// printOutcomes streams one file's matching lines to std.Out.
func printOutcomes(path string, since time.Time, ships map[string]bool, std IO) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 64<<10)
	for {
		line, err := outcomes.ReadLine(r)
		if len(line) > 0 {
			var rec struct {
				Ts    time.Time `json:"ts"`
				Ship  string    `json:"ship"`
				Event string    `json:"event"`
			}
			ok := line[0] == '{' && json.Unmarshal(line, &rec) == nil && !rec.Ts.IsZero() &&
				(rec.Ship != "" || shiplessEvents[rec.Event])
			if ok && (ships == nil || ships[rec.Ship]) && !rec.Ts.Before(since) {
				if _, err := fmt.Fprintf(std.Out, "%s\n", line); err != nil {
					return err
				}
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	}
}
