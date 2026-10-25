// Package chat is the AdmiralBot chat session on Admiral's side (ADR 0018,
// 0019): it starts the `_chat` tmux session, follows the transcript Claude Code
// writes for it, and serves the conversation, the state and the way to send.
package chat

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// Message is one turn of text in the conversation.
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

// transcriptLine is the part of a Claude Code transcript line Admiral reads. The
// format is undocumented (ADR 0019): anything else in a line is ignored.
type transcriptLine struct {
	Type        string `json:"type"`
	UUID        string `json:"uuid"`
	Timestamp   string `json:"timestamp"`
	IsMeta      bool   `json:"isMeta"`
	IsSidechain bool   `json:"isSidechain"`
	Message     struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// parseLine is the message a transcript line carries, if it carries one: the
// text of a user turn or of an assistant turn's text block. Tool use and results,
// thinking, meta and sidechain lines, system lines, a line with no uuid and the
// local-command lines `/clear` leaves are not messages.
func parseLine(line []byte) (Message, bool) {
	var l transcriptLine
	if json.Unmarshal(line, &l) != nil || l.UUID == "" || l.IsMeta || l.IsSidechain {
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

// contentText is a message's text: the content when it is a string, else its
// text blocks joined by a blank line.
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

// errRefused is a transcript path Admiral will not read; a path that is only not
// there yet is a plain not-exist error.
var errRefused = errors.New("not a regular transcript file under the Claude config directory's projects/")

// resolveTranscript is the transcript at path p, if it may be read. The path comes
// from a pane option that anything in the chat session can write, so it must be
// absolute, end in .jsonl and lie under <configDir>/projects/, and neither the
// file nor a directory between projects/ and it may be a symlink. The config
// directory itself (a dotfiles ~/.claude, say) and projects/ may be links: the
// path is matched against the real projects/, found by walking up from the file.
// It returns the real projects/ and the path below it.
func resolveTranscript(configDir, p string) (root, rel string, err error) {
	if !filepath.IsAbs(p) || !strings.HasSuffix(p, ".jsonl") || len(p) > 4096 ||
		strings.IndexFunc(p, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
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

// follower reads a transcript as it grows: from the offset it stopped at, whole
// lines only. A line over maxLine is dropped without being held.
type follower struct {
	info    os.FileInfo // of the file read last, to tell a replaced file
	offset  int64
	pending []byte // the start of a line that has no newline yet
	skip    bool   // dropping the rest of an over-long line
}

// toEnd makes the follower serve only what is written after now.
func (f *follower) toEnd(size int64) {
	f.offset, f.pending, f.skip = size, nil, false
}

// read reads what has been added to file (opened from the path in info) and
// calls add for each message. A file smaller than the offset, or a different one,
// starts over, and reset runs first so the caller can drop what it kept.
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

// feed splits data into lines and parses each whole one.
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
