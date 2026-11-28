package conversation

import (
	"context"
	"time"
)

var ObserveUIServerWithin = func(ctx context.Context, port int, sessionID string, r *ServerReader, wait time.Duration) error {
	return observeUIServer(ctx, port, sessionID, r, nil, wait)
}

var (
	ReadClaudeCode = readClaudeCode
	ReadCopilot    = readCopilot
)
