package conversation

import "context"

// Placeholders until the observer exists.

type ServerReader struct{}

func NewServerReader(o Options) *ServerReader { return &ServerReader{} }

func (r *ServerReader) Feed(msg []byte) {}

func (r *ServerReader) Items() []Item { return nil }

func ObserveUIServer(ctx context.Context, port int, sessionID string, r *ServerReader) error {
	return nil
}
