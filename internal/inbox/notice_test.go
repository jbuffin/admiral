package inbox

import "testing"

func TestNoticeOnlyFromAdmiralSession(t *testing.T) {
	b := []byte(`{"version":1,"id":"n-1","kind":"notice","created":"2026-11-10T00:00:00Z","title":"t","notice":{"body":"b"}}`)
	if _, err := parseItem(b, AdmiralSession, "n-1"); err != nil {
		t.Errorf("notice from _admiral: %v", err)
	}
	if _, err := parseItem(b, "some-ship", "n-1"); err == nil {
		t.Error("a notice in a ship's inbox was accepted")
	}
}
