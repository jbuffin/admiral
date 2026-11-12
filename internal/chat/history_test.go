package chat

import "testing"

func TestHistoryWindowIsTwoHundred(t *testing.T) {
	if HistoryWindow != 200 {
		t.Fatalf("HistoryWindow = %d, want 200: docs/api.md and ADR 0019 name it", HistoryWindow)
	}
}
