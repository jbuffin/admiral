package crewapi

import (
	_ "embed"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jbuffin/admiral/internal/capabilities"
	"github.com/jbuffin/admiral/internal/crew"
	"github.com/jbuffin/admiral/internal/judge"
)

const (
	maxDescription = 4000
	maxSkills      = 20
	draftBudgetUSD = 0.30
	draftTimeout   = 2 * time.Minute
)

//go:embed crewwriter.md
var crewWriter string

const draftSchema = `{"type":"object","properties":{` +
	`"name":{"type":"string","maxLength":64},` +
	`"role":{"type":"string","maxLength":64},` +
	`"based-on":{"type":"string","maxLength":64},` +
	`"skills":{"type":"array","items":{"type":"string","maxLength":100}},` +
	`"model":{"type":"string","maxLength":100},` +
	`"pick-me-when":{"type":"string","maxLength":300},` +
	`"instructions":{"type":"string","maxLength":12000}` +
	`},"required":["name","role","based-on","skills","model","pick-me-when","instructions"],"additionalProperties":false}`

// draftPrompt is the crew-writer prompt followed by the description in a fence longer than any backtick run in it.
func draftPrompt(description string) string {
	fence, run := "```", 0
	for _, r := range description {
		if r == '`' {
			run++
			if run >= len(fence) {
				fence += "`"
			}
		} else {
			run = 0
		}
	}
	prompt := strings.Replace(crewWriter, "{{roles}}", strings.Join(crew.Roles(), ", "), 1)
	return prompt + "\nThe engineer's description:\n\n" + fence + "\n" + description + "\n" + fence + "\n"
}

