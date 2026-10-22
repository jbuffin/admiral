package cli

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/jbuffin/admiral/internal/launcher"
)

// maxOutcomeLine is the longest line Outcomes reads: an `ended` record holds a
// report of up to 1 MiB (internal/ships), with room for the rest of the record.
const maxOutcomeLine = 4 << 20

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
// the files so the server need not run. A line that is not a JSON object with a
// ship and a time, such as one a crash cut short, is skipped.
func Outcomes(args []string, std IO) error {
	fs := newFlags("outcomes")
	sinceArg := fs.String("since", "", "only records at or after this date (YYYY-MM-DD, UTC) or RFC 3339 time")
	shipArg := fs.String("ship", "", "only this ship's records, by session name or display name")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	var since time.Time
	if set["since"] {
		t, err := parseSince(*sinceArg)
		if err != nil {
			return usagef("outcomes: --since wants YYYY-MM-DD or an RFC 3339 time, got %q", *sinceArg)
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
		if m == nil {
			continue
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

// printOutcomes streams one file's matching lines to std.Out.
func printOutcomes(path string, since time.Time, ships map[string]bool, std IO) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), maxOutcomeLine)
	for sc.Scan() {
		line := sc.Bytes()
		var rec struct {
			Ts   time.Time `json:"ts"`
			Ship string    `json:"ship"`
		}
		if len(line) == 0 || line[0] != '{' || json.Unmarshal(line, &rec) != nil || rec.Ts.IsZero() {
			continue
		}
		if (ships != nil && !ships[rec.Ship]) || rec.Ts.Before(since) {
			continue
		}
		if _, err := fmt.Fprintf(std.Out, "%s\n", line); err != nil {
			return err
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}
