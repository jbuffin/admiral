// Package needcard is the one place that says which need card a ship's stop or an Inbox item posts and which choices one tap answers it with.
package needcard

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/jbuffin/admiral/internal/inbox"
	"github.com/jbuffin/admiral/internal/keymap"
	"github.com/jbuffin/admiral/internal/sessionhost"
	"github.com/jbuffin/admiral/internal/ships"
	"github.com/jbuffin/admiral/internal/textsafe"
)

// StateOpen is the state a card is posted in.
const StateOpen = "open"

// The kinds of a stop card.
const (
	StopPermission = "permission"
	StopTurn       = "stop"
	StopNotStarted = "not_started"
)

// StopKinds are the card kinds a ship's stop posts.
var StopKinds = map[string]bool{StopPermission: true, StopTurn: true, StopNotStarted: true}

// Choice is one button of a need card that a single tap answers whole.
type Choice struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// Resolver reads cards off ships and items. Configured says whether a fleet key is one of the configured fleets, the only ones that post cards.
type Resolver struct {
	Configured func(fleet string) bool
}

// ForShip is the id of the card the ship's current stop posts and its choices, or "" when the stop posts no card.
func (r Resolver) ForShip(sh ships.Ship) (string, []Choice) {
	kind, identity := StopKind(sh.Reason), StopIdentity(sh)
	if sh.Status != ships.StatusNeedsYou || kind == "" || identity == "" || sh.Fleet == "" || sessionhost.IsChatSession(sh.Session) || sh.Session == inbox.AdmiralSession || !r.Configured(sh.Fleet) {
		return "", nil
	}
	if kind == StopPermission && StopAnswerable(sh) {
		return StopCardID(sh.Session, identity), []Choice{{"allow", "Allow once"}, {"deny", "Deny"}}
	}
	return StopCardID(sh.Session, identity), nil
}

// ForItem is the id of the card the Inbox item posts and its choices, or "" when the item posts no card.
func (r Resolver) ForItem(it inbox.Item) (string, []Choice) {
	fleet := Fleet(it)
	if !Kinds[it.Kind] || fleet == "" || sessionhost.IsChatSession(it.Session) || it.Session == inbox.AdmiralSession && !FleetKinds[it.Kind] || !r.Configured(fleet) {
		return "", nil
	}
	if _, ok := Body(it); !ok {
		return "", nil
	}
	return CardID(it.Session, it.ID, Created(it.Created)), ItemChoices(it)
}

// ItemChoices are the choices a card for the item offers, none for a kind or shape no single tap answers.
func ItemChoices(it inbox.Item) []Choice {
	switch it.Kind {
	case inbox.KindQuestion:
		q, ok := OnlyQuestion(it)
		if !ok {
			return nil
		}
		out := make([]Choice, 0, len(q.Options))
		for i, o := range q.Options {
			out = append(out, Choice{strconv.Itoa(i), clean(o.Label)})
		}
		return out
	case inbox.KindReview:
		return []Choice{{"COMMENT", "COMMENT"}, {"APPROVE", "APPROVE"}, {"REQUEST_CHANGES", "REQUEST_CHANGES"}, {"discard", "Discard"}}
	case inbox.KindRoutineProposal:
		return []Choice{{"launch", "Launch"}, {"dismiss", "Dismiss"}}
	case inbox.KindCampaignDraft:
		return []Choice{{"create", "Create campaign"}, {"dismiss", "Dismiss"}}
	}
	return nil
}

// Question is a question item's one single-select question.
type Question struct {
	ID      string `json:"id"`
	Multi   bool   `json:"multi"`
	Options []struct {
		Label string `json:"label"`
	} `json:"options"`
}

// OnlyQuestion is the question of an item that has exactly one, single-select and with options.
func OnlyQuestion(it inbox.Item) (Question, bool) {
	var f struct {
		Questions []Question `json:"questions"`
	}
	if json.Unmarshal(it.Field(inbox.KindQuestion), &f) != nil || len(f.Questions) != 1 || f.Questions[0].Multi || len(f.Questions[0].Options) == 0 || f.Questions[0].ID == "" {
		return Question{}, false
	}
	return f.Questions[0], true
}

// AnswerBody is the body of the kind's own answer path that the choice stands for, read from the item as it is now; false for an id the item's card does not offer.
func AnswerBody(it inbox.Item, id string) ([]byte, bool) {
	known := false
	for _, c := range ItemChoices(it) {
		known = known || c.ID == id
	}
	if !known {
		return nil, false
	}
	var body any
	switch it.Kind {
	case inbox.KindQuestion:
		q, _ := OnlyQuestion(it)
		i, _ := strconv.Atoi(id)
		label, _ := json.Marshal(q.Options[i].Label)
		body = inbox.QuestionRequest{Answers: map[string]inbox.QuestionAnswer{q.ID: {Choice: label}}}
	case inbox.KindReview:
		if id == "discard" {
			body = map[string]bool{"discard": true}
		} else {
			body = inbox.SubmitRequest{Event: id}
		}
	case inbox.KindRoutineProposal:
		body = map[string]bool{id: true}
	case inbox.KindCampaignDraft:
		if id == "dismiss" {
			body = map[string]bool{"dismiss": true}
		} else {
			d, _ := it.CampaignDraft()
			body = inbox.CampaignDraftRequest{Repo: d.Repo, Title: d.Title, Body: d.Body}
		}
	}
	b, err := json.Marshal(body)
	return b, err == nil
}

