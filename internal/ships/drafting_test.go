package ships

import (
	"net/http"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/sessionhost"
)

func TestADraftingLaunchThatAsksForACloneIsRefusedBeforeAnyCloneIsMade(t *testing.T) {
	fl := fleets(t)
	s := &Service{
		Host:     sessionhost.New("tmux", t.TempDir()+"/tmux.sock"),
		Launcher: &launcher.Launcher{Home: guardedHome(t), Config: func() *config.Config { return &config.Config{Fleets: fl} }},
	}
	rec := launchPost(t, s, `{"fleet":"work","harness":"claude","clone":true,"text":"https://github.com/o/r","drafting":true,"skill":{"kind":"skill","name":"wayfinder"}}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(strings.ToLower(rec.Body.String()), "drafting") {
		t.Errorf("a drafting launch with clone and no dir = %d %s, want 400 saying a drafting ship needs a dir", rec.Code, rec.Body)
	}
}

func TestADraftingLaunchIgnoresAutopilotSoTheCapAndTheGuardNeverRefuseIt(t *testing.T) {
	fl := fleets(t)
	s := &Service{
		Host:     sessionhost.New("tmux", t.TempDir()+"/tmux.sock"), // never run: not ready
		Launcher: &launcher.Launcher{Home: guardedHome(t), Config: func() *config.Config { return &config.Config{Fleets: fl, MaxShips: 1} }},
	}
	s.holdUnlisted(OrderKey{"s", "o1"}, "ship-1", "p", nil)
	body := `{"fleet":"work","harness":"claude","dir":"` + t.TempDir() + `","drafting":true,"autopilot":true,"skill":{"kind":"skill","name":"wayfinder"}}`
	if rec := launchPost(t, s, body); rec.Code == http.StatusConflict || strings.Contains(strings.ToLower(rec.Body.String()), "autopilot") {
		t.Errorf("a drafting launch over the autopilot cap = %d %s, want it never refused over autopilot", rec.Code, rec.Body)
	}
}

func TestAShipReusingADraftingShipsNameIsNotADraftingShip(t *testing.T) {
	s := &Service{}
	s.markDrafting("ship-1", "2026-11-18T10:00:00Z", "work", "claude")
	if !s.IsDrafting("ship-1", "2026-11-18T10:00:00Z") {
		t.Fatal("the drafting ship does not read as drafting")
	}
	if s.IsDrafting("ship-1", "2026-11-18T11:00:00Z") {
		t.Error("a later ship with the same name reads as drafting")
	}
}
