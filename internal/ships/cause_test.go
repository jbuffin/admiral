package ships

import (
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/sessionhost"
	"github.com/jbuffin/admiral/internal/standing"
	"github.com/jbuffin/admiral/internal/usage"
)

// An event launch turns a refusal that a later try could clear into a proposal by its Cause, so each site sets it.
func TestEachRefusalASecondTryCouldClearCarriesItsCause(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	rig := func(t *testing.T, maxShips int) *Service {
		cfg := &config.Config{MaxShips: maxShips, Resources: map[string]config.Resource{"phone": {Capacity: 1}}, Fleets: fleets()}
		cfg.Fleets[0].Limits = standing.PresetLimits(standing.Work)
		return &Service{
			Host:     sessionhost.New("tmux", t.TempDir()+"/tmux.sock"), // never run: not ready
			Launcher: &launcher.Launcher{Home: guardedHome(t), Config: func() *config.Config { return cfg }},
		}
	}
	req := launcher.Request{Fleet: "work", Harness: "claude", Autopilot: true, Unattended: true, Skill: launcher.Skill{Kind: "none"}}
	cases := []struct {
		name string
		want string
		run  func(t *testing.T) *launcher.Error
	}{
		{"quota hold", launcher.CauseQuota, func(t *testing.T) *launcher.Error {
			s := rig(t, 5)
			s.Quota = usage.NewStore(func() time.Time { return now })
			s.Quota.SetQuota(s.dockConfig("work", "claude"), usage.Quota{
				Primary: &usage.Window{UsedPercent: 99, WindowMinutes: 300, ResetsAt: now.Add(time.Hour).Unix()},
			}, now)
			_, lerr := s.admit(req)
			return lerr
		}},
		{"cap", launcher.CauseCap, func(t *testing.T) *launcher.Error {
			s := rig(t, 1)
			s.holdUnlisted(OrderKey{"s", "o1"}, "ship-1", "p", nil)
			_, lerr := s.admit(req)
			return lerr
		}},
		{"resources", launcher.CauseResources, func(t *testing.T) *launcher.Error {
			s := rig(t, 5)
			s.Reserve = func([]string) (func(), string, string) { return nil, "phone", "in use" }
			r := req
			r.Resources = []string{"phone"}
			_, lerr := s.admit(r)
			return lerr
		}},
		{"host not known", launcher.CauseHost, func(t *testing.T) *launcher.Error {
			r := req
			r.RoutineKey = "routine:r1"
			_, lerr := rig(t, 5).LaunchOrder(r)
			return lerr
		}},
		{"routine held", launcher.CauseRoutineHeld, func(t *testing.T) *launcher.Error { return routineHeld("ship-1") }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if lerr := c.run(t); lerr == nil || lerr.Cause != c.want {
				t.Errorf("refusal = %+v, want Cause %q", lerr, c.want)
			}
		})
	}
}
