package main

import (
	"context"
	"os"

	"github.com/jbuffin/admiral/internal/capabilities"
	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/github"
	"github.com/jbuffin/admiral/internal/judge"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/sessionhost"
	"github.com/jbuffin/admiral/internal/shipconv"
	"github.com/jbuffin/admiral/internal/ships"
	"github.com/jbuffin/admiral/internal/shiptitle"
	"github.com/jbuffin/admiral/internal/sources"
)

func newTitler(home string, cfg *config.Manager, host *sessionhost.Host, shipService *ships.Service, tails *shipconv.Tails, supervisor *sources.Supervisor) *shiptitle.Service {
	return &shiptitle.Service{
		List: shipService.List,
		Seed: tails.Seed,
		Set:  host.SetOptions,
		ItemTitle: func(ctx context.Context, fleetKey, link string) (string, bool) {
			fleet, ok := cfg.Current().Fleet(fleetKey)
			if !ok {
				return "", false
			}
			for _, h := range launcher.GitHubHosts(cfg.Current(), fleet) {
				canonical, ok := github.ItemURL(link, h)
				if !ok {
					continue
				}
				c, ok := supervisor.GitHubClient(fleetKey, h)
				if !ok {
					c = github.NewClient(h, "", nil, os.Environ())
				}
				title, found, err := c.ItemTitle(ctx, canonical)
				return title, err == nil && found
			}
			return "", false
		},
		Judge: func(ctx context.Context, fleetKey string, call judge.Call) (judge.Reply, error) {
			fleet, ok := cfg.Current().Fleet(fleetKey)
			if !ok {
				return judge.Reply{}, &judge.NoJudgeHarness{Reason: "the fleet is gone"}
			}
			choice, err := judge.Choose(fleet, capabilities.Open(home))
			if err != nil {
				return judge.Reply{}, err
			}
			return judge.Run(ctx, choice, call)
		},
	}
}
