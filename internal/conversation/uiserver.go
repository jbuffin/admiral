package conversation

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ServerReader builds a conversation from the events a Copilot --ui-server pushes. It holds what the
// events.jsonl decoder would, plus the assistant text of a turn still running. It is safe for concurrent use.
type ServerReader struct {
	mu   sync.Mutex
	h    *held
	dec  *copilotDecoder
	live map[string]*liveText // messageId -> the reply's text so far
}

type liveText struct {
	b    strings.Builder
	full bool // over the cap: the file has the whole reply, so no more is held
}

func NewServerReader(o Options) *ServerReader {
	o.Harness = "copilot"
	return &ServerReader{h: newHeld(o, true), dec: newCopilotDecoder(o.MaxOutput), live: map[string]*liveText{}}
}

// Feed takes one JSON-RPC message from the server. Anything that is not a session event is ignored.
func (r *ServerReader) Feed(msg []byte) { r.feed("", msg) }

// feed ignores events of other sessions when session is set.
func (r *ServerReader) feed(session string, msg []byte) {
	var m struct {
		Method string `json:"method"`
		Params struct {
			SessionID string `json:"sessionId"`
			Event     struct {
				Type      string          `json:"type"`
				ID        string          `json:"id"`
				AgentID   string          `json:"agentId"`
				Timestamp string          `json:"timestamp"`
				Data      json.RawMessage `json:"data"`
			} `json:"event"`
		} `json:"params"`
	}
	if json.Unmarshal(msg, &m) != nil || m.Method != "session.event" || m.Params.Event.Type == "" {
		return
	}
	if session != "" && m.Params.SessionID != session {
		return
	}
	ev := m.Params.Event
	r.mu.Lock()
	defer r.mu.Unlock()
	switch ev.Type {
	case "assistant.message_delta":
		var d copilotData
		// A subagent's text folds into its parent's tool call, as in the file.
		if json.Unmarshal(ev.Data, &d) != nil || ev.AgentID != "" || d.ParentToolCallID != "" || d.MessageID == "" || d.DeltaContent == "" {
			return
		}
		lt := r.live[d.MessageID]
		if lt == nil {
			lt = &liveText{}
			r.live[d.MessageID] = lt
		}
		if lt.full {
			return
		}
		if lt.b.Len()+len(d.DeltaContent) > maxText*2 {
			lt.full = true
			return
		}
		lt.b.WriteString(d.DeltaContent)
		if text := strings.TrimSpace(lt.b.String()); text != "" {
			r.h.emit([]emission{{Item: Item{ID: d.MessageID, Kind: KindAssistant, At: ev.Timestamp, Text: cutTo(text, maxText, "…")}}})
		}
		return
	case "assistant.message":
		var d copilotData
		if json.Unmarshal(ev.Data, &d) == nil {
			delete(r.live, d.MessageID)
		}
	case "assistant.turn_end", "abort", "session.error":
		// Text that never got its final message is not held; the tab keeps what it showed.
		clear(r.live)
	}
	line, err := json.Marshal(copilotLine{Type: ev.Type, ID: ev.ID, Timestamp: ev.Timestamp, Data: ev.Data})
	if err != nil {
		return
	}
	if out, ok := r.dec.line(line); ok {
		r.h.emit(out)
	}
}

// Items is everything held, oldest first.
func (r *ServerReader) Items() []Item {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Item(nil), r.h.items...)
}

// Changed is the held items new or filled in since the last call.
func (r *ServerReader) Changed() []Item {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.h.changed()
}

const (
	handshakeWait  = 5 * time.Second
	maxFrame       = 8 << 20
	maxHeaderLine  = 1 << 10
	maxHeaderBlock = 8 << 10
)

// ObserveUIServer reads the events of one session from the Copilot --ui-server on loopback port into r, until
// the server goes away (an error) or ctx ends (nil). It is read-only: it sends connect, session.resume without
// a resume event, and session.detach, and nothing in reply to what the server sends. It has no code that answers.
func ObserveUIServer(ctx context.Context, port int, sessionID string, r *ServerReader) error {
	return ObserveUIServerChecked(ctx, port, sessionID, r, nil)
}

// ObserveUIServerChecked is ObserveUIServer with verify run once connect has succeeded and before the session id
// is sent. If it says no, the connection is dropped and the error is returned.
func ObserveUIServerChecked(ctx context.Context, port int, sessionID string, r *ServerReader, verify func() bool) error {
	d := net.Dialer{Timeout: handshakeWait}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return err
	}
	defer conn.Close()
	br := bufio.NewReader(conn)

	_ = conn.SetDeadline(time.Now().Add(handshakeWait))
	// The server accepts connect with no token and ignores a wrong one, so none is sent.
	if err := call(conn, br, 1, "connect", map[string]any{}, sessionID, r); err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	if verify != nil && !verify() {
		return errors.New("the listener is not the ship's own")
	}
	// disableResume keeps the server from recording a resume of a session the engineer is in.
	if err := call(conn, br, 2, "session.resume", map[string]any{"sessionId": sessionID, "disableResume": true}, sessionID, r); err != nil {
		return fmt.Errorf("session.resume: %w", err)
	}
	_ = conn.SetDeadline(time.Time{})

	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.SetWriteDeadline(time.Now().Add(time.Second))
			_ = writeFrame(conn, map[string]any{"jsonrpc": "2.0", "id": 3, "method": "session.detach", "params": map[string]any{"sessionId": sessionID}})
			conn.Close()
		case <-stop:
		}
	}()
	for {
		body, err := readFrame(br)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		r.feed(sessionID, body)
	}
}

// call sends one request and reads until its response, feeding the events that arrive meanwhile.
func call(conn net.Conn, br *bufio.Reader, id int, method string, params map[string]any, session string, r *ServerReader) error {
	if err := writeFrame(conn, map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		return err
	}
	for {
		body, err := readFrame(br)
		if err != nil {
			return err
		}
		var m struct {
			ID     *int            `json:"id"`
			Method string          `json:"method"`
			Error  json.RawMessage `json:"error"`
		}
		if json.Unmarshal(body, &m) != nil {
			continue
		}
		if m.ID != nil && m.Method == "" && *m.ID == id {
			if len(m.Error) > 0 && !bytes.Equal(m.Error, []byte("null")) {
				return errors.New(string(m.Error))
			}
			return nil
		}
		r.feed(session, body)
	}
}

func writeFrame(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "Content-Length: %d\r\n\r\n%s", len(b), b)
	return err
}

func readFrame(br *bufio.Reader) ([]byte, error) {
	n := -1
	block := 0
	for {
		line, err := readHeaderLine(br)
		if err != nil {
			return nil, err
		}
		if block += len(line); block > maxHeaderBlock {
			return nil, errors.New("frame header too long")
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		if v, ok := strings.CutPrefix(strings.ToLower(line), "content-length:"); ok {
			n, err = strconv.Atoi(strings.TrimSpace(v))
			if err != nil {
				return nil, err
			}
		}
	}
	if n < 0 || n > maxFrame {
		return nil, fmt.Errorf("bad frame length %d", n)
	}
	body := make([]byte, n)
	_, err := io.ReadFull(br, body)
	return body, err
}

// readHeaderLine reads one header line, refusing one that runs past maxHeaderLine.
func readHeaderLine(br *bufio.Reader) (string, error) {
	var line []byte
	for {
		b, err := br.ReadByte()
		if err != nil {
			return "", err
		}
		if b == '\n' {
			return string(line), nil
		}
		if line = append(line, b); len(line) > maxHeaderLine {
			return "", errors.New("frame header line too long")
		}
	}
}
