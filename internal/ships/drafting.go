package ships

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jbuffin/admiral/internal/inbox"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/sessionhost"
	"github.com/jbuffin/admiral/internal/transcriptfs"
)

// draftState is what Admiral holds of one drafting ship, in memory only: the ship can rewrite its own pane options
// and anything under $ADMIRAL_HOME, so what it filed is what Admiral followed, not what the ship says.
type draftState struct {
	run      sync.Mutex // serialises reads of the ref, follows and the end item
	mu       sync.Mutex // guards the fields below; never held across a follow
	followed string     // the issue URL followed from this ship's ref
	notice   string
	handled  string // the turn end or end whose ref was last read
	ended    bool
}

func draftKey(session, first string) string { return session + "\x00" + first }

// markDrafting records a drafting ship by session and its first launch, so a woken ship stays one and a later ship that reuses the name is not.
func (s *Service) markDrafting(session, first string) {
	s.drafters.Store(draftKey(session, first), &draftState{})
}

// IsDrafting is whether the ship of this session and first launch stamp was launched as a drafting ship since Admiral started.
func (s *Service) IsDrafting(session, first string) bool { return s.draftOf(session, first) != nil }

func (s *Service) draftOf(session, first string) *draftState {
	v, ok := s.drafters.Load(draftKey(session, first))
	if !ok {
		return nil
	}
	return v.(*draftState)
}

// view is whether the ship still drafts with no campaign followed, and the line about the last ref it wrote.
func (d *draftState) view() (drafting bool, notice string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.followed == "", d.notice
}

func (s *Service) showDrafting(sh *Ship) {
	if d := s.draftOf(sh.Session, sh.FirstLaunched); d != nil {
		sh.Drafting, sh.DraftingNotice = d.view()
	}
}

// issueLabel is owner/repo#n for an issue URL that issueRef matched.
func issueLabel(url string) string {
	m := issueRef.FindStringSubmatch(url)
	if m == nil {
		return url
	}
	return m[1] + "#" + url[strings.LastIndex(url, "/")+1:]
}

// applyRef follows the issue a ship wrote, once: a later ref naming another issue is ignored and one the follow refuses
// is shown on the ship. The caller holds d.run.
func (s *Service) applyRef(d *draftState, fleet, line string) {
	if issueRef.FindStringSubmatch(line) == nil || s.FollowDrafted == nil {
		return
	}
	d.mu.Lock()
	followed := d.followed
	d.mu.Unlock()
	if followed != "" {
		if !strings.EqualFold(followed, line) {
			d.mu.Lock()
			d.notice = fmt.Sprintf("Ignored %s: this ship already follows %s.", issueLabel(line), issueLabel(followed))
			d.mu.Unlock()
			s.broadcast()
		}
		return
	}
	err := s.FollowDrafted(fleet, line)
	d.mu.Lock()
	if err != nil {
		d.notice = err.Error()
	} else {
		d.followed, d.notice = line, ""
	}
	d.mu.Unlock()
	s.broadcast()
}

// readDraftRef reads and removes what the ship wrote at its campaign-ref since the last read.
func (s *Service) readDraftRef(session string) string {
	dir, ok := s.handDir(session)
	if !ok {
		return ""
	}
	line, _ := readRefLine(filepath.Join(dir, launcher.CampaignRefFile))
	return line
}

// draftTurnKey names the end a drafting ship is at, "" while it works; a new end has a new key.
func (s *Service) draftTurnKey(sess sessionhost.Session, now time.Time) string {
	switch st := status(sess, now); {
	case st == StatusAnchored || st == StatusSailingOff:
		return st
	case st == StatusNeedsYou && reasonOf(sess) == "stop":
		since := sess.Options[sessionhost.OptStopSince]
		s.mu.Lock()
		if tr := s.tracks[sess.Name]; tr != nil && !tr.Since.IsZero() {
			since = tr.Since.UTC().Format(time.RFC3339Nano)
		}
		s.mu.Unlock()
		if since != "" {
			return "stop " + since
		}
	}
	return ""
}

