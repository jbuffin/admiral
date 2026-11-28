package inbox

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jbuffin/admiral/internal/allowrules"
	"github.com/jbuffin/admiral/internal/outcomes"
	"github.com/jbuffin/admiral/internal/permlog"
)

// KindAllowRules lives only in the AdmiralSession directory: Admiral writes it when a ship's round ends, and only an
// answer changes a fleet's rules.
const KindAllowRules = "allow_rules"

const maxReasonChars = 200

// RulePrompt is a logged prompt a card's rule would have answered.
type RulePrompt struct {
	Ship    string `json:"ship"`
	ID      string `json:"id"`
	Crew    string `json:"crew,omitempty"`
	At      string `json:"at"`
	Tool    string `json:"tool,omitempty"`
	Command string `json:"command,omitempty"`
}

// RuleCard is one proposed rule. A Codex card has a pattern and no repo, and says it covers every repo of the fleet.
type RuleCard struct {
	ID        string       `json:"id"`
	Harness   string       `json:"harness"`
	Repo      string       `json:"repo,omitempty"`
	Rule      string       `json:"rule,omitempty"`
	Pattern   []string     `json:"pattern,omitempty"`
	EveryRepo bool         `json:"every_repo,omitempty"`
	Count     int          `json:"count"`
	Reason    string       `json:"reason,omitempty"`
	Prompts   []RulePrompt `json:"prompts"`
}

// AllowRulesObject is the allow_rules object of an item: one fleet's proposed rules.
type AllowRulesObject struct {
	Fleet string     `json:"fleet"`
	Cards []RuleCard `json:"cards"`
}

func (c RuleCard) allowRule() allowrules.Rule {
	return allowrules.Rule{Harness: c.Harness, Repo: c.Repo, Rule: c.Rule, Pattern: c.Pattern}
}

// ruleText is the words a report's allow_rules entry names the rule by.
func ruleText(r allowrules.Rule) string {
	if r.Harness == "codex" {
		return strings.Join(r.Pattern, " ")
	}
	return r.Rule
}

func (it Item) AllowRulesObject() (AllowRulesObject, bool) {
	var o AllowRulesObject
	if it.Kind != KindAllowRules {
		return o, false
	}
	if json.Unmarshal(it.fields[KindAllowRules], &o) != nil || o.Fleet == "" || len(o.Cards) == 0 {
		return o, false
	}
	seen := map[string]bool{}
	for _, c := range o.Cards {
		if c.ID == "" || seen[c.ID] || c.allowRule().Check() != nil {
			return o, false
		}
		seen[c.ID] = true
	}
	return o, true
}

// AllowRuleReasons reads a v2 report's allow_rules: one line of plain text per rule, cut to 200 characters. A report
// that isn't v2, or an entry that isn't plain text, gives none.
func AllowRuleReasons(report []byte) map[string]string {
	var r struct {
		Version    int `json:"version"`
		AllowRules []struct {
			Rule   string `json:"rule"`
			Reason string `json:"reason"`
		} `json:"allow_rules"`
	}
	if json.Unmarshal(report, &r) != nil || r.Version != reportVersion {
		return nil
	}
	out := map[string]string{}
	for _, e := range r.AllowRules {
		reason := strings.TrimSpace(e.Reason)
		if line, _, multi := strings.Cut(reason, "\n"); multi {
			reason = strings.TrimSpace(line)
		}
		if reason == "" || e.Rule == "" || strings.ContainsFunc(reason, func(c rune) bool { return !unicode.IsPrint(c) }) {
			continue
		}
		if utf8.RuneCountInString(reason) > maxReasonChars {
			reason = string([]rune(reason)[:maxReasonChars])
		}
		out[e.Rule] = reason
	}
	return out
}

func cardID(r allowrules.Rule) string {
	return "r" + itemHash(append([]string{r.Harness, r.Repo, r.Rule}, r.Pattern...)...)
}

func promptOf(r permlog.Record) RulePrompt {
	return RulePrompt{Ship: r.Ship, ID: r.ID, Crew: r.Crew, At: r.At, Tool: r.Call.Tool, Command: r.Call.Command}
}

// proposalMu orders the writer against every answer and Remove in the _admiral inbox (writeAnswered holds it too), and
// keeps two rounds ending together from each finding no open item and writing their own.
var proposalMu sync.Mutex

// beforeProposalWrite is a test seam: it runs once the writer has chosen its item and not yet checked for an answer.
var beforeProposalWrite func(dir, id string)

// maxCardPrompts is how many prompts a card keeps, the newest; its count goes on counting all of them.
const maxCardPrompts = 20

// WriteAllowRuleProposals puts a round's candidate rules on the fleet's open allow rule proposal, or on a new item when
// none is open. A rule already on a card has its prompts added and its count raised; an item with an answer file is
// never touched, and the round's rules go on a new item. reasons maps a rule's text to the report's reason for it, and
// adds no card.
func WriteAllowRuleProposals(home, fleet string, cands []allowrules.Candidate, reasons map[string]string) error {
	if fleet == "" || len(cands) == 0 {
		return nil
	}
	proposalMu.Lock()
	defer proposalMu.Unlock()
	dir := filepath.Join(home, "inbox", AdmiralSession)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	id, created, obj := openAllowRulesItem(dir, fleet)
	for attempt := 0; ; attempt++ {
		if id == "" {
			now := time.Now().UTC()
			id, created, obj = "allow-rules-"+itemHash(fleet, now.Format(time.RFC3339Nano)), now.Format(time.RFC3339), AllowRulesObject{Fleet: fleet}
		}
		mergeCandidates(&obj, cands, reasons)
		b, err := json.Marshal(map[string]any{"version": 1, "id": id, "kind": KindAllowRules, "created": created,
			"title": "Allow rules for " + fleet, KindAllowRules: obj})
		if err != nil {
			return err
		}
		if beforeProposalWrite != nil {
			beforeProposalWrite(dir, id)
		}
		if _, err := os.Lstat(filepath.Join(dir, answerName(id))); err == nil && attempt == 0 {
			id, obj = "", AllowRulesObject{} // answered meanwhile: this round's rules go on a new item
			continue
		} else if err == nil {
			return nil
		}
		return allowrules.WriteFile(filepath.Join(dir, id+".json"), b)
	}
}

