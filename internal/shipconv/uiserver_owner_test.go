package shipconv

import (
	"net"
	"os"
	"os/exec"
	"testing"
)

func TestStartedSessionIgnoresAnOrdersText(t *testing.T) {
	other := "11111111-2222-4333-8444-555555555555"
	for name, c := range map[string]struct{ cmd, want string }{
		"flag":              {"'/bin/copilot' '--session-id' '" + sid + "' '--' 'go'", sid},
		"in an order":       {"'/bin/copilot' '--interactive=fix --session-id " + other + "' '--session-id' '" + sid + "'", sid},
		"only in an order":  {"'/bin/copilot' '--interactive=fix --session-id " + other + "'", ""},
		"after the order's": {"copilot -i 'a' --session-id " + sid, sid},
	} {
		if got := startedSession(c.cmd); got != c.want {
			t.Errorf("%s: startedSession = %q, want %q", name, got, c.want)
		}
	}
}

func TestDescendsFrom(t *testing.T) {
	parents := map[int]int{30: 20, 20: 10, 10: 2, 99: 98, 98: 2}
	parent := func(p int) (int, bool) { n, ok := parents[p]; return n, ok }
	for name, c := range map[string]struct {
		pids []int
		root int
		want bool
	}{
		"the pane itself":    {[]int{10}, 10, true},
		"a grandchild":       {[]int{30}, 10, true},
		"another pane's":     {[]int{99}, 10, false},
		"one of two is not":  {[]int{30, 99}, 10, false},
		"no listener":        {nil, 10, false},
		"an unknown process": {[]int{77}, 10, false},
		"init is no pane":    {[]int{30}, 1, false},
	} {
		if got := descendsFrom(c.pids, c.root, parent); got != c.want {
			t.Errorf("%s: descendsFrom = %v, want %v", name, got, c.want)
		}
	}
}

func TestListenerBelongsToTheProcessThatListens(t *testing.T) {
	if _, err := exec.LookPath("lsof"); err != nil {
		t.Skip("no lsof")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	if !listenerBelongsTo(port, os.Getpid()) {
		t.Error("a listener in the pane's own process was refused")
	}
	other := exec.Command("sleep", "30")
	if err := other.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Process.Kill(); _ = other.Wait() }()
	if listenerBelongsTo(port, other.Process.Pid) {
		t.Error("a listener outside the pane's process tree was accepted")
	}
}
