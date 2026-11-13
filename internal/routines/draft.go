package routines

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jbuffin/admiral/internal/capabilities"
	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/judge"
	"github.com/jbuffin/admiral/internal/standing"
)

const (
	maxProseRunes  = 4000
	draftBudgetUSD = 0.30
	draftTimeout   = 2 * time.Minute
)

//go:embed standingorders_writer.md
var ordersWriter string

//go:embed routinewriter.md
var routineWriter string

const ordersSchema = `{"type":"object","properties":{` +
	`"limits":{"type":"object"},` +
	`"checks":{"type":"array","items":{"type":"string","maxLength":300},"maxItems":20},` +
	`"guidance":{"type":"string","maxLength":2000}` +
	`},"required":["limits","checks","guidance"],"additionalProperties":false}`

const routineSchema = `{"type":"object","properties":{` +
	`"name":{"type":"string","maxLength":80},` +
	`"enabled":{"type":"boolean"},` +
	`"when":{"type":"string"},` +
	`"before_acting":{"type":"string","enum":["report_only","ask_first","act"]},` +
	`"notify":{"type":"boolean"},` +
	`"do":{"type":"object","properties":{"prompt":{"type":"string"}},"required":["prompt"],"additionalProperties":false}` +
	`},"required":["name","enabled","when","before_acting","notify","do"],"additionalProperties":false}`

type problem struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// fencedPrompt is template followed by the prose in a fence longer than any backtick run in it.
func fencedPrompt(template, label, prose string) string {
	fence, run := "```", 0
	for _, r := range prose {
		if r == '`' {
			run++
			if run >= len(fence) {
				fence += "`"
			}
		} else {
			run = 0
		}
	}
	return template + "\n" + label + "\n\n" + fence + "\n" + prose + "\n" + fence + "\n"
}

func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }

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

func badGateway(w http.ResponseWriter, kind judge.FailureKind) {
	writeJSON(w, http.StatusBadGateway, map[string]string{"error": failureReason(kind), "kind": string(kind)})
}

// draftCall reads the prose, picks the fleet's judge harness and runs one Strong call; ok is false once the answer is written.
func (s *Service) draftCall(w http.ResponseWriter, r *http.Request, prompt func(prose string) string, schema string) (prose string, fields map[string]any, ok bool) {
	key := r.PathValue("key")
	f, err := s.Store.fleet(key)
	if err != nil {
		fail(w, err)
		return "", nil, false
	}
	var req struct {
		Prose string `json:"prose"`
	}
	if !decodeBody(w, r, &req) {
		return "", nil, false
	}
	switch {
	case strings.TrimSpace(req.Prose) == "":
		writeError(w, http.StatusBadRequest, "prose: write what you want")
		return "", nil, false
	case utf8.RuneCountInString(req.Prose) > maxProseRunes:
		writeError(w, http.StatusBadRequest, fmt.Sprintf("prose: at most %d characters", maxProseRunes))
		return "", nil, false
	}
	choice, err := judge.Choose(f, capabilities.Open(s.Store.Home))
	if err != nil {
		reason := "no judge harness is proven for this fleet"
		var none *judge.NoJudgeHarness
		if errors.As(err, &none) {
			reason = none.Reason
		}
		writeJSON(w, http.StatusConflict, map[string]string{"error": "no harness is proven safe for judge calls", "reason": reason})
		return "", nil, false
	}
	run := s.Judge
	if run == nil {
		run = judge.Run
	}
	reply, err := run(r.Context(), choice, judge.Call{Kind: judge.Strong, Prompt: prompt(req.Prose),
		Schema: []byte(schema), BudgetUSD: draftBudgetUSD, Timeout: draftTimeout})
	if err != nil {
		kind := judge.KindError
		var jf *judge.Failure
		if errors.As(err, &jf) {
			kind = jf.Kind
		}
		badGateway(w, kind)
		return "", nil, false
	}
	return req.Prose, reply.Fields, true
}

// ordersPrompt names every Limit by the field path its sentence carries.
func ordersPrompt(prose string) string {
	var fields []string
	for _, sn := range standing.Sentences(standing.Limits{}) {
		if sn.Field != "" {
			fields = append(fields, "- "+sn.Field)
		}
	}
	template := strings.Replace(ordersWriter, "{{fields}}", strings.Join(fields, "\n"), 1)
	return fencedPrompt(template, "The engineer's text:", prose)
}

// draftOrders is what a standing-order draft reply holds, as far as it fits the schema.
type draftOrders struct {
	limits   map[string]json.RawMessage
	checks   []string
	guidance string
}

