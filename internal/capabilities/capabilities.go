// Package capabilities holds what each harness claims, the probe results kept in
// $ADMIRAL_HOME/capabilities.json and the harness version they are judged against.
package capabilities

import "time"

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

// Result is one probe's outcome.
type Result struct {
	Passed bool      `json:"passed"`
	At     time.Time `json:"at"`
	Detail string    `json:"detail,omitempty"` // one line
}
