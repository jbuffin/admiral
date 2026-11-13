package capabilities

import (
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/config"
)

func chatConfig(t *testing.T, probe config.ChatProbe) (*config.Config, config.Fleet) {
	t.Helper()
	cfg, err := config.Parse([]byte(`{"version":1,"fleets":{"w":{"name":"W","claude":{},"codex":{},"copilot":{}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	cfg.ChatProbe = probe
	f, _ := cfg.Fleet("w")
	return cfg, f
}

func option(t *testing.T, cfg *config.Config, f config.Fleet, harness string) config.ChatHarnessOption {
	t.Helper()
	for _, o := range cfg.ChatHarnessOptions(f) {
		if o.Harness == harness {
			return o
		}
	}
	t.Fatalf("no option for %s", harness)
	return config.ChatHarnessOption{}
}

func label(t *testing.T, id ID) string {
	t.Helper()
	for _, f := range Table() {
		for _, c := range f.Capabilities {
			if c.ID == id {
				return c.Label
			}
		}
	}
	t.Fatalf("%s is not in the table", id)
	return ""
}

// config repeats the AdmiralBot capabilities' ids and labels, since it cannot import this package.
func TestChatReasonsUseTheTablesIDsAndLabels(t *testing.T) {
	store := Open(t.TempDir())
	now := time.Now()
	if err := store.RecordVersion("w", "codex", "1.0.0", now); err != nil {
		t.Fatal(err)
	}
	cfg, f := chatConfig(t, ChatProbe(store))
	for _, id := range []ID{BotMCPFence, BotConfigHome} {
		if id == BotConfigHome {
			if err := store.Record("w", "codex", "1.0.0", BotMCPFence, Result{Passed: true, At: now}); err != nil {
				t.Fatal(err)
			}
		}
		if err := store.Record("w", "codex", "1.0.0", id, Result{Passed: false, At: now, Detail: "broke"}); err != nil {
			t.Fatal(err)
		}
		o := option(t, cfg, f, "codex")
		if o.Offered || o.Capability != string(id) {
			t.Errorf("%s failed: offered %v, capability %q", id, o.Offered, o.Capability)
		}
		if want := label(t, id); !strings.Contains(o.Reason, want) || !strings.Contains(o.Reason, "broke") {
			t.Errorf("%s failed: reason %q, want the table's label %q and the detail", id, o.Reason, want)
		}
	}
}

func TestAHarnessVersionChangeTurnsCodexOff(t *testing.T) {
	store := Open(t.TempDir())
	now := time.Now()
	if err := store.RecordVersion("w", "codex", "1.0.0", now); err != nil {
		t.Fatal(err)
	}
	for _, id := range []ID{BotMCPFence, BotConfigHome} {
		if err := store.Record("w", "codex", "1.0.0", id, Result{Passed: true, At: now}); err != nil {
			t.Fatal(err)
		}
	}
	cfg, f := chatConfig(t, ChatProbe(store))
	if o := option(t, cfg, f, "codex"); !o.Offered {
		t.Fatalf("codex with both probes passed on its version: %+v", o)
	}
	if err := store.RecordVersion("w", "codex", "2.0.0", now); err != nil {
		t.Fatal(err)
	}
	if o := option(t, cfg, f, "codex"); o.Offered || !strings.Contains(o.Reason, "has not been checked") {
		t.Errorf("codex on a version nobody probed: %+v, want it off as not checked", o)
	}
	if err := store.RecordVersionFailure("w", "codex", "no answer", now); err != nil {
		t.Fatal(err)
	}
	if o := option(t, cfg, f, "codex"); o.Offered {
		t.Errorf("codex whose version could not be detected: %+v, want it off", o)
	}
}
