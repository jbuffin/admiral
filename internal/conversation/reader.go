package conversation

import (
	"bytes"
	"io"
	"os"
)

// The safety limits of reading a transcript; the chat's own, so it looks the same read through here.
const (
	maxLine    = 1 << 20 // a line over this is skipped
	maxItems   = 200     // the last this many items are kept
	chatBytes  = 256 << 10
	tailStart  = 8 << 20  // a first read of a larger file starts this far from its end
	readBudget = 16 << 20 // bytes read in one refresh; the rest waits for the next
	readChunk  = 256 << 10

	DefaultMaxBytes = 4 << 20
	ChatMaxBytes    = chatBytes
)

// Options are the limits of a Reader and of Page. The zero value is the ship's view: every kind of item.
type Options struct {
	// Keep runs before the count and byte limits, so a text-only reader still holds its last MaxItems messages.
	Keep func(Item) bool
	// MaxItems zero means 200.
	MaxItems int
	// MaxBytes bounds the text, tool output and question text held; zero means DefaultMaxBytes.
	MaxBytes int
	// MaxOutput zero means DefaultMaxOutput.
	MaxOutput int
	// Harness picks the decoder: "copilot" reads events.jsonl, anything else Claude Code's transcript.
	Harness string
}

// TextView is the chat's view: user and assistant text and notices, the last 200 within 256 KB.
func TextView() Options { return Options{Keep: TextOnly, MaxBytes: ChatMaxBytes} }

func (o Options) items() int {
	if o.MaxItems <= 0 {
		return maxItems
	}
	return o.MaxItems
}

func (o Options) bytes() int {
	if o.MaxBytes <= 0 {
		return DefaultMaxBytes
	}
	return o.MaxBytes
}

// splitter hands whole lines to fn and drops a line over maxLine without holding it.
type splitter struct {
	pending []byte
	skip    bool // dropping the rest of an over-long line
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

// held numbers items as they arrive so a trim from the front keeps byID valid: the front is number base.
type held struct {
	opts    Options
	trim    bool
	dec     decoder
	items   []Item
	base    int
	byID    map[string]int
	bytes   int
	touched map[string]struct{} // ids new or updated since the caller last took them

	sawContent bool
	sawObject  bool
}

func newHeld(o Options, trim bool) *held {
	h := &held{opts: o, trim: trim}
	h.reset()
	return h
}

func (h *held) reset() {
	h.dec = newDecoder(h.opts)
	h.items, h.base, h.bytes = nil, 0, 0
	h.byID, h.touched = map[string]int{}, map[string]struct{}{}
	h.sawContent, h.sawObject = false, false
}

func (h *held) line(raw []byte) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return
	}
	h.sawContent = true
	out, ok := h.dec.line(raw)
	if !ok {
		return
	}
	h.sawObject = true
	h.emit(out)
}

func (h *held) emit(out []emission) {
	for _, e := range out {
		if !e.Update && h.opts.Keep != nil && !h.opts.Keep(e.Item) {
			continue
		}
		h.put(e)
	}
}

// put drops an update of an item no longer held.
func (h *held) put(e emission) {
	n, have := h.byID[e.Item.ID]
	switch {
	case have && e.Update:
		cur := &h.items[n-h.base]
		before := size(*cur)
		merge(cur, e.Item)
		h.bytes += size(*cur) - before
	case e.Update:
		return
	case have:
		h.bytes += size(e.Item) - size(h.items[n-h.base])
		h.items[n-h.base] = e.Item
	default:
		h.byID[e.Item.ID] = h.base + len(h.items)
		h.items = append(h.items, e.Item)
		h.bytes += size(e.Item)
	}
	h.touched[e.Item.ID] = struct{}{}
	if !h.trim {
		return
	}
	for len(h.items) > 1 && (len(h.items) > h.opts.items() || h.bytes > h.opts.bytes()) {
		gone := h.items[0]
		h.bytes -= size(gone)
		delete(h.byID, gone.ID)
		delete(h.touched, gone.ID)
		h.items = h.items[1:]
		h.base++
	}
}

func merge(cur *Item, up Item) {
	if cur.Tool != nil && up.Tool != nil {
		t := *cur.Tool
		t.Output, t.Error, t.Done = up.Tool.Output, up.Tool.Error, up.Tool.Done
		cur.Tool = &t
	}
	if cur.Question != nil && up.Question != nil {
		q := *cur.Question
		q.Answer, q.Pending = up.Question.Answer, false
		cur.Question = &q
	}
}

