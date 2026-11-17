package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/jbuffin/admiral/internal/commodore"
	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/skillindex"
)

// probeAndOffer is what setup does once config.json is final: probe every harness, then
// offer Commodore for each.
func (s *session) probeAndOffer() error {
	if err := probeFleets(s.dir, s.io); err != nil {
		return err
	}
	return s.offerCommodore()
}

// takeInstallAnswer removes install_commodore from a fleet's answers: it is an answer, not
// a config key, so it never reaches the plan or config.json.
func (s *session) takeInstallAnswer(path, key string, a *config.Object) error {
	raw, ok := a.Get("install_commodore")
	if !ok {
		return nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return fmt.Errorf("%s: fleets.%s.install_commodore: %w", path, key, err)
	}
	on, isBool := v.(bool)
	if !isBool {
		return fmt.Errorf("%s: fleets.%s.install_commodore: expected a boolean (true or false), got %s", path, key, raw)
	}
	a.Delete("install_commodore")
	if s.install == nil {
		s.install = map[string]bool{}
	}
	s.install[key] = on
	return nil
}

// offerCommodore walks the saved fleets as the probes do. Interactively each Install or
// Update is a question; with --answers only a fleet's install_commodore decides.
func (s *session) offerCommodore() error {
	data, err := config.ReadConfig(s.dir)
	if err != nil {
		return err
	}
	cfg, err := config.Parse(data)
	if err != nil {
		return err
	}
	home, err := filepath.Abs(s.dir)
	if err != nil {
		return err
	}
	runner := &commodore.Runner{Home: home, Timeout: commodore.InstallTimeout}
	w := s.io.Out
	for _, fleet := range cfg.Fleets {
		for _, h := range fleet.Harnesses {
			st := commodore.Detect(h)
			who := fmt.Sprintf("%s in fleet %s", h.Name, fleet.Name)
			var action commodore.Action
			switch st.State {
			case commodore.StateOK:
				fmt.Fprintf(w, "\n%s\n", commodoreLine(h, "ok", who))
				continue
			case commodore.StateUnknownVersion:
				fmt.Fprintf(w, "\n%s: Commodore version unknown (installed as loose skills)\n", who)
				fmt.Fprintln(w, "  To switch to the plugin, remove the loose skills, then install Commodore.")
				continue
			case commodore.StateOutdated:
				fmt.Fprintf(w, "\n%s: Commodore %s is older than this Admiral needs (%s)\n", who, st.Installed, st.Minimum)
				action = commodore.Update
			default:
				action = commodore.Install
			}
			if s.answers {
				if !s.install[fleet.Key] {
					continue
				}
			} else {
				ok, err := confirm(s.reader(), w, fmt.Sprintf("%s Commodore for %s?", words[action].verb, who))
				if err != nil {
					return err
				}
				if !ok {
					continue
				}
			}
			fmt.Fprintf(w, "%s Commodore for %s...\n", words[action].ing, who)
			out, err := runner.Run(context.Background(), h, action)
			if err != nil && !errors.Is(err, commodore.ErrAutoUpdate) {
				fmt.Fprintf(w, "Commodore was not %s: %s\n", words[action].past, printableLines(err.Error()))
				if tail := commodore.LastLines(out, 20); strings.TrimSpace(tail) != "" {
					fmt.Fprintln(w, printableLines(tail))
				}
				fmt.Fprintln(w, "Run these yourself:")
				for _, c := range commodore.Commands(h.Name, action) {
					fmt.Fprintf(w, "  %s\n", c)
				}
				continue
			}
			if err != nil {
				fmt.Fprintf(w, "Warning: %v\n", err)
			}
			fmt.Fprintln(w, commodoreLine(h, commodore.Detect(h).State, who))
		}
	}
	return nil
}

// commodoreLine is "Commodore <version> for <who>", or the state word when no version is known.
func commodoreLine(h config.Harness, state, who string) string {
	if c, ok := skillindex.LookupCommodore(h.Name, h.Environ(), ""); ok && c.Version != "" {
		return fmt.Sprintf("Commodore %s for %s", printable(c.Version), who)
	}
	return fmt.Sprintf("Commodore %s for %s", state, who)
}

// words are what setup calls each action in its questions and lines.
var words = map[commodore.Action]struct{ verb, ing, past string }{
	commodore.Install: {"Install", "Installing", "installed"},
	commodore.Update:  {"Update", "Updating", "updated"},
}

// printableLines keeps a harness's escape sequences from rewriting the terminal, line by line.
func printableLines(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = printable(l)
	}
	return strings.Join(lines, "\n")
}
