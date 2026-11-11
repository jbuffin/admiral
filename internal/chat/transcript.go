// Package chat starts the AdmiralBot `_chat` session, follows the transcript Claude Code writes
// for it, and serves the conversation.
package chat

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/jbuffin/admiral/internal/sessionhost"
)

type Message struct {
	ID   string `json:"id"`
	Role string `json:"role"` // "user" or "assistant"
	Text string `json:"text"`
	At   string `json:"at"`
}

const (
	maxLine     = 1 << 20   // a transcript line over this is skipped
	maxMessages = 200       // what is served: the last this many messages
	maxBytes    = 256 << 10 // and about this much text
	maxText     = 64 << 10  // one message is cut here
	tailStart   = 8 << 20   // a first read of a larger file starts this far from its end
	readBudget  = 16 << 20  // bytes read in one refresh; the rest waits for the next
)

// The transcript format is undocumented: anything else in a line is ignored.
type transcriptLine struct {
	Type        string `json:"type"`
	UUID        string `json:"uuid"`
	Timestamp   string `json:"timestamp"`
	IsMeta      bool   `json:"isMeta"`
	IsSidechain bool   `json:"isSidechain"`
	// Claude Code's compaction summary is a user line that only the transcript shows.
	IsCompactSummary          bool `json:"isCompactSummary"`
	IsVisibleInTranscriptOnly bool `json:"isVisibleInTranscriptOnly"`
	Message                   struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// The local-command lines `/clear` leaves are not messages.
func parseLine(line []byte) (Message, bool) {
	var l transcriptLine
	if json.Unmarshal(line, &l) != nil || l.UUID == "" || l.IsMeta || l.IsSidechain || l.IsCompactSummary || l.IsVisibleInTranscriptOnly {
		return Message{}, false
	}
	if l.Type != "user" && l.Type != "assistant" {
		return Message{}, false
	}
	text := contentText(l.Message.Content)
	if l.Type == "user" {
		head := strings.TrimLeft(text, " \t\r\n")
		if strings.HasPrefix(head, "<command-name>") || strings.HasPrefix(head, "<local-command-") {
			return Message{}, false
		}
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return Message{}, false
	}
	if len(text) > maxText {
		cut := maxText
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		text = text[:cut] + "…"
	}
	return Message{ID: l.UUID, Role: l.Type, Text: text, At: l.Timestamp}, true
}

func contentText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n\n")
}

// A path that is only not there yet is a plain not-exist error.
var errRefused = errors.New("not a regular transcript file under the Claude config directory's projects/")

// Anything in the chat session can write the path, so nothing between projects/ and the file
// may be a symlink. The config directory (a dotfiles ~/.claude, say) and projects/ may be.
func resolveTranscript(configDir, p string) (root, rel string, err error) {
	if !sessionhost.ValidTranscriptPath(p) {
		return "", "", errRefused
	}
	p = filepath.Clean(p)
	root, err = filepath.EvalSymlinks(filepath.Join(configDir, "projects"))
	if err != nil {
		return "", "", err
	}
	parts := []string{filepath.Base(p)}
	for cur := filepath.Dir(p); ; cur = filepath.Dir(cur) {
		if real, err := filepath.EvalSymlinks(cur); err == nil && real == root {
			break
		}
		if filepath.Dir(cur) == cur {
			return "", "", errRefused
		}
		parts = append([]string{filepath.Base(cur)}, parts...)
	}
	at := root
	for i, part := range parts {
		at = filepath.Join(at, part)
		fi, err := os.Lstat(at)
		switch {
		case err != nil:
			return "", "", err
		case fi.Mode()&os.ModeSymlink != 0:
			return "", "", errRefused
		case i < len(parts)-1 && !fi.IsDir():
			return "", "", errRefused
		case i == len(parts)-1 && !fi.Mode().IsRegular():
			return "", "", errRefused
		}
	}
	return root, filepath.Join(parts...), nil
}

// A line over maxLine is dropped without being held.
type follower struct {
	info    os.FileInfo // of the file read last, to tell a replaced file
	offset  int64
	pending []byte // the start of a line that has no newline yet
	skip    bool   // dropping the rest of an over-long line
}

func (f *follower) toEnd(size int64) {
	f.offset, f.pending, f.skip = size, nil, false
}

// A file smaller than the offset, or a different one, starts over, after reset.
func (f *follower) read(file *os.File, info os.FileInfo, add func(Message), reset func()) {
	if f.info != nil && (!os.SameFile(f.info, info) || info.Size() < f.offset) {
		*f = follower{}
		reset()
	}
	first := f.info == nil
	f.info = info
	size := info.Size()
	if first && f.offset == 0 && size > tailStart {
		f.offset, f.skip = size-tailStart, true
	}
	buf := make([]byte, 256<<10)
	for budget := readBudget; f.offset < size && budget > 0; {
		n, _ := file.ReadAt(buf, f.offset)
		if n == 0 {
			break
		}
		f.offset += int64(n)
		budget -= n
		f.feed(buf[:n], add)
	}
}

func (f *follower) feed(data []byte, add func(Message)) {
	for len(data) > 0 {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			if !f.skip {
				f.pending = append(f.pending, data...)
				if len(f.pending) > maxLine {
					f.pending, f.skip = nil, true
				}
			}
			return
		}
		chunk := data[:i]
		data = data[i+1:]
		if f.skip {
			f.skip, f.pending = false, nil
			continue
		}
		line := chunk
		if len(f.pending) > 0 {
			line = append(f.pending, chunk...)
		}
		f.pending = nil
		if len(line) > maxLine {
			continue
		}
		if m, ok := parseLine(line); ok {
			add(m)
		}
	}
}
