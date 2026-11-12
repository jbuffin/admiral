package shipconv

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"strconv"
	"time"

	"github.com/jbuffin/admiral/internal/conversation"
	"github.com/jbuffin/admiral/internal/moor"
	"github.com/jbuffin/admiral/internal/ships"
)

// maxFilesPerPage bounds the transcripts one page opens, so a long history of empty rounds cannot fan out.
const maxFilesPerPage = 8

// A before id names a recorded round when it is that round's divider ("r2") or one of its items ("r2:<id>").
var roundID = regexp.MustCompile(`^r(\d+)(?::(.+))?$`)

func (s *Service) mooredOf(session string) (ships.Ship, bool) {
	if s.moored != nil {
		return s.moored(session)
	}
	if s.Ships == nil {
		return ships.Ship{}, false
	}
	return s.Ships.Moored(session)
}

func (s *Service) roundsOf(sh ships.Ship) ([]moor.Round, error) {
	if s.rounds != nil {
		return s.rounds(sh)
	}
	if s.Ships == nil {
		return nil, nil
	}
	return s.Ships.Rounds(sh.Name)
}

func (s *Service) fleetDirOf(fleet string) (string, bool) {
	if s.fleetDir != nil {
		return s.fleetDir(fleet)
	}
	return s.configDir(ships.Ship{Fleet: fleet}, "claude")
}

func (s *Service) liveRoundOf(sh ships.Ship) int {
	if s.liveRound != nil {
		return s.liveRound(sh)
	}
	if s.Ships == nil {
		return 0
	}
	return s.Ships.Round(sh.Session)
}

// segment is one round's part of a stitched conversation.
type segment struct {
	round   int
	started time.Time
	open    func() (*os.File, os.FileInfo, string) // a state when the file cannot be shown
}

// segmentsOf lists the rounds to stitch, oldest first: a moored ship's recorded rounds, or a live ship's recorded rounds then its own.
// It is nil when the ship has nothing to stitch (a live ship in its first round).
func (s *Service) segmentsOf(sh ships.Ship, live bool) []segment {
	liveRound := 0
	if live {
		if liveRound = s.liveRoundOf(sh); liveRound < 1 {
			return nil
		}
	}
	hist, err := s.roundsOf(sh)
	if err != nil {
		fmt.Fprintf(os.Stderr, "admiral: ship %s: its round history cannot be read: %v\n", sh.Name, err)
	}
	if !live && len(hist) == 0 {
		return nil
	}
	segs := []segment{}
	for _, r := range hist {
		if live && r.Round >= liveRound {
			continue
		}
		segs = append(segs, segment{round: r.Round, started: r.Started, open: func() (*os.File, os.FileInfo, string) { return s.openRecorded(r) }})
	}
	if live {
		loc, _, none := s.locateShip(sh, "")
		seg := segment{round: liveRound}
		seg.started, _ = time.Parse(time.RFC3339, sh.Launched)
		seg.open = func() (*os.File, os.FileInfo, string) {
			if none { // a Claude round's option may not be bound yet right after a wake: a divider and no state
				if sh.Harness != "claude" {
					return nil, nil, StateNone
				}
				return nil, nil, ""
			}
			f, info, _, err := loc.open(true)
			switch {
			case errors.Is(err, fs.ErrNotExist):
				return nil, nil, "" // the round has not written yet: a divider and no items
			case err != nil:
				return nil, nil, StateUnreadable
			}
			return f, info, ""
		}
		segs = append(segs, seg)
	}
	return segs
}

// openRecorded opens a recorded round's file, confined under its fleet's projects dir.
func (s *Service) openRecorded(r moor.Round) (*os.File, os.FileInfo, string) {
	if r.Transcript == "" {
		return nil, nil, StateNone
	}
	dir, ok := s.fleetDirOf(r.Fleet)
	if !ok {
		return nil, nil, StateUnreadable
	}
	f, info, _, err := where{configDir: dir, path: r.Transcript, id: r.SessionID}.open(true)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, nil, StateGone
	case err != nil:
		return nil, nil, StateUnreadable
	}
	return f, info, ""
}

// prefixed gives an item of round n the id it has across rounds; round 0 keeps its own.
func prefixed(n int, items []conversation.Item) []conversation.Item {
	if n < 1 {
		return items
	}
	out := make([]conversation.Item, len(items))
	for i, it := range items {
		it.ID = idIn(n, it.ID)
		out[i] = it
	}
	return out
}

func idIn(n int, id string) string {
	if n < 1 {
		return id
	}
	return "r" + strconv.Itoa(n) + ":" + id
}

func divider(seg segment, state string) conversation.Item {
	d := conversation.Item{ID: "r" + strconv.Itoa(seg.round), Kind: conversation.KindRound, Round: seg.round, State: state}
	if !seg.started.IsZero() {
		d.At = seg.started.UTC().Format(time.RFC3339Nano)
	}
	return d
}

// readSegment reads up to limit items of a round before an id of its own ("" for its newest).
func readSegment(seg segment, before string, limit int) (items []conversation.Item, more bool, state string, err error) {
	f, info, state := seg.open()
	if f == nil {
		return nil, false, state, nil
	}
	defer f.Close()
	items, more, err = conversation.Page(f, info, conversation.Options{}, before, limit)
	switch {
	case errors.Is(err, conversation.ErrUnknownID):
		return nil, false, "", err
	case err != nil:
		return nil, false, StateUnreadable, nil
	}
	return prefixed(seg.round, items), more, "", nil
}

// stitch pages the rounds newest first: it opens an older round only while the page has room.
// A before id in a recorded round N >= 1 is read in that round; any other id is round 0's.
func stitch(segs []segment, before string) (Page, error) {
	page := Page{State: StateOK, Items: []conversation.Item{}}
	at, inner := len(segs)-1, ""
	if before != "" {
		routed := false
		if m := roundID.FindStringSubmatch(before); m != nil {
			n, _ := strconv.Atoi(m[1])
			for i, seg := range segs {
				if n < 1 || seg.round != n {
					continue
				}
				routed = true
				at, inner = i, m[2]
				if m[2] == "" { // the divider: what comes before it is the round before
					at = i - 1
				}
			}
		}
		if !routed {
			at = -1
			for i, seg := range segs {
				if seg.round == 0 {
					at, inner = i, before
				}
			}
			if at < 0 {
				return page, conversation.ErrUnknownID
			}
		}
	}

	remaining, opened := pageSize, 0
	var chunks [][]conversation.Item
	i := at
	for ; i >= 0 && remaining > 0; i-- {
		if opened == maxFilesPerPage {
			page.More = true
			break
		}
		opened++
		seg := segs[i]
		b := ""
		if i == at {
			b = inner
		}
		items, more, state, err := readSegment(seg, b, remaining)
		if err != nil {
			return page, err
		}
		remaining -= len(items)
		if !more && seg.round >= 1 {
			if remaining == 0 { // no room for the divider: the next page opens with it
				more = true
			} else {
				items = append([]conversation.Item{divider(seg, state)}, items...)
				remaining--
			}
		}
		chunks = append(chunks, items)
		if more {
			page.More = true
			break
		}
	}
	if i >= 0 && remaining == 0 {
		page.More = true
	}
	for c := len(chunks) - 1; c >= 0; c-- {
		page.Items = append(page.Items, chunks[c]...)
	}
	return page, nil
}
