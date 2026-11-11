package conversation

import (
	"bytes"
	"io"
	"os"
)

// The safety limits of reading a transcript. The first six are the chat's own at
// main, so the chat reads through here and looks the same.
const (
	maxLine    = 1 << 20   // a transcript line over this is skipped
	maxItems   = 200       // what is kept: the last this many items
	chatBytes  = 256 << 10 // and about this much text, for the chat's text view
	tailStart  = 8 << 20   // a first read of a larger file starts this far from its end
	readBudget = 16 << 20  // bytes read in one refresh; the rest waits for the next
	readChunk  = 256 << 10 // how much one read takes

	// DefaultMaxBytes is what a reader that keeps tool calls and questions holds.
	DefaultMaxBytes = 4 << 20
	// ChatMaxBytes is what the chat's text view holds, as at main.
	ChatMaxBytes = chatBytes
)

// Options are the limits of a Reader and of Page. The zero value is the view a
// ship's conversation needs: every kind of item, the last 200 of them within
// DefaultMaxBytes, tool output cut to DefaultMaxOutput. The chat's view is
// TextView.
type Options struct {
	// Keep says which items to keep; nil keeps all. It runs before the count and
	// byte limits, so a text-only reader still holds its last MaxItems messages.
	Keep func(Item) bool
	// MaxItems is how many items are held, the last ones. Zero means 200.
	MaxItems int
	// MaxBytes is about how many bytes of text, tool output and question text are
	// held, the last ones. Zero means DefaultMaxBytes.
	MaxBytes int
	// MaxOutput is how long a tool call's output is kept before it is cut to a
	// preview with a marker. Zero means DefaultMaxOutput.
	MaxOutput int
}

// TextView is the chat's view: user and assistant text and notices, the last 200
// within 256 KB, the limits the chat has at main.
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

// held is the items kept so far. Items are numbered as they arrive; the front is
// number base, and byID gives an item's number, so an update finds its item and a
// trim from the front costs nothing per lookup.
type held struct {
	opts    Options
	trim    bool
	dec     *claudeDecoder
	items   []Item
	base    int
	byID    map[string]int
	bytes   int
	touched map[string]struct{} // ids new or updated since the caller last took them

	sawContent bool // a line with something on it
	sawObject  bool // a line that is a JSON object
}

func newHeld(o Options, trim bool) *held {
	h := &held{opts: o, trim: trim}
	h.reset()
	return h
}

func (h *held) reset() {
	h.dec = newClaudeDecoder(h.opts.MaxOutput)
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
	for _, e := range out {
		if !e.Update && h.opts.Keep != nil && !h.opts.Keep(e.Item) {
			continue
		}
		h.put(e)
	}
}

// put adds an item, or merges an update into the item it fills in. An update of an
// item that is no longer held is dropped. When trimming, only the last MaxItems
// within MaxBytes are kept.
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

// merge puts what a result carries into the item it answers.
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

// size is what an item holds that the byte limit bounds: all its text.
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

// changed is the held items that are new or updated since the last call, oldest
// first. It is bounded by what is held, whatever the refresh read.
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

// readClaudeCode reads a whole stream with no limits on what it holds: for the
// tests, which read a recorded sample. A real file goes through Reader or Page.
func readClaudeCode(r io.Reader) ([]Item, error) {
	h := decode(r, Options{}, false)
	return h.items, h.result()
}

// decode feeds a stream to a decoder. It holds every item, with no trimming.
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

// Reader follows a Claude Code transcript file as it grows and keeps the tail of
// its conversation within its Options. The caller opens the file (ADR 0019: its
// path checks, its os.Root, O_NONBLOCK) and hands the open file over; Reader never
// opens a path. It is not safe for concurrent use.
type Reader struct {
	h      *held
	info   os.FileInfo // of the file read last, to tell a replaced file
	offset int64
	split  splitter
}

// Update is what one Refresh found.
type Update struct {
	// Items are the held items new or filled in since the last Refresh, oldest
	// first: never more than the reader holds.
	Items []Item
	// Reset means the file was replaced or truncated: drop everything held before Items.
	Reset bool
}

// NewReader follows a transcript with the limits in o.
func NewReader(o Options) *Reader { return &Reader{h: newHeld(o, true)} }

// ToEnd makes the reader serve only what is written after size.
func (r *Reader) ToEnd(size int64) {
	r.offset, r.split = size, splitter{}
}

// Clear empties what the reader holds and moves it to size, the file's current
// size: the chat's /clear. What is written after that is served.
func (r *Reader) Clear(size int64) {
	r.h.reset()
	r.ToEnd(size)
}

// Items is the conversation held: the last items of the file, oldest first.
func (r *Reader) Items() []Item { return append([]Item(nil), r.h.items...) }

// Refresh reads what has been added to f, the file the caller opened, and info,
// its FileInfo. ErrUnreadable means the file has content and nothing in it
// parses; an empty file or one of only metadata lines is an empty conversation.
// Whether the file is missing is the caller's to tell, from its open. A file
// larger than tailStart is read from tailStart before its end on the first
// refresh, and no more than readBudget bytes are read in one refresh; the rest
// waits for the next. A file smaller than the offset, or a different one,
// starts over and the Update says Reset.
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

// Page is up to n items before the item with id before, oldest first, for
// ?before=<id>; with before empty it is the n newest. more says there are items
// older than the first one returned within what Page reads. It reads the same
// window a Reader's first refresh does, from tailStart before the end of the
// file (the whole file when smaller) for at most readBudget bytes, and holds none
// of it afterwards. So paging reaches back that far and no further: an id older
// than the window is ErrUnknownID, like one that never existed. Items are decoded
// as for a Reader with Options o, without trimming, so ids and contents match
// what a Reader serves. f and info are as for Refresh; the caller opened f.
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
