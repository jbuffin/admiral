package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jbuffin/admiral/internal/config"
)

type plan struct {
	before, after []byte
	fleets        *config.Object
	remote        *config.Remote // nil leaves the remote section out of the write
	cfg           *config.Config
}

// newPlan leaves fleets not in touched as they are, even when they share an
// identity directory.
func newPlan(dir string, fleets *config.Object, touched []string, ask *remoteAsk) (*plan, error) {
	var remote *config.Remote
	if ask != nil {
		cur, err := currentRemote(dir)
		if err != nil {
			return nil, err
		}
		m := ask.merged(cur)
		remote = &m
	}
	p := &plan{fleets: fleets, remote: remote}
	before, after, err := p.preview(dir)
	if err != nil {
		return nil, err
	}
	cfg, err := config.Parse(after)
	if err != nil {
		return nil, err
	}
	if err := refuseShared(cfg, touched); err != nil {
		return nil, err
	}
	p.before, p.after, p.cfg = before, after, cfg
	return p, nil
}

func (p *plan) preview(dir string) (before, after []byte, err error) {
	if p.remote != nil {
		return config.PreviewFleetsAndRemote(dir, p.fleets.JSON(), *p.remote)
	}
	return config.PreviewFleets(dir, p.fleets.JSON())
}

var errChanged = errors.New("config.json changed since the change was planned; run it again")

func (p *plan) write(dir string) error {
	before, _, err := p.preview(dir)
	if err != nil {
		return err
	}
	if !bytes.Equal(before, p.before) {
		return errChanged
	}
	if p.remote != nil {
		return config.SetFleetsAndRemote(dir, p.fleets.JSON(), *p.remote)
	}
	return config.SetFleets(dir, p.fleets.JSON())
}

// config.IdentityDir falls back to Admiral's environment and then the harness
// default, so two fleets that leave the variable unset share it and are refused.
func refuseShared(cfg *config.Config, touched []string) error {
	isTouched := map[string]bool{}
	for _, k := range touched {
		isTouched[k] = true
	}
	for _, a := range cfg.Fleets {
		if !isTouched[a.Key] {
			continue
		}
		for _, ha := range a.Harnesses {
			dirA := config.IdentityDir(ha.Name, ha.Env)
			for _, b := range cfg.Fleets {
				if b.Key == a.Key {
					continue
				}
				if hb, ok := b.Harness(ha.Name); ok && config.IdentityDir(ha.Name, hb.Env) == dirA {
					return fmt.Errorf("fleets %s and %s would both run %s with the identity directory %s; give each fleet its own",
						a.Key, b.Key, ha.Name, dirA)
				}
			}
		}
	}
	return nil
}

func dockOf(fleet *config.Object, harness string) (*config.Object, error) {
	raw, ok := fleet.Get(harness)
	if !ok {
		return config.NewObject(), nil
	}
	o, err := config.ParseObject(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", harness, err)
	}
	return o, nil
}

func envOf(dock *config.Object) (*config.Object, error) {
	raw, ok := dock.Get("env")
	if !ok {
		return config.NewObject(), nil
	}
	return config.ParseObject(raw)
}

func stringsOf(raw json.RawMessage, ok bool) []string {
	var out []string
	if ok {
		_ = json.Unmarshal(raw, &out)
	}
	return out
}

func isHarness(name string) bool {
	for _, h := range config.Harnesses() {
		if h == name {
			return true
		}
	}
	return false
}

func splitPair(flagName, s string) (harness, rest string, err error) {
	harness, rest, ok := strings.Cut(s, "=")
	if !ok || !isHarness(harness) {
		return "", "", usagef("--%s %q: want <claude|codex|copilot>=...", flagName, s)
	}
	return harness, rest, nil
}

// withIdentity names the identity directory explicitly because nothing clears an
// inherited variable (ADR 0010).
func withIdentity(dock *config.Object, harness, dir string) error {
	env, err := envOf(dock)
	if err != nil {
		return err
	}
	v, _, _ := config.IdentityVar(harness)
	if _, ok := env.Get(v); ok {
		return nil
	}
	fresh := config.NewObject()
	fresh.SetValue(v, dir)
	for _, k := range env.Keys() {
		raw, _ := env.Get(k)
		fresh.Set(k, raw)
	}
	dock.Set("env", fresh.JSON())
	return nil
}
