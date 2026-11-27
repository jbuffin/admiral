package ships

import (
	"os"
	"testing"
	"time"
)

// The rigs end real ships, so production's tick and sail-off delay would be most of each test's time.
func TestMain(m *testing.M) {
	tick = 25 * time.Millisecond
	SailOffDelay = 200 * time.Millisecond
	os.Exit(m.Run())
}

// settleHold outlasts every pass that could still act on a ship: the sail-off delay and a few ticks after it.
func settleHold() time.Duration { return SailOffDelay + 4*tick }
