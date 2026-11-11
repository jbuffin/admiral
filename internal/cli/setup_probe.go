package cli

import (
	"context"
	"fmt"

	"github.com/jbuffin/admiral/internal/capabilities"
	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/probes"
)

// probeFleets runs every probe of every harness of every fleet in the saved config and
// prints one line per capability. A failed probe is a result, not an error.
func probeFleets(dir string, std IO) error {
	data, err := config.ReadConfig(dir)
	if err != nil {
		return err
	}
	cfg, err := config.Parse(data)
	if err != nil {
		return err
	}
	runner := probes.NewDefault(dir)
	for _, fleet := range cfg.Fleets {
		for _, h := range fleet.Harnesses {
			fmt.Fprintf(std.Out, "\nProbing %s in fleet %s\n", h.Name, fleet.Name)
			rep, err := runner.Run(context.Background(), fleet, h.Name)
			if err != nil {
				fmt.Fprintf(std.Out, "  not probed: %v\n", err)
				continue
			}
			fmt.Fprintf(std.Out, "  version %s\n", rep.Version)
			for _, l := range rep.Lines {
				fmt.Fprintf(std.Out, "  %s: %s\n", l.Label, probeText(l))
			}
		}
	}
	return nil
}

func probeText(l probes.Line) string {
	word := "not checked"
	switch l.Status {
	case capabilities.Passed:
		word = "passed"
	case capabilities.Failed:
		word = "failed"
	}
	if l.Detail == "" {
		return word
	}
	return word + ": " + l.Detail
}
