package sources

import (
	"context"
	"encoding/json"
)

// EventKind is one kind of event a source can raise; Panel is set when the kind belongs to one of its panels.
type EventKind struct {
	Kind  string `json:"kind"`
	Title string `json:"title"`
	Panel string `json:"panel,omitempty"`
}

// Event is one outside event as a source sent it. A source's own tier is never read.
type Event struct {
	Kind     string
	Panel    string
	Ref      string
	Actor    string
	Author   string
	Viewer   string // the engineer's login, filled by the built-in GitHub source only
	Verified bool   // true only for events the built-in GitHub source saw itself
}

// maxEventsPerMinute bounds one source's delivered events in a fixed one-minute window.
const maxEventsPerMinute = 60

// maxEventField is the longest ref, actor or author a source may send.
const maxEventField = 512

// Events is the source's event catalog; ok is false when it has none (not asked, or it answered method-not-found).
func (r *runner) Events() (kinds []EventKind, ok bool) { return nil, false }

// pullCatalog asks the source for events.catalog; any failure leaves it with no catalog.
func (r *runner) pullCatalog(ctx context.Context, p conn) {}

// handleEvent validates one `event` notification against the catalog and delivers it to onEvent.
func (r *runner) handleEvent(params json.RawMessage) {}