func size(it Item) int {
	n := len(it.Text)
	if it.Tool != nil {
		n += len(it.Tool.Name) + len(it.Tool.Summary) + len(it.Tool.Output)
	}
	if it.Question != nil {
		n += len(it.Question.Text) + len(it.Question.Answer)
		for _, o := range it.Question.Options {
			n += len(o.Label) + len(o.Description)
		}
	}
	return n
}

// changed is bounded by what is held, whatever the refresh read.
func (h *held) changed() []Item {
	var out []Item
	if len(h.touched) > 0 {
		for _, it := range h.items {
			if _, ok := h.touched[it.ID]; ok {
				out = append(out, it)
			}
		}
	}
	h.touched = map[string]struct{}{}
	return out
}

func (h *held) result() error {
	if h.sawContent && !h.sawObject {
		return ErrUnreadable
	}
	return nil
}

// readClaudeCode holds everything it reads, so only the tests use it.
func readClaudeCode(r io.Reader) ([]Item, error) {
	h := decode(r, Options{}, false)
	return h.items, h.result()
}

// readCopilot holds everything it reads, so only the tests use it.
func readCopilot(r io.Reader) ([]Item, error) {
	h := decode(r, Options{Harness: "copilot"}, false)
	return h.items, h.result()
}

func decode(r io.Reader, o Options, skipFirst bool) *held {
	h := newHeld(o, false)
	s := splitter{skip: skipFirst}
	buf := make([]byte, readChunk)
	for {
		n, err := r.Read(buf)
		s.feed(buf[:n], h.line)
		if err != nil {
			break
		}
	}
	if len(s.pending) > 0 && !s.skip {
		h.line(s.pending)
	}
	return h
}

// Reader follows a transcript as it grows and keeps its tail within its Options. The
// caller opens the file, so the path checks stay with it; Reader never opens a path.
// It is not safe for concurrent use.
type Reader struct {
	h      *held
	info   os.FileInfo // to tell a replaced file
	offset int64
	split  splitter
}

type Update struct {
	// Items are the held items new or filled in since the last Refresh, oldest first.
	Items []Item
	// Reset means the file was replaced or truncated: drop everything held before Items.
	Reset bool
}

func NewReader(o Options) *Reader { return &Reader{h: newHeld(o, true)} }

func (r *Reader) ToEnd(size int64) {
	r.offset, r.split = size, splitter{}
}

// Clear is the chat's /clear: it drops what is held and serves only what is written after size.
func (r *Reader) Clear(size int64) {
	r.h.reset()
	r.ToEnd(size)
}

// Offset is how far into the file the Reader has read.
func (r *Reader) Offset() int64 { return r.offset }

func (r *Reader) Items() []Item { return append([]Item(nil), r.h.items...) }

// Refresh reads what was added to f since the last call. A file larger than tailStart
// is read from tailStart before its end the first time, and at most readBudget bytes
// are read per call. A file that shrank or was replaced starts over and says Reset.
func (r *Reader) Refresh(f *os.File, info os.FileInfo) (Update, error) {
	var up Update
	if r.info != nil && (!os.SameFile(r.info, info) || info.Size() < r.offset) {
		r.h.reset()
		r.offset, r.split = 0, splitter{}
		r.info = nil
		up.Reset = true
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
		r.split.feed(buf[:n], r.h.line)
	}
	up.Items = r.h.changed()
	return up, r.h.result()
}

// Page is up to n items before the item with id before, oldest first; with before
// empty it is the n newest. It reads the window a Reader's first refresh does, so an id
// older than that is ErrUnknownID, like one that never existed.
func Page(f *os.File, info os.FileInfo, o Options, before string, n int) (items []Item, more bool, err error) {
	size := info.Size()
	var start int64
	if size > tailStart {
		start = size - tailStart
	}
	h := decode(io.NewSectionReader(f, start, readBudget), o, start > 0)
	if err := h.result(); err != nil {
		return nil, false, err
	}
	end := len(h.items)
	if before != "" {
		at, ok := h.byID[before]
		if !ok {
			return nil, false, ErrUnknownID
		}
		end = at - h.base
	}
	from := end - n
	if from < 0 || n <= 0 {
		from = 0
	}
	if n <= 0 {
		end = 0
	}
	return append([]Item(nil), h.items[from:end]...), from > 0, nil
}
