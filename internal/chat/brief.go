package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"time"

	"github.com/jbuffin/admiral/internal/brief"
	"github.com/jbuffin/admiral/internal/campaigns/follow"
	"github.com/jbuffin/admiral/internal/chatlog"
	"github.com/jbuffin/admiral/internal/digest"
	"github.com/jbuffin/admiral/internal/inbox"
	"github.com/jbuffin/admiral/internal/ledger"
	"github.com/jbuffin/admiral/internal/sessionhost"
	"github.com/jbuffin/admiral/internal/ships"
)

const briefFile = "brief.md"

// tickState is what a fleet's tick remembers between ticks. The ledger is read again only when its files change.
type tickState struct {
	text      string // the brief as last written
	wrote     bool
	ledgerSig string
	instr     string
	notes     []ledger.Entry
	commits   []ledger.Commitment
	ledgerOK  bool

	ackedTo   uint64 // the printed sequence the digest was last acked to
	baselined bool   // ships, Inbox items and answers seen once, so what was already there is no event
	shipState map[string]string
	inboxSeen map[string]bool
	answered  map[string]bool
}

// tickInputs is what one tick reads from outside the chat, once for every fleet.
type tickInputs struct {
	ships     []ships.Ship
	campaigns []follow.Followed
	inbox     []inbox.Item
}

func (s *Service) tickInputs() tickInputs {
	var in tickInputs
	if s.Ships != nil {
		in.ships = s.Ships()
	}
	if s.Campaigns != nil {
		in.campaigns = s.Campaigns()
	}
	if s.Inbox != nil {
		in.inbox = s.Inbox()
	}
	return in
}

// Tick renders each Claude Code fleet's brief and keeps its digest: only its hooks print them, so another
// harness's chat gets neither.
func (s *Service) Tick() {
	in := s.tickInputs()
	for _, fc := range s.onFleets() {
		if fc.harness() != "claude" {
			continue
		}
		fc.renderBrief(in)
		fc.digestTick(in)
	}
}

// tickLoop ticks until ctx ends, each tick after the last has finished.
func (s *Service) tickLoop(ctx context.Context) {
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.Tick()
			s.ResetTick()
		}
	}
}

// printedSeq is the highest digest sequence the fleet's chat has printed. It comes from the pane options the chat
// read already, so it costs no tmux call and is 0 for a chat that isn't running.
func (s *fleetChat) printedSeq() uint64 {
	if s.DigestAcked != nil {
		return s.DigestAcked(s.key)
	}
	s.mu.Lock()
	v := s.st.opts.digest
	s.mu.Unlock()
	if !sessionhost.SafeToken.MatchString(v) {
		return 0
	}
	n, _ := strconv.ParseUint(v, 10, 64)
	return n
}

// ledgerSig names the state of the ledger's files, so an unchanged pair is not read again.
func (s *fleetChat) ledgerSig() string {
	dir, err := chatlog.OpenFleetDir(s.Home, s.key, false)
	if err != nil {
		return "none"
	}
	defer dir.Close()
	sig := ""
	for _, name := range []string{"ledger.jsonl", "instructions.md"} {
		fi, err := dir.Lstat(name)
		if err != nil {
			sig += "|-"
			continue
		}
		sig += fmt.Sprintf("|%d@%d", fi.Size(), fi.ModTime().UnixNano())
	}
	return sig
}

func (s *fleetChat) renderBrief(ti tickInputs) {
	s.tmu.Lock()
	defer s.tmu.Unlock()
	st := &s.tk
	if sig := s.ledgerSig(); !st.ledgerOK || sig != st.ledgerSig {
		instr, ierr := s.notes().Instructions(s.key)
		notes, nerr := s.notes().Notes(s.key)
		commits, cerr := s.notes().Commitments(s.key)
		if ierr != nil || nerr != nil || cerr != nil {
			return
		}
		st.instr, st.notes, st.commits, st.ledgerSig, st.ledgerOK = instr, notes, commits, sig, true
	}
	in := brief.Input{Instructions: st.instr, Now: time.Now()}
	for _, c := range st.commits {
		bc := brief.Commitment{ID: c.ID, Text: c.Text, Trigger: c.Trigger.Kind, Subject: c.Trigger.Subject, Action: c.Action, TS: c.TS, Waiting: c.Waiting, Launched: c.Launched, WaitingChildren: c.WaitingChildren}
		if c.Trigger.Kind == ledger.TriggerCampaign {
			for _, fc := range ti.campaigns {
				if fc.ID != c.Trigger.Subject {
					continue
				}
				if bc.Campaign = campaignRef(fc.URL); fc.Tree != nil && fc.Tree.Title != "" {
					bc.Campaign = fc.Tree.Title
				}
			}
		}
		in.Commitments = append(in.Commitments, bc)
	}
	for _, n := range st.notes {
		in.Notes = append(in.Notes, brief.Note{Text: n.Text, Via: n.Via, TS: n.TS})
	}
	// questions() is oldest first; the brief wants the newest first.
	qs := s.questions(false)
	for i := len(qs) - 1; i >= 0; i-- {
		if q := qs[i]; len(q.Answer) == 0 {
			in.Questions = append(in.Questions, brief.Question{Text: questionTitle(q), Asked: q.Item.Created})
		}
	}
	for _, sh := range ti.ships {
		if sh.Fleet == s.key {
			in.Ships = append(in.Ships, brief.Ship{Name: sh.Name, Status: sh.Status, Via: sh.Via, Title: sh.Title})
		}
	}
	for _, c := range ti.campaigns {
		if c.Fleet == s.key && !c.Ended() {
			name := c.ID
			if c.Tree != nil && c.Tree.Title != "" {
				name += " " + c.Tree.Title
			}
			in.Campaigns = append(in.Campaigns, brief.Campaign{Name: name + " " + c.URL})
		}
	}
	if recent, err := s.history().Recent(s.key, 50); err == nil {
		for _, e := range recent {
			in.Entries = append(in.Entries, brief.Entry{Kind: e.Kind, Role: e.Role, Text: e.Text})
		}
	}
	text := brief.Render(in)
	if st.wrote && text == st.text {
		return
	}
	if err := writeBrief(s.Home, s.key, text); err != nil {
		return
	}
	st.text, st.wrote = text, true
}

