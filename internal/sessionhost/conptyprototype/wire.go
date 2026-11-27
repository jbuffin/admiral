//go:build windows

package main

// The host's pipe protocol (PROTOTYPE). A connection opens with one JSON line
// (a request). attach then switches to frames both ways: a type byte, a
// big-endian uint32 length, the payload.

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os/user"
	"sync"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

const (
	frameOutput = 'o' // host→bridge: pane output bytes
	frameEvent  = 'e' // host→bridge: JSON event (bell, title, mode, query, exit, size)
	frameInput  = 'i' // bridge→host: bytes for the pane
	frameResize = 'r' // bridge→host: {"cols","rows"}
)

type request struct {
	Op     string            `json:"op"` // spawn, list, attach, kill, dump, stop
	Pane   string            `json:"pane,omitempty"`
	Name   string            `json:"name,omitempty"`
	Argv   []string          `json:"argv,omitempty"`
	Dir    string            `json:"dir,omitempty"`
	Env    map[string]string `json:"env,omitempty"`
	Cols   int               `json:"cols,omitempty"`
	Rows   int               `json:"rows,omitempty"`
	Answer string            `json:"answer,omitempty"` // "host" (default) or "browser": who answers terminal queries
	Snap   string            `json:"snap,omitempty"`   // "render" (tmux-style screen+scrollback) or "replay" (tuios snapshot.Replay bytes)
}

func pipeName() string {
	u, _ := user.Current()
	name := "user"
	if u != nil {
		name = u.Username
	}
	return `\\.\pipe\admiral-proto-876-` + sanitize(name)
}

func sanitize(s string) string {
	b := []rune(s)
	for i, r := range b {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			b[i] = '_'
		}
	}
	return string(b)
}

// pipeSDDL grants only the current user (protected DACL, no inheritance).
func pipeSDDL() (string, error) {
	tok := windows.GetCurrentProcessToken()
	tu, err := tok.GetTokenUser()
	if err != nil {
		return "", err
	}
	return "D:P(A;;GA;;;" + tu.User.Sid.String() + ")", nil
}

func listen() (io.Closer, func() (io.ReadWriteCloser, error), error) {
	sddl, err := pipeSDDL()
	if err != nil {
		return nil, nil, err
	}
	l, err := winio.ListenPipe(pipeName(), &winio.PipeConfig{SecurityDescriptor: sddl, InputBufferSize: 1 << 16, OutputBufferSize: 1 << 16})
	if err != nil {
		return nil, nil, err
	}
	return l, func() (io.ReadWriteCloser, error) { return l.Accept() }, nil
}

type frameWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (f *frameWriter) write(t byte, p []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	var h [5]byte
	h[0] = t
	binary.BigEndian.PutUint32(h[1:], uint32(len(p)))
	if _, err := f.w.Write(h[:]); err != nil {
		return err
	}
	_, err := f.w.Write(p)
	return err
}

func (f *frameWriter) event(v any) error {
	b, _ := json.Marshal(v)
	return f.write(frameEvent, b)
}

func readFrame(r *bufio.Reader) (byte, []byte, error) {
	var h [5]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(h[1:])
	if n > 64<<20 {
		return 0, nil, fmt.Errorf("frame too big: %d", n)
	}
	p := make([]byte, n)
	_, err := io.ReadFull(r, p)
	return h[0], p, err
}