func mergeCandidates(obj *AllowRulesObject, cands []allowrules.Candidate, reasons map[string]string) {
	for _, c := range cands {
		rule := c.Rule
		i := slices.IndexFunc(obj.Cards, func(k RuleCard) bool { return k.allowRule().Same(rule) })
		if i < 0 {
			obj.Cards = append(obj.Cards, RuleCard{ID: cardID(rule), Harness: rule.Harness, Repo: rule.Repo, Rule: rule.Rule,
				Pattern: rule.Pattern, EveryRepo: rule.Harness == "codex", Prompts: []RulePrompt{}})
			i = len(obj.Cards) - 1
		}
		card := &obj.Cards[i]
		for _, p := range c.Prompts {
			if !slices.ContainsFunc(card.Prompts, func(q RulePrompt) bool { return q.Ship == p.Ship && q.ID == p.ID }) {
				card.Prompts = append(card.Prompts, promptOf(p))
				card.Count++
			}
		}
		if len(card.Prompts) > maxCardPrompts {
			card.Prompts = slices.Clone(card.Prompts[len(card.Prompts)-maxCardPrompts:])
		}
		if reason := reasons[ruleText(rule)]; reason != "" {
			card.Reason = reason
		}
	}
}

// openAllowRulesItem finds the fleet's newest allow rule item that has no answer file.
func openAllowRulesItem(dir, fleet string) (id, created string, obj AllowRulesObject) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return
	}
	defer root.Close()
	var newest time.Time
	for _, it := range scanShip(root, AdmiralSession) {
		if o, ok := it.AllowRulesObject(); ok && o.Fleet == fleet && !it.Created.Before(newest) {
			id, created, obj, newest = it.ID, it.Created.UTC().Format(time.RFC3339), o, it.Created
		}
	}
	return
}

// AllowRulesRequest decides every card of an item: add or skip.
type AllowRulesRequest struct {
	Cards map[string]struct {
		Decision string `json:"decision"`
	} `json:"cards"`
}

type allowRuleAnswer struct {
	Decision string `json:"decision"`
}

// AnswerAllowRules adds the chosen rules to the fleet's store and records the skipped ones, then publishes the answer.
func (s *Service) AnswerAllowRules(id string, req AllowRulesRequest) error {
	return s.AnswerAllowRulesGuarded(id, req, nil)
}

// AnswerAllowRulesGuarded is AnswerAllowRules for a caller with a Guard, checked under the lock before any rule is written.
func (s *Service) AnswerAllowRulesGuarded(id string, req AllowRulesRequest, g *Guard) error {
	err := s.writeAnswered(AdmiralSession, id, KindAllowRules, func(it Item) (any, []outcomes.Record, error) {
		if err := g.check(it); err != nil {
			return nil, nil, err
		}
		o, _ := it.AllowRulesObject()
		if s.AllowRules == nil {
			return nil, nil, errors.New("allow rules are not available")
		}
		have := map[string]bool{}
		for _, c := range o.Cards {
			have[c.ID] = true
		}
		for cid := range req.Cards {
			if !have[cid] {
				return nil, nil, invalid("the item has no card %q", cid)
			}
		}
		answers := map[string]allowRuleAnswer{}
		for _, c := range o.Cards {
			d, ok := req.Cards[c.ID]
			if !ok {
				return nil, nil, invalid("card %s has no decision", c.ID)
			}
			if d.Decision != "add" && d.Decision != "skip" {
				return nil, nil, invalid("card %s: %q is not add or skip", c.ID, d.Decision)
			}
			answers[c.ID] = allowRuleAnswer{d.Decision}
		}
		var recs []outcomes.Record
		for _, c := range o.Cards {
			var err error
			event := "allow_rule_skipped"
			if answers[c.ID].Decision == "add" {
				event, err = "allow_rule_added", s.AllowRules.Add(o.Fleet, c.allowRule())
			} else {
				err = s.AllowRules.Skip(o.Fleet, c.allowRule())
			}
			if err != nil {
				return nil, nil, err
			}
			recs = append(recs, outcomes.Record{Event: event, Fields: map[string]any{"item": it.ID, "kind": KindAllowRules, "card": c.ID, "fleet": o.Fleet}})
		}
		file := struct {
			Version  int                        `json:"version"`
			ID       string                     `json:"id"`
			Kind     string                     `json:"kind"`
			Answered string                     `json:"answered"`
			Cards    map[string]allowRuleAnswer `json:"cards"`
		}{1, id, KindAllowRules, time.Now().UTC().Format(time.RFC3339), answers}
		return file, recs, nil
	})
	if err == nil {
		s.settled(AdmiralSession, id, g)
		s.Refresh()
	}
	return err
}

func (s *Service) serveAllowRules(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req AllowRulesRequest
	if !decode(w, r, 1<<20, &req) {
		return
	}
	respond(w, AdmiralSession, id, ReviewResult{}, s.AnswerAllowRules(id, req))
}