func readOrdersReply(fields map[string]any) (draftOrders, bool) {
	var d draftOrders
	lim, ok := fields["limits"].(map[string]any)
	if !ok {
		return d, false
	}
	d.limits = map[string]json.RawMessage{}
	for k, v := range lim {
		raw, err := json.Marshal(v)
		if err != nil {
			return d, false
		}
		d.limits[k] = raw
	}
	list, ok := fields["checks"].([]any)
	if !ok {
		return d, false
	}
	for _, v := range list {
		c, ok := v.(string)
		if !ok {
			return d, false
		}
		d.checks = append(d.checks, c)
	}
	d.guidance, ok = fields["guidance"].(string)
	return d, ok
}

func (s *Service) serveOrdersDraft(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	prose, fields, ok := s.draftCall(w, r, ordersPrompt, ordersSchema)
	if !ok {
		return
	}
	d, ok := readOrdersReply(fields)
	if !ok {
		badGateway(w, judge.KindBadReply)
		return
	}
	problems := []problem{}
	f, _ := s.Config.Current().Fleet(key)

	var limits map[string]json.RawMessage
	effective := f.Limits
	if p, err := config.PreviewLimits(s.Store.Home, key, config.FleetFields{Limits: d.limits}); err != nil {
		problems = append(problems, problem{"limits", err.Error()})
	} else {
		limits, effective = d.limits, p.Limits
	}
	changes, loosens := []map[string]string{}, standing.Loosens(f.Limits, effective)
	if loosens == nil {
		loosens = []string{}
	}
	before := map[string]string{}
	for _, sn := range standing.Sentences(f.Limits) {
		before[sn.Field] = sn.Sentence
	}
	sentences := standing.Sentences(effective)
	for _, sn := range sentences {
		if sn.Field != "" && before[sn.Field] != sn.Sentence {
			changes = append(changes, map[string]string{"field": sn.Field, "sentence": sn.Sentence})
		}
	}

	text := collapse(prose)
	checks := []string{}
	for _, c := range d.checks {
		c = strings.TrimSpace(c)
		switch {
		case checkOne(c) != "":
			problems = append(problems, problem{"checks", fmt.Sprintf("dropped %q: %s", c, checkOne(c))})
		case !strings.Contains(text, collapse(c)):
			problems = append(problems, problem{"checks", fmt.Sprintf("dropped %q: it is not in the text", c)})
		case containsString(checks, c):
		default:
			checks = append(checks, c)
		}
	}
	if len(checks) > maxChecks {
		problems = append(problems, problem{"checks", fmt.Sprintf("kept the first %d of %d Checks", maxChecks, len(checks))})
		checks = checks[:maxChecks]
	}

	var guidance *string
	if g := strings.TrimSpace(d.guidance); g != "" {
		switch {
		case checkGuidance(g) != "":
			problems = append(problems, problem{"guidance", "dropped the Guidance: " + checkGuidance(g)})
		case !strings.Contains(text, collapse(g)):
			problems = append(problems, problem{"guidance", "dropped the Guidance: it is not in the text"})
		default:
			guidance = &g
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"draft":     map[string]any{"limits": limits, "checks": checks, "guidance": guidance},
		"sentences": sentences, "changes": changes, "loosens": loosens, "problems": problems,
	})
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// routineDraft is a create body: no id, off_by or timestamps, which the server sets.
type routineDraft struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	When    string `json:"when"`
	Do      Do     `json:"do"`
	Before  Mode   `json:"before_acting"`
	Notify  bool   `json:"notify"`
}

func readRoutineReply(fields map[string]any) (routineDraft, bool) {
	var d routineDraft
	raw, err := json.Marshal(fields)
	if err != nil {
		return d, false
	}
	for _, k := range []string{"name", "enabled", "when", "before_acting", "notify", "do"} {
		if _, has := fields[k]; !has {
			return d, false
		}
	}
	return d, json.Unmarshal(raw, &d) == nil && d.Do.Launch == nil
}

// routineProblem names the form field a validate error is about; the Do fields share one name.
func routineProblem(err error) problem {
	msg := strings.TrimPrefix(err.Error(), ErrInvalid.Error()+": ")
	field := "do"
	for _, f := range []string{"name", "when", "before_acting"} {
		if strings.HasPrefix(msg, f) {
			field = f
		}
	}
	return problem{field, strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(msg, field), ":"))}
}

func (s *Service) serveRoutineDraft(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	_, fields, ok := s.draftCall(w, r, func(prose string) string {
		return fencedPrompt(routineWriter, "The engineer's description:", prose)
	}, routineSchema)
	if !ok {
		return
	}
	d, ok := readRoutineReply(fields)
	if !ok {
		badGateway(w, judge.KindBadReply)
		return
	}
	problems := []problem{}
	f, _ := s.Store.fleet(key)
	if err := s.Store.validate(f, Routine{Name: d.Name, Enabled: d.Enabled, When: d.When, Do: d.Do, Before: d.Before, Notify: d.Notify}); err != nil {
		problems = append(problems, routineProblem(err))
	}
	writeJSON(w, http.StatusOK, map[string]any{"draft": d, "problems": problems})
}
