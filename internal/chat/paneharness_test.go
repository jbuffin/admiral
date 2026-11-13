package chat

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/sessionhost"
)

// The flush, the scheduled reset and New chat's clear are typed into the pane that is running, so they follow the
// harness that pane was launched under, not what the config says now.

// hostedChat runs the rig's fake Claude chat under a live host. A config harness other than claude swaps the
// service's config for one that names it.
func hostedChat(t *testing.T, harness string) (*Service, *fleetChat) {
	t.Helper()
	l, home, _ := chatRig(t)
	if err := l.StartChat("work"); err != nil {
		t.Fatal(err)
	}
	svc := serviceFor(t, l, home)
	model := map[string]string{"claude": "opus", "codex": "gpt-6.1-sol"}[harness]
	{
		raw, _ := json.Marshal(map[string]any{"version": 1, "fleets": map[string]any{"work": map[string]any{
			"name": "Work", "claude": map[string]any{}, "codex": map[string]any{},
			"chat": map[string]any{"harness": harness, "model": model, "effort": "high"}}}})
		cfg, err := config.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		svc.Config = func() *config.Config { return cfg }
	}
	svc.Host = sessionhost.New(l.Tmux, l.Socket)
	ctx, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	go svc.Host.Run(ctx)
	select {
	case <-svc.Host.Ready():
	case <-time.After(10 * time.Second):
		t.Fatal("the host never read the sessions")
	}
	return svc, svc.chat("work")
}

// record sets the harness option the session carries ("" removes it) and reads it back into the chat's state.
func record(t *testing.T, svc *Service, fc *fleetChat, harness string) {
	t.Helper()
	target := "=" + sessionhost.ChatSession("work") + ":"
	args := []string{"-S", svc.Socket, "set", "-t", target, sessionhost.ChatHarnessOption, harness}
	if harness == "" {
		args = []string{"-S", svc.Socket, "set", "-u", "-t", target, sessionhost.ChatHarnessOption}
	}
	if out, err := exec.Command(svc.Tmux, args...).CombinedOutput(); err != nil {
		t.Fatalf("recording the harness: %v: %s", err, out)
	}
	read(t, fc, harness)
}

func read(t *testing.T, fc *fleetChat, want string) {
	t.Helper()
	sess, ok := fc.session()
	if !ok {
		t.Fatal("the chat is not running")
	}
	opts, err := fc.readOptions(sess.PaneID)
	if err != nil {
		t.Fatal(err)
	}
	if opts.harness != want {
		t.Fatalf("the session records harness %q, want %q", opts.harness, want)
	}
	fc.mu.Lock()
	fc.st.pane, fc.st.opts = sess.PaneID, opts
	fc.mu.Unlock()
}

func (s *fleetChat) tracked() bool {
	s.rst.mu.Lock()
	defer s.rst.mu.Unlock()
	return s.rst.pane != ""
}

func (s *fleetChat) clears(f config.Fleet) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st.opts.matches(f)
}

func TestALaunchedClaudeChatRecordsItsHarnessAndIsTrackedForResets(t *testing.T) {
	svc, fc := hostedChat(t, "claude")
	read(t, fc, "claude")
	fc.resetTick(time.Now())
	if !fc.tracked() {
		t.Fatal("a Claude pane on a Claude fleet is not tracked for scheduled resets, so the cases below prove nothing")
	}
	f, _ := svc.Config().Fleet("work")
	if !fc.clears(f) {
		t.Error("New chat would restart a session launched with the fleet's own settings, want a clear")
	}
}

func TestNoScheduledResetOrClearReachesAPaneThatRecordsNoHarnessOrAnotherOne(t *testing.T) {
	for name, tc := range map[string]struct{ config, recorded string }{
		"a Claude pane started before the harness was recorded": {"claude", ""},
		"a Codex pane on a fleet switched to Claude":            {"claude", "codex"},
		"a Copilot pane on a fleet switched to Claude":          {"claude", "copilot"},
		"a Claude pane on a fleet switched to Codex":            {"codex", "claude"},
		"a Codex pane on a Codex fleet":                         {"codex", "codex"},
	} {
		t.Run(name, func(t *testing.T) {
			svc, fc := hostedChat(t, tc.config)
			record(t, svc, fc, tc.recorded)
			fc.resetTick(time.Now().Add(25 * time.Hour))
			if fc.tracked() {
				t.Error("the tick tracked the pane for a flush and a reset, want none")
			}
			if f, _ := svc.Config().Fleet("work"); fc.clears(f) {
				t.Error("New chat would clear this session, want it restarted")
			}
		})
	}
}

// pasted is whether the flush prompt's opening words are on the pane's screen.
func pasted(t *testing.T, svc *Service, fc *fleetChat) bool {
	t.Helper()
	sess, _ := fc.session()
	out, err := exec.Command(svc.Tmux, "-S", svc.Socket, "capture-pane", "-p", "-J", "-t", sess.PaneID).CombinedOutput()
	if err != nil {
		t.Fatalf("capture-pane: %v: %s", err, out)
	}
	return strings.Contains(string(out), strings.Fields(FlushPrompt)[0]+" "+strings.Fields(FlushPrompt)[1])
}

func TestNewChatFlushesTheRunningPanesHarnessNotTheConfigs(t *testing.T) {
	t.Setenv("ADMIRAL_CHAT_FLUSH_WAIT", "200ms")
	idle := Snapshot{State: "idle"}
	for name, tc := range map[string]struct {
		config, recorded string
		want             bool
	}{
		"a Claude pane the fleet switched away from is flushed before the kill": {"codex", "claude", true},
		"a Codex pane the fleet switched to Claude is not":                      {"claude", "codex", false},
		"a pane that predates the record is not":                                {"claude", "", false},
	} {
		t.Run(name, func(t *testing.T) {
			svc, fc := hostedChat(t, tc.config)
			record(t, svc, fc, tc.recorded)
			fc.flushIfIdle(idle)
			if got := pasted(t, svc, fc); got != tc.want {
				t.Errorf("flush prompt on the pane = %v, want %v", got, tc.want)
			}
		})
	}
}
