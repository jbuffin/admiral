package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jbuffin/admiral/internal/config"
)

// plan is a change to the fleets: the config.json before and after, indented
// the same way, and the loaded result.
type plan struct {
	before, after []byte
	fleets        *config.Object
	cfg           *config.Config
}

// newPlan previews writing fleets and refuses a result that does not load or
// in which a touched fleet shares an identity directory with another (story
// 26). Fleets not in touched are left as they are, even when they share.
func newPlan(dir string, fleets *config.Object, touched []string) (*plan, error) {
	before, after, err := config.PreviewFleets(dir, fleets.JSON())
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
	return &plan{before: before, after: after, fleets: fleets, cfg: cfg}, nil
}

// errChanged says config.json is not what the plan was made from.
var errChanged = errors.New("config.json changed since the change was planned; run it again")

// write saves the plan if config.json still holds what it was made from.
func (p *plan) write(dir string) error {
	before, _, err := config.PreviewFleets(dir, p.fleets.JSON())
	if err != nil {
		return err
	}
	if !bytes.Equal(before, p.before) {
		return errChanged
	}
	return config.SetFleets(dir, p.fleets.JSON())
}

// refuseShared fails when a touched fleet runs a harness against the identity
// directory another fleet uses for it. config.IdentityDir falls back to
// Admiral's environment and then the harness default, so two fleets that leave
// the variable unset share the default and are refused.
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

// ---- docks ----

// dockOf returns the fleet's dock object for a harness, or a new empty one.
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

// splitPair splits "harness=rest".
func splitPair(flagName, s string) (harness, rest string, err error) {
	harness, rest, ok := strings.Cut(s, "=")
	if !ok || !isHarness(harness) {
		return "", "", usagef("--%s %q: want <claude|codex|copilot>=...", flagName, s)
	}
	return harness, rest, nil
}

// withIdentity makes the dock's env name its identity directory, first in the
// object, when it does not yet (ADR 0010: nothing clears an inherited variable).
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
