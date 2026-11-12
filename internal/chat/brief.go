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
	"github.com/jbuffin/admiral/internal/chatlog"
	"github.com/jbuffin/admiral/internal/digest"
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
	ledgerOK  bool

	baselined bool // ships, Inbox items and answers seen once, so what was already there is no event
	shipState map[string]string
	inboxSeen map[string]bool
	answered  map[string]bool
}

// Tick renders each fleet's brief and keeps its digest; Run calls it on its tick.
func (s *Service) Tick() {
	for _, fc := range s.onFleets() {
		fc.renderBrief()
		fc.digestTick()
	}
}

// DigestSeq is the highest digest sequence the fleet's chat has printed, which its hook records on the pane; 0 when
// it has none.
func (s *Service) DigestSeq(fleet string) uint64 {
	if s.Tmux == "" {
		return 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	v, err := sessionhost.NewServer(s.Tmux, s.Socket).WithoutUTF8().StderrErrors().ReadPaneOption(ctx, "="+sessionhost.ChatSession(fleet)+":", sessionhost.ChatDigestOpt)
	if err != nil || !sessionhost.SafeToken.MatchString(v) {
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

func (s *fleetChat) renderBrief() {
	s.tmu.Lock()
	defer s.tmu.Unlock()
	st := &s.tk
	if sig := s.ledgerSig(); !st.ledgerOK || sig != st.ledgerSig {
		instr, ierr := s.notes().Instructions(s.key)
		notes, nerr := s.notes().Notes(s.key)
		if ierr != nil || nerr != nil {
			return
		}
		st.instr, st.notes, st.ledgerSig, st.ledgerOK = instr, notes, sig, true
	}
	in := brief.Input{Instructions: st.instr}
	for _, n := range st.notes {
		in.Notes = append(in.Notes, brief.Note{Text: n.Text, Via: n.Via, TS: n.TS})
	}
	qs := s.questions(false)
	for i := len(qs) - 1; i >= 0; i-- {
		if q := qs[i]; len(q.Answer) == 0 {
			in.Questions = append(in.Questions, brief.Question{Text: questionTitle(q), Asked: q.Item.Created})
		}
	}
	if s.Ships != nil {
		for _, sh := range s.Ships() {
			if sh.Fleet == s.key {
				in.Ships = append(in.Ships, brief.Ship{Name: sh.Name, Status: sh.Status, Via: sh.Via, Title: sh.Title})
			}
		}
	}
	if s.Campaigns != nil {
		for _, c := range s.Campaigns() {
			if c.Fleet == s.key && !c.Ended() {
				name := c.ID
				if c.Tree != nil && c.Tree.Title != "" {
					name += " " + c.Tree.Title
				}
				in.Campaigns = append(in.Campaigns, brief.Campaign{Name: name + " " + c.URL})
			}
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
func (s *fleetChat) digestTick() {
	s.tmu.Lock()
	defer s.tmu.Unlock()
	st := &s.tk
	if s.DigestAcked != nil {
		if d, err := digest.Read(s.Home, s.key); err == nil && len(d.Lines) > 0 {
			if seq := s.DigestAcked(s.key); seq > 0 {
				_ = digest.Ack(s.Home, s.key, seq)
			}
		}
	}
	var lines []string
	add := func(text string) {
		if st.baselined {
			lines = append(lines, text)
		}
	}

	if st.shipState == nil {
		st.shipState = map[string]string{}
		st.inboxSeen = map[string]bool{}
		st.answered = map[string]bool{}
	}
	if s.Ships != nil {
		for _, sh := range s.Ships() {
			if sh.Fleet != s.key {
				continue
			}
			if prev := st.shipState[sh.Session]; prev != sh.Status {
				st.shipState[sh.Session] = sh.Status
				if what := shipEvent(sh.Status); what != "" {
					add("Ship " + shipName(sh) + " " + what + ".")
				}
			}
		}
	}
	if s.Inbox != nil {
		chatSession := sessionhost.ChatSession(s.key)
		for _, it := range s.Inbox() {
			id := it.Session + "\x00" + it.ID
			if it.Fleet != s.key || it.Session == chatSession || st.inboxSeen[id] {
				continue
			}
			st.inboxSeen[id] = true
			who := it.Name
			if who == "" {
				who = it.Session
			}
			add("Inbox: a " + brief.Clean(it.Kind) + " from " + brief.Clean(who) + " is waiting.")
		}
	}
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