func questionTitle(q Question) string {
	var title string
	_ = json.Unmarshal(q.Item.Field("title"), &title)
	return title
}

// writeBrief replaces brief.md through a temp file renamed into place, so a link planted at the path is never
// followed; no text removes it.
func writeBrief(home, fleet, text string) error {
	dir, err := chatlog.OpenFleetDir(home, fleet, text != "")
	if errors.Is(err, fs.ErrNotExist) && text == "" {
		return nil
	} else if err != nil {
		return err
	}
	defer dir.Close()
	if text == "" {
		if err := dir.Remove(briefFile); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	const tmp = briefFile + ".tmp"
	_ = dir.Remove(tmp)
	f, err := dir.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = f.WriteString(text)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = dir.Rename(tmp, briefFile)
	}
	if err != nil {
		_ = dir.Remove(tmp)
	}
	return err
}

// digestTick drops the lines the chat has printed and adds a line for each event since the last tick. A ship, Inbox
// item or answer found on the first tick is the baseline: after a restart, what happened meanwhile is lost, and the
// brief carries the current state.
func (s *fleetChat) digestTick(ti tickInputs) {
	s.tmu.Lock()
	defer s.tmu.Unlock()
	st := &s.tk
	// The digest file is read only when the chat's printed sequence has moved.
	if seq := s.printedSeq(); seq != st.ackedTo {
		st.ackedTo = seq
		if seq > 0 {
			_ = digest.Ack(s.Home, s.key, seq)
		}
	}
	var lines []string
	add := func(text string) {
		if st.baselined {
			lines = append(lines, text)
		}
	}

	if st.answered == nil {
		st.answered = map[string]bool{}
	}
	shipsNow := map[string]string{}
	for _, sh := range ti.ships {
		if sh.Fleet != s.key {
			continue
		}
		shipsNow[sh.Session] = sh.Status
		if prev := st.shipState[sh.Session]; prev != sh.Status {
			if what := shipEvent(sh.Status); what != "" {
				add("Ship " + shipName(sh) + " " + what + ".")
			}
		}
	}
	// What is gone is forgotten, so a reused session name starts fresh.
	st.shipState = shipsNow

	chatSession := sessionhost.ChatSession(s.key)
	inboxNow := map[string]bool{}
	for _, it := range ti.inbox {
		id := it.Session + "\x00" + it.ID
		if it.Fleet != s.key || it.Session == chatSession {
			continue
		}
		inboxNow[id] = true
		if st.inboxSeen[id] {
			continue
		}
		who := it.Name
		if who == "" {
			who = it.Session
		}
		add("Inbox: a " + brief.Clean(it.Kind) + " from " + brief.Clean(who) + " is waiting.")
	}
	st.inboxSeen = inboxNow
	here := map[string]bool{}
	for _, q := range s.questions(false) {
		if len(q.Answer) == 0 {
			continue
		}
		here[q.Item.ID] = true
		if !st.answered[q.Item.ID] {
			st.answered[q.Item.ID] = true
			add("The engineer answered your question: " + brief.Clean(questionTitle(q)))
		}
	}
	for id := range st.answered {
		if !here[id] {
			delete(st.answered, id)
		}
	}
	st.baselined = true
	if len(lines) > 0 {
		_ = digest.Add(s.Home, s.key, lines...)
	}
}

func shipName(sh ships.Ship) string {
	if sh.Name != "" {
		return brief.Clean(sh.Name)
	}
	return brief.Clean(sh.Session)
}

// shipEvent is what a ship that changed to status did, "" for a change AdmiralBot needn't hear of.
func shipEvent(status string) string {
	switch status {
	case ships.StatusSailingOff:
		return "sailed off"
	case ships.StatusMoored:
		return "moored with a PR open"
	case ships.StatusAnchored:
		return "anchored"
	case ships.StatusNeedsYou:
		return "is waiting on the engineer"
	}
	return ""
}
