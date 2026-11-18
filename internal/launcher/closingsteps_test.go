package launcher

import (
	"strings"
	"testing"
)

func (p *pinRig) chatLaunch() Request {
	r := p.justRequest()
	r.Via, r.OnInstruction, r.Autopilot = "admiralbot", true, true
	return r
}

func TestAQuickShipsPromptIsAdmiralBotsLaunchTextThenAdmiralsClosingSteps(t *testing.T) {
	p := newPinRig(t, nil)
	if _, err := p.l.Launch(p.chatLaunch()); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	prompt := p.lastArg(t)
	if !strings.HasPrefix(prompt, "ship the fix") {
		t.Fatalf("prompt = %q, want AdmiralBot's launch text first", prompt)
	}
	rest := strings.ToLower(strings.TrimPrefix(prompt, "ship the fix"))
	var at []int
	for _, want := range []string{"commit", "push", "pull request", "admiral_report", "result", "prs", "full url"} {
		i := strings.Index(rest, want)
		if i < 0 {
			t.Fatalf("closing steps = %q, want them to mention %q", rest, want)
		}
		at = append(at, i)
	}
	if !(at[0] < at[1] && at[1] < at[2] && at[2] < at[3]) {
		t.Errorf("closing steps = %q, want commit, push, open a PR, then the report, in that order", rest)
	}
}

func TestOnlyAFreshNoSkillLaunchTheEngineerAskedForInTheChatGetsClosingSteps(t *testing.T) {
	p := newPinRig(t, nil)
	// the control: the same request as a quick ship carries the steps, so the cases below differ in one thing
	if _, err := p.l.Launch(p.chatLaunch()); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if got := p.lastArg(t); !strings.Contains(got, "ADMIRAL_REPORT") {
		t.Fatalf("a quick ship's prompt = %q, want closing steps naming ADMIRAL_REPORT", got)
	}
	chat := func(mod func(*Request)) Request {
		r := p.chatLaunch()
		mod(&r)
		return r
	}
	for _, c := range []struct {
		name string
		req  Request
	}{
		{"a scout", chat(func(r *Request) { r.Scout = true })},
		{"a Commodore launch", chat(func(r *Request) { r.Skill = Skill{Kind: "commodore"} })},
		{"a skill launch", chat(func(r *Request) { r.Skill = Skill{Kind: "skill", Name: "commodore"} })},
		{"a no-skill launch AdmiralBot made for a commitment or campaign", chat(func(r *Request) { r.OnInstruction = false })},
		{"a launch from the UI", p.justRequest()},
		{"a wake", chat(func(r *Request) { r.Prompt = "resume the work" })},
		{"a wake with no resume text", chat(func(r *Request) { r.Bare = true })},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := p.l.Launch(c.req); err != nil {
				t.Fatalf("Launch: %v", err)
			}
			got := p.lastArg(t)
			for _, no := range []string{"ADMIRAL_REPORT", "full URL"} {
				if strings.Contains(got, no) {
					t.Errorf("prompt = %q, want no closing steps (found %q)", got, no)
				}
			}
		})
	}
}