type problem struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// newMember is a member as the editor holds it; the empty string stands for an absent optional field.
type newMember struct {
	Name         string   `json:"name"`
	Role         string   `json:"role"`
	BasedOn      string   `json:"based-on"`
	Skills       []string `json:"skills"`
	Model        string   `json:"model"`
	Harness      string   `json:"harness"`
	PickMeWhen   string   `json:"pick-me-when"`
	Instructions string   `json:"instructions"`
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func (m newMember) body() memberBody {
	skills := m.Skills
	if skills == nil {
		skills = []string{}
	}
	return memberBody{Role: m.Role, BasedOn: optional(m.BasedOn), Skills: skills, Model: optional(m.Model),
		Harness: optional(m.Harness), PickMeWhen: m.PickMeWhen, Instructions: trimBlankLines(m.Instructions)}
}

// hidden finds the code points a reader can't see, other than the newline and tab an instruction may hold.
func hidden(s string, allowLayout bool) (count int, first rune) {
	for _, r := range s {
		if allowLayout && (r == '\n' || r == '\t') {
			continue
		}
		if !unicode.IsGraphic(r) {
			if count == 0 {
				first = r
			}
			count++
		}
	}
	return count, first
}

// problems runs every check the file would meet, one problem per failed check; name is checked by the caller.
func (m newMember) problems() []problem {
	var out []problem
	b := m.body()
	fields := []struct{ name, value string }{{"role", m.Role}, {"based-on", m.BasedOn}, {"model", m.Model},
		{"harness", m.Harness}, {"pick-me-when", m.PickMeWhen}, {"instructions", b.Instructions}}
	for _, skill := range m.Skills {
		fields = append(fields, struct{ name, value string }{"skills", skill})
	}
	flagged := map[string]bool{}
	for _, f := range fields {
		if n, first := hidden(f.value, f.name == "instructions"); n > 0 && !flagged[f.name] {
			flagged[f.name] = true
			out = append(out, problem{f.name, fmt.Sprintf("holds %d hidden character(s); the first is U+%04X", n, first)})
		}
	}
	if len(m.Skills) > maxSkills {
		out = append(out, problem{"skills", fmt.Sprintf("at most %d skills", maxSkills)})
	}
	if field := b.oneLine(); field != "" {
		return append(out, problem{field, "must be one line"})
	}
	stem := m.Name
	if !crew.ValidName(stem) {
		stem = "member"
	}
	got, ferr := crew.CheckFile(b.file(stem), stem)
	switch {
	case ferr != nil:
		out = append(out, problem{ferr.Field, ferr.Message})
	default:
		if field := b.differs(got); field != "" {
			out = append(out, problem{field, "would be read back differently from what was sent"})
		}
	}
	return out
}

// nameProblem is why name can't be a new member of the fleet; it looks the name up only once it is a valid name.
func (s *Service) nameProblem(c fleetCrew, name string) (p *problem, err error) {
	if !crew.ValidName(name) {
		return &problem{"name", "must be a lowercase slug of at most 64 characters"}, nil
	}
	dir, found, err := c.memberDir(false)
	if err == nil && found {
		found, err = lstatFile(filepath.Join(dir, name+".md"))
	}
	switch {
	case err != nil:
		return nil, err
	case found:
		return &problem{"name", "this fleet already has a crew member " + name}, nil
	}
	fleet, _ := s.Config.Current().Fleet(c.key)
	if slices.Contains(BuiltInNames(fleet), name) {
		return &problem{"name", name + " is a built-in member; customize it instead"}, nil
	}
	return nil, nil
}

func failureReason(k judge.FailureKind) string {
	switch k {
	case judge.KindTimeout:
		return "The drafting call did not answer in time."
	case judge.KindBudgetSpent:
		return "The drafting call ran out of budget."
	case judge.KindBadReply:
		return "The drafting call's reply was not usable."
	}
	return "The drafting call failed."
}

// readDraft takes the judge's fields as the editor holds them; ok is false for a reply of the wrong shape.
func readDraft(fields map[string]any) (newMember, bool) {
	str := func(k string) (string, bool) { s, ok := fields[k].(string); return s, ok }
	var m newMember
	var ok [6]bool
	m.Name, ok[0] = str("name")
	m.Role, ok[1] = str("role")
	m.BasedOn, ok[2] = str("based-on")
	m.Model, ok[3] = str("model")
	m.PickMeWhen, ok[4] = str("pick-me-when")
	m.Instructions, ok[5] = str("instructions")
	list, isList := fields["skills"].([]any)
	m.Skills = []string{}
	for _, v := range list {
		s, isStr := v.(string)
		if !isStr {
			return m, false
		}
		m.Skills = append(m.Skills, s)
	}
	return m, isList && !slices.Contains(ok[:], false)
}

func (s *Service) serveDraft(w http.ResponseWriter, r *http.Request) {
	c, ok := s.fleetCrew(w, r)
	if !ok {
		return
	}
	var req struct {
		Description string `json:"description"`
	}
	if !decode(w, r, &req) {
		return
	}
	switch {
	case strings.TrimSpace(req.Description) == "":
		writeError(w, http.StatusBadRequest, "description: describe the member")
		return
	case utf8.RuneCountInString(req.Description) > maxDescription:
		writeError(w, http.StatusBadRequest, fmt.Sprintf("description: at most %d characters", maxDescription))
		return
	}
	fleet, _ := s.Config.Current().Fleet(c.key)
	choice, err := judge.Choose(fleet, capabilities.Open(c.home))
	if err != nil {
		reason := "no judge harness is proven for this fleet"
		var none *judge.NoJudgeHarness
		if errors.As(err, &none) {
			reason = none.Reason
		}
		writeJSON(w, http.StatusConflict, map[string]string{"error": "no harness is proven safe for judge calls", "reason": reason})
		return
	}
	run := s.Judge
	if run == nil {
		run = judge.Run
	}
	reply, err := run(r.Context(), choice, judge.Call{Kind: judge.Strong, Prompt: draftPrompt(req.Description),
		Schema: []byte(draftSchema), BudgetUSD: draftBudgetUSD, Timeout: draftTimeout})
	if err != nil {
		kind := judge.KindError
		var f *judge.Failure
		if errors.As(err, &f) {
			kind = f.Kind
		}
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": failureReason(kind), "kind": string(kind)})
		return
	}
	m, ok := readDraft(reply.Fields)
	if !ok {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": failureReason(judge.KindBadReply), "kind": string(judge.KindBadReply)})
		return
	}
	problems := []problem{}
	np, err := s.nameProblem(c, m.Name)
	if err != nil {
		refuse(w, err)
		return
	}
	if np != nil {
		problems = append(problems, *np)
	}
	problems = append(problems, m.problems()...)
	writeJSON(w, http.StatusOK, map[string]any{"draft": m, "problems": problems})
}

func (s *Service) serveCreate(w http.ResponseWriter, r *http.Request) {
	c, ok := s.fleetCrew(w, r)
	if !ok {
		return
	}
	var m newMember
	if !decode(w, r, &m) {
		return
	}
	if !crew.ValidName(m.Name) {
		fieldError(w, "name", "must be a lowercase slug of at most 64 characters")
		return
	}
	writeMu.Lock()
	defer writeMu.Unlock()
	np, err := s.nameProblem(c, m.Name)
	if err != nil {
		refuse(w, err)
		return
	}
	if np != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": fieldMessage(np.Field, np.Message), "field": np.Field})
		return
	}
	if ps := m.problems(); len(ps) > 0 {
		fieldError(w, ps[0].Field, ps[0].Message)
		return
	}
	dir, _, err := c.memberDir(true)
	if err != nil {
		refuse(w, err)
		return
	}
	if err := writeAtomic(dir, m.Name+".md", m.body().file(m.Name)); err != nil {
		refuse(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"name": m.Name})
}
