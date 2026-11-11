// Package capabilities holds what each harness claims, the probe results kept in
// $ADMIRAL_HOME/capabilities.json and the harness version they are judged against.
package capabilities

import (
	"context"
	"errors"
	"time"

	"github.com/jbuffin/admiral/internal/config"
)

var errUnfinished = errors.New("capabilities: not implemented")

// FileName is the results file in $ADMIRAL_HOME.
const FileName = "capabilities.json"

// ID is a capability's stable identifier, used in the store and the API.
type ID string

const (
	JudgeNoTools      ID = "judge.no_tools"
	JudgeNoNetwork    ID = "judge.no_network"
	JudgeJSONSchema   ID = "judge.json_schema"
	JudgeCallCap      ID = "judge.call_cap"
	JudgeUsage        ID = "judge.usage_reports"
	BotMCPFence       ID = "bot.mcp_fence"
	BotConfigHome     ID = "bot.config_home"
	RestrictedSandbox ID = "restricted.sandbox_fails_closed"
	RestrictedAllow   ID = "restricted.network_allowlist"
	AutopilotGuard    ID = "autopilot.guard_hook"
	AutopilotArgs     ID = "autopilot.args_allowlist"
	AutopilotAdvisor  ID = "autopilot.advisor"
)

// Status of a capability for one fleet and harness.
type Status string

const (
	Passed     Status = "passed"
	Failed     Status = "failed"
	NotChecked Status = "not_checked"
)

// Capability is one row of the table.
type Capability struct {
	ID    ID
	Label string
}

// Feature groups the capabilities one Admiral extra needs.
type Feature struct {
	ID           string // judge, bot, restricted, autopilot
	Label        string
	Capabilities []Capability
}

// Claim says which versions of a harness claim a capability.
type Claim struct {
	From string // minimum version; empty means every version
}

// Table returns the built-in table, grouped by feature.
func Table() []Feature { return nil }

// Claims reports the claim a harness makes for a capability, and false when it makes none.
func Claims(harness string, id ID) (Claim, bool) { return Claim{}, false }

// ClaimedBy reports whether a harness at the given version claims the capability.
// The version may carry trailing text ("2.1.295 (Claude Code)").
func ClaimedBy(harness string, id ID, version string) bool { return false }

// Result is one probe's outcome.
type Result struct {
	Passed bool
	At     time.Time
	Detail string // one line
}

// Store reads and writes $ADMIRAL_HOME/capabilities.json. It holds no state of its own:
// every call reads the current file.
type Store struct{ home string }

// Open returns a store over home. It does no I/O.
func Open(home string) *Store { return &Store{home: home} }

// RecordVersion stores the last detected version of a harness in a fleet.
func (s *Store) RecordVersion(fleet, harness, version string, at time.Time) error {
	return errUnfinished
}

// Version returns the last detected version and when; "" when none was recorded.
// A corrupt file returns "" and the error.
func (s *Store) Version(fleet, harness string) (string, time.Time, error) {
	return "", time.Time{}, errUnfinished
}

// Record stores a result for the harness version that was probed.
func (s *Store) Record(fleet, harness, version string, id ID, r Result) error {
	return errUnfinished
}

// Result returns the stored result for the last detected version, if any.
func (s *Store) Result(fleet, harness string, id ID) (Result, bool, error) {
	return Result{}, false, errUnfinished
}

// Status computes the status against the last detected version. On a corrupt or
// unreadable file it returns NotChecked and the error.
func (s *Store) Status(fleet, harness string, id ID) (Status, error) {
	return NotChecked, errUnfinished
}

// Proven is true only when Status is Passed.
func (s *Store) Proven(fleet, harness string, id ID) bool {
	return false
}

// DetectVersion runs the harness's executable with --version under the fleet's env for
// that harness and returns the trimmed output. A missing executable, a nonzero exit or
// a timeout is an error.
func DetectVersion(ctx context.Context, fleet config.Fleet, harness string, timeout time.Duration) (string, error) {
	return "", errUnfinished
}
