package conversation

import (
	"bytes"
	"io"
	"os"
)

// The safety limits of reading a transcript. They are the chat's own, so the chat
// reads through here and looks the same.
const (
	maxLine    = 1 << 20   // a transcript line over this is skipped
	maxItems   = 200       // what is kept: the last this many items
	maxBytes   = 256 << 10 // and about this much text
	tailStart  = 8 << 20   // a first read of a larger file starts this far from its end
	readBudget = 16 << 20  // bytes read in one refresh; the rest waits for the next
	readChunk  = 256 << 10 // how much one read takes
)

// splitter cuts a stream into whole lines and hands each to fn. A line over
// maxLine is dropped without being held.
type splitter struct {
	pending []byte // the start of a line that has no newline yet
	skip    bool   // dropping the rest of an over-long line
}

func (s *splitter) feed(data []byte, fn func(line []byte)) {
	for len(data) > 0 {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			if !s.skip {
				s.pending = append(s.pending, data...)
				if len(s.pending) > maxLine {
					s.pending, s.skip = nil, true
				}
			}
			return
		}
		chunk := data[:i]
		data = data[i+1:]
		if s.skip {
			s.skip, s.pending = false, nil
			continue
		}
		line := chunk
		if len(s.pending) > 0 {
			line = append(s.pending, chunk...)
		}
		s.pending = nil
		if len(line) > maxLine {
			continue
		}
		fn(line)
	}
}

// decoded is what feeding lines to the Claude Code decoder has made so far.
type decoded struct {
	dec        *claudeDecoder
	keep       func(Item) bool
	trim       bool
	items      []Item
	bytes      int
	changed    []Item // new or updated since the caller last took them
	sawContent bool   // a line with something on it
	sawObject  bool   // a line that is a JSON object
}

func (c *decoded) line(raw []byte) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return
	}
	c.sawContent = true
	out, ok := c.dec.line(raw)
	if !ok {
		return
	}
	c.sawObject = true
	for _, e := range out {
		if c.keep != nil && !c.keep(e.Item) {
			continue
		}
		c.put(e)
	}
}

// put adds an item, or replaces the one it updates. An update of an item that is
// no longer held is dropped. Only the last maxItems within maxBytes are kept when
// trimming is on.
func (c *decoded) put(e emission) {
	at := -1
	for i := len(c.items) - 1; i >= 0; i-- {
		if c.items[i].ID == e.Item.ID {
			at = i
			break
		}
	}
	switch {
	case at >= 0:
		c.bytes += size(e.Item) - size(c.items[at])
		c.items[at] = e.Item
	case e.Update:
		return
	default:
		c.items = append(c.items, e.Item)
		c.bytes += size(e.Item)
	}
	for i := len(c.changed) - 1; i >= 0; i-- {
		if c.changed[i].ID == e.Item.ID {
			c.changed = append(c.changed[:i], c.changed[i+1:]...)
			break
		}
	}
	c.changed = append(c.changed, e.Item)
	if c.trim {
		for len(c.items) > 1 && (len(c.items) > maxItems || c.bytes > maxBytes) {
			c.bytes -= size(c.items[0])
			c.items = c.items[1:]
		}
	}
}

// size is the text an item holds, which maxBytes bounds.
func size(it Item) int {
	n := len(it.Text)
	if it.Tool != nil {
		n += len(it.Tool.Output) + len(it.Tool.Summary)
	}
	if it.Question != nil {
		n += len(it.Question.Text) + len(it.Question.Answer)
	}
	return n
}

func (c *decoded) result() ([]Item, error) {
	if c.sawContent && !c.sawObject {
		return nil, ErrUnreadable
	}
	return c.items, nil
}

// ReadClaudeCode reads a whole Claude Code transcript into items. A line the
// adapter does not know is skipped. It is ErrUnreadable when the stream has
// content and not one JSON object in it; an empty stream, or one of only
// metadata lines, is an empty conversation.
func ReadClaudeCode(r io.Reader) ([]Item, error) {
	c := &decoded{dec: newClaudeDecoder()}
	var s splitter
	buf := make([]byte, readChunk)
	for {
		n, err := r.Read(buf)
		s.feed(buf[:n], c.line)
		if err != nil {
			break
		}
	}
	if len(s.pending) > 0 && !s.skip {
		c.line(s.pending)
	}
	return c.result()
}

// Reader follows a Claude Code transcript file as it grows and keeps the tail of
// its conversation, within the safety limits. The caller has already checked the
// path (ADR 0019); Reader only opens it. It is not safe for concurrent use.
type Reader struct {
	c      decoded
	info   os.FileInfo // of the file read last, to tell a replaced file
	offset int64
	split  splitter
}

// Update is what one Refresh found.
type Update struct {
	// Items are the items new or filled in since the last Refresh, oldest first.
	Items []Item
	// Reset means the file was replaced or truncated: drop everything held before Items.
	Reset bool
}

// NewReader follows a transcript, keeping only the items keep says yes to, or
// all of them when keep is nil. TextOnly gives the chat's view.
func NewReader(keep func(Item) bool) *Reader {
	return &Reader{c: decoded{dec: newClaudeDecoder(), keep: keep, trim: true}}
}

// ToEnd makes the reader serve only what is written after size.
func (r *Reader) ToEnd(size int64) {
	r.offset, r.split = size, splitter{}
}

// Items is the conversation held: the last items of the file, oldest first.
func (r *Reader) Items() []Item { return append([]Item(nil), r.c.items...) }

// Refresh reads what has been added to the file at path. The error satisfies
// errors.Is(err, fs.ErrNotExist) when the file is missing or removed, and is
// ErrUnreadable when the file has content and nothing in it parses. A file
// larger than tailStart is read from tailStart before its end on the first
// refresh, and no more than readBudget bytes are read in one refresh; the rest
// waits for the next.
func (r *Reader) Refresh(path string) (Update, error) {
	f, err := os.Open(path)
	if err != nil {
		return Update{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return Update{}, err
	}
	var up Update
	if r.info != nil && (!os.SameFile(r.info, info) || info.Size() < r.offset) {
		r.c = decoded{dec: newClaudeDecoder(), keep: r.c.keep, trim: true}
		r.offset, r.split = 0, splitter{}
		up.Reset = true
		r.info = nil
	}
	first := r.info == nil
	r.info = info
	size := info.Size()
	if first && r.offset == 0 && size > tailStart {
		r.offset, r.split.skip = size-tailStart, true
	}
	buf := make([]byte, readChunk)
	for budget := readBudget; r.offset < size && budget > 0; {
		n, _ := f.ReadAt(buf, r.offset)
		if n == 0 {
			break
		}
		r.offset += int64(n)
		budget -= n
		r.split.feed(buf[:n], r.c.line)
	}
	up.Items, r.c.changed = r.c.changed, nil
	if _, err := r.c.result(); err != nil {
		return up, err
	}
	return up, nil
}

// ReadFile reads the tail of the Claude Code transcript at path in one refresh,
// with the limits Reader keeps. keep is as for NewReader.
func ReadFile(path string, keep func(Item) bool) ([]Item, error) {
	r := NewReader(keep)
	if _, err := r.Refresh(path); err != nil {
		return nil, err
	}
	return r.Items(), nil
}