// clean drops control and bidi characters, as the push does for what a lock screen shows.
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if textsafe.Hidden(r) {
			return -1
		}
		return r
	}, s)
}

// StopKind is the card kind a needs-you reason posts, or "" for a reason that posts none.
func StopKind(reason string) string {
	switch {
	case strings.HasPrefix(reason, "permission:"), reason == "permission", reason == "notification:permission_prompt":
		return StopPermission
	case reason == "stop":
		return StopTurn
	case reason == "not_started":
		return StopNotStarted
	}
	return ""
}

// StopAnswerable is false for a permission prompt the answer route cannot key, which is answered in the terminal.
func StopAnswerable(sh ships.Ship) bool {
	switch sh.Reason {
	case "stop":
		return true
	case "permission:AskUserQuestion", "permission:ExitPlanMode":
		return false
	}
	if !strings.HasPrefix(sh.Reason, "permission:") && !(sh.Harness == "copilot" && sh.Reason == "notification:permission_prompt") {
		return false
	}
	m, ok := keymap.For(sh.Harness)
	return ok && m.CardTool(strings.TrimPrefix(sh.Reason, "permission:"))
}

// StopIdentity is what binds a card to one stop. A ship that was never started has had no hook write, so its launch stands for it.
func StopIdentity(sh ships.Ship) string {
	if sh.StopID != "" {
		return sh.StopID
	}
	if sh.Reason == "not_started" {
		return sh.Launched
	}
	return ""
}

func StopCardID(session, identity string) string {
	return CardID(session, "stop:"+identity, "")
}

// Created is an item's created stamp as the card ids and records spell it.
func Created(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// CardID folds the item's identity in, so a reused session name or item id gets its own card; hashed, since a session name can hold any character.
func CardID(session, item, created string) string {
	h := sha256.Sum256([]byte(session + "\x00" + item + "\x00" + created))
	return "need-" + hex.EncodeToString(h[:12])
}

// Kinds are the Inbox kinds that post a need card.
var Kinds = map[string]bool{inbox.KindQuestion: true, inbox.KindReview: true, inbox.KindFindings: true,
	inbox.KindRoutineProposal: true, inbox.KindAllowRules: true, inbox.KindCampaignDraft: true}

// FleetKinds are the Kinds whose items live under _admiral and name their own fleet.
var FleetKinds = map[string]bool{inbox.KindRoutineProposal: true, inbox.KindAllowRules: true, inbox.KindCampaignDraft: true}

// Fleet is the fleet whose chat holds an item's card; an allow rule item names it in its object, since the list leaves Fleet empty for an unconfigured one.
func Fleet(it inbox.Item) string {
	switch it.Kind {
	case inbox.KindAllowRules:
		if o, ok := it.AllowRulesObject(); ok {
			return o.Fleet
		}
		return ""
	case inbox.KindRoutineProposal:
		if p, ok := it.RoutineProposal(); ok {
			return p.Fleet
		}
		return ""
	case inbox.KindCampaignDraft:
		if d, ok := it.CampaignDraft(); ok {
			return d.Fleet
		}
		return ""
	}
	return it.Fleet
}

// Body is the card's JSON for an item, with what its kind's choices need; a review's summary and a set of finding cards' quotes stay in the Inbox item, which the web joins.
func Body(it inbox.Item) (map[string]any, bool) {
	card := map[string]any{"ship": it.Session, "item": it.ID, "item_kind": it.Kind, "state": StateOpen}
	switch it.Kind {
	case inbox.KindQuestion:
		var q struct {
			Questions json.RawMessage `json:"questions"`
		}
		if json.Unmarshal(it.Field(inbox.KindQuestion), &q) != nil || len(q.Questions) == 0 {
			return nil, false
		}
		card["questions"] = q.Questions
	case inbox.KindReview:
		var r struct {
			PRURL string `json:"pr_url"`
		}
		if json.Unmarshal(it.Field(inbox.KindReview), &r) != nil {
			return nil, false
		}
		card["events"], card["discard"], card["pr_url"] = []string{"COMMENT", "APPROVE", "REQUEST_CHANGES"}, true, r.PRURL
	case inbox.KindFindings:
		var f struct {
			Cards []struct {
				ID       string          `json:"id"`
				Proposal *inbox.Proposal `json:"proposal"`
			} `json:"cards"`
		}
		if json.Unmarshal(it.Field(inbox.KindFindings), &f) != nil {
			return nil, false
		}
		cards := make([]map[string]any, 0, len(f.Cards))
		for _, c := range f.Cards {
			cards = append(cards, map[string]any{"id": c.ID, "proposal": c.Proposal})
		}
		card["cards"] = cards
	case inbox.KindRoutineProposal:
		p, ok := it.RoutineProposal()
		if !ok {
			return nil, false
		}
		delete(card, "ship")
		card["action"], card["reason"], card["expires_at"], card["routine_name"] = p.Action, p.Reason, p.ExpiresAt, p.RoutineName
	case inbox.KindAllowRules:
		o, ok := it.AllowRulesObject()
		if !ok {
			return nil, false
		}
		delete(card, "ship")
		card["cards"] = o.Cards
	case inbox.KindCampaignDraft:
		d, ok := it.CampaignDraft()
		if !ok {
			return nil, false
		}
		delete(card, "ship")
		card["repo"], card["title"], card["body"] = d.Repo, d.Title, d.Body
	default:
		return nil, false // a kind in Kinds with no body here must fail loudly, not post as a question
	}
	return card, true
}
