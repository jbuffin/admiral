package cli

import "testing"

func TestParsePort(t *testing.T) {
	for in, ok := range map[string]bool{"7420": true, "65535": true,
		"1@evil.example": false, "0": false, "65536": false, "+80": false} {
		if _, err := parsePort(in); (err == nil) != ok {
			t.Errorf("parsePort(%q): err = %v, want ok = %v", in, err, ok)
		}
	}
}

func TestBadAdmiralPortFallsBack(t *testing.T) {
	t.Setenv("ADMIRAL_PORT", "1@evil.example")
	if got := defaultPort(); got != DefaultPort {
		t.Errorf("defaultPort() = %q with a bad ADMIRAL_PORT, want %q", got, DefaultPort)
	}
	t.Setenv("ADMIRAL_PORT", "8123")
	if got := defaultPort(); got != "8123" {
		t.Errorf("defaultPort() = %q, want 8123", got)
	}
}