// advanceDrafting reads each drafting ship's ref once at each turn end, at anchoring and at sailing off, so GitHub is
// asked only when the ship wrote something. It runs before the moor and sail-off steps, which would otherwise take the ship first.
func (s *Service) advanceDrafting() {
	now := time.Now()
	for _, sess := range s.Host.Sessions() {
		if !sess.IsShip() {
			continue
		}
		d := s.draftOf(sess.Name, firstLaunched(sess.Options))
		if d == nil {
			continue
		}
		key := s.draftTurnKey(sess, now)
		d.mu.Lock()
		skip := key == "" || key == d.handled || d.ended
		if !skip {
			d.handled = key
		}
		d.mu.Unlock()
		if skip {
			continue
		}
		name, fleet := sess.Name, sess.Options["fleet"]
		s.settleWG.Add(1)
		go func() {
			defer s.settleWG.Done()
			d.run.Lock()
			defer d.run.Unlock()
			if line := s.readDraftRef(name); line != "" {
				s.applyRef(d, fleet, line)
			}
		}()
	}
}

// draftEnd is what a drafting ship's end needs from the ship while it is still there: the last ref and the conversation binding.
type draftEnd struct {
	d     *draftState
	fleet string
	item  inbox.DraftingEnded
	ref   string
}

// captureDraftEnd is nil unless the ship is a drafting ship with no campaign followed. It reads the ref and the binding now,
// since the ship's files and pane go with its end.
func (s *Service) captureDraftEnd(sess sessionhost.Session) *draftEnd {
	d := s.draftOf(sess.Name, firstLaunched(sess.Options))
	if d == nil {
		return nil
	}
	d.mu.Lock()
	skip := d.followed != "" || d.ended
	d.mu.Unlock()
	if skip {
		return nil
	}
	o := sess.Options
	e := &draftEnd{d: d, fleet: o["fleet"], ref: s.readDraftRef(sess.Name)}
	e.item = inbox.DraftingEnded{Ship: sess.Name, Name: o["name"], Fleet: o["fleet"], Harness: o["harness"]}
	if e.item.Name == "" {
		e.item.Name = sess.Name
	}
	if e.item.Harness == "" {
		e.item.Harness = "claude"
	}
	if raw, cmd, _, ok := s.conversationBinding(sess); ok {
		e.item.Transcript, e.item.StartCmd, e.item.ConfigDir = raw, cmd, s.dockConfig(o["fleet"], o["harness"])
	}
	return e
}

// followEndRef follows the ref the ship wrote last. It reports whether the ship still has no campaign.
func (s *Service) followEndRef(e *draftEnd) bool {
	e.d.run.Lock()
	defer e.d.run.Unlock()
	if e.ref != "" {
		s.applyRef(e.d, e.fleet, e.ref)
		e.ref = ""
	}
	drafting, _ := e.d.view()
	return drafting
}

// postDraftEnded tells the engineer a drafting ship ended with no campaign followed, once.
func (s *Service) postDraftEnded(e *draftEnd) {
	e.d.run.Lock()
	defer e.d.run.Unlock()
	e.d.mu.Lock()
	skip := e.d.followed != "" || e.d.ended
	e.d.ended = true
	e.d.mu.Unlock()
	if skip {
		return
	}
	if _, err := inbox.WriteDraftingEnded(s.Launcher.Home, e.item); err != nil {
		fmt.Printf("admiral: ship %s: the ended-without-a-campaign item cannot be written: %v\n", e.item.Ship, err)
	}
}

// endDrafted follows the last ref and posts the item off the caller's goroutine: the follow reads GitHub, and the stop route holds the ship lock under a short deadline.
func (s *Service) endDrafted(e *draftEnd) {
	if e == nil {
		return
	}
	s.settleWG.Add(1)
	go func() {
		defer s.settleWG.Done()
		if s.followEndRef(e) {
			s.postDraftEnded(e)
		}
		s.broadcast()
	}()
}

// dropDrafting ends the drafting record, as a hand to Commodore does: its relaunch carries the first launch stamp.
func (s *Service) dropDrafting(session, first string) { s.drafters.Delete(draftKey(session, first)) }

// conversationBinding is the ship's transcript option and start command, only when the start command binds the option to the conversation it gave the harness.
func (s *Service) conversationBinding(sess sessionhost.Session) (raw, startCmd, id string, ok bool) {
	if sess.PaneID == "" {
		return "", "", "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	host := sessionhost.NewServer(s.Launcher.Tmux, s.Launcher.Socket)
	raw, rerr := host.ReadPaneOption(ctx, sess.PaneID, "transcript")
	cmd, cerr := host.ReadPaneStartCommand(ctx, sess.PaneID)
	if rerr != nil || cerr != nil {
		return "", "", "", false
	}
	_, id, ok = transcriptfs.BindFor("claude", raw, cmd)
	return raw, cmd, id, ok
}
