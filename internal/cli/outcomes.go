package cli

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/outcomes"
)

func parseSince(s string) (time.Time, error) {
	if t, err := time.Parse(time.DateOnly, s); err == nil {
		return t, nil
	}
	return time.Parse(time.RFC3339, s)
}

// Outcomes reads the log files directly so the server need not run. It skips a
// line that is not a JSON object with a ship and a time: a crash can cut one short.
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
	files, err := outcomes.MonthFiles(filepath.Join(dir, "outcomes"))
	if err != nil {
		return err
	}
	for _, f := range files {
		if end := f.End(); !since.IsZero() && !end.IsZero() && !end.After(since) {
			continue // every record in it is older than since
		}
		if err := printOutcomes(f.Path, since, ships, std); err != nil {
			return err
		}
	}
	return nil
}

// shiplessEvents never match a --ship filter, since none has a ship.
var shiplessEvents = map[string]bool{"skipped": true, "launch_failed": true, "resource_wait": true, "retro_queued": true}

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
