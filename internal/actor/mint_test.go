package actor_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/actor"
)

func minter(t *testing.T, home string) *actor.Minter {
	t.Helper()
	m, err := actor.NewMinter(home)
	if err != nil {
		t.Fatalf("NewMinter: %v", err)
	}
	return m
}

func TestATokenFollowsItsFleetAndItsChatSession(t *testing.T) {
	m := minter(t, t.TempDir())
	base := m.Token("work", "nonce-1")
	if len(base) < 32 {
		t.Fatalf("token %q is too short to be a secret", base)
	}
	if again := m.Token("work", "nonce-1"); again != base {
		t.Errorf("same fleet and chat session gave %q then %q, want one token", base, again)
	}
	if other := m.Token("personal", "nonce-1"); other == base {
		t.Errorf("two fleets share the token %q", base)
	}
	if next := m.Token("work", "nonce-2"); next == base {
		t.Errorf("a new chat session kept the token %q", base)
	}
}

func TestATokenSurvivesARestartOfAdmiral(t *testing.T) {
	home := t.TempDir()
	before := minter(t, home).Token("work", "nonce-1")
	if after := minter(t, home).Token("work", "nonce-1"); after != before || before == "" {
		t.Errorf("token after a restart = %q, want the same %q", after, before)
	}
}

func TestTwoHomesMintDifferentTokens(t *testing.T) {
	a := minter(t, t.TempDir()).Token("work", "nonce-1")
	b := minter(t, t.TempDir()).Token("work", "nonce-1")
	if a == "" || a == b {
		t.Errorf("tokens %q and %q, want a different one per home", a, b)
	}
}

func TestTheSecretIsOnlyInAPrivateFileAndNeverInTheToken(t *testing.T) {
	home := t.TempDir()
	tok := minter(t, home).Token("work", "nonce-1")
	var files int
	err := filepath.Walk(home, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		files++
		if info.Mode().Perm() != 0o600 {
			t.Errorf("%s has mode %v, want 0600", p, info.Mode().Perm())
		}
		b, _ := os.ReadFile(p)
		if strings.Contains(string(b), tok) {
			t.Errorf("%s holds the token", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if files != 1 {
		t.Errorf("minting left %d files under the home, want the one secret file", files)
	}
}
