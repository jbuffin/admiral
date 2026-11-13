package launcher

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const guidanceText = "Prefer small PRs.\nNever touch the billing module."

func (p *pinRig) guided(text string, err error) *[]string {
	asked := &[]string{}
	p.l.Guidance = func(fleet string) (string, error) {
		*asked = append(*asked, fleet)
		return text, err
	}
	return asked
}

func (p *pinRig) lastArg(t *testing.T) string {
	t.Helper()
	argv := p.argv(t, "claude")
	return argv[len(argv)-1]
}

func (p *pinRig) justRequest() Request {
	return Request{Fleet: "work", Harness: "claude", Dir: p.work, Skill: Skill{Kind: "none"}, Text: "ship the fix"}
}

func TestAWorkerLaunchedShipsPromptCarriesTheFleetsGuidanceInAStandingOrdersSection(t *testing.T) {
	p := newPinRig(t, nil)
	asked := p.guided(guidanceText, nil)
	req := p.justRequest()
	req.WorkerLaunched = true
	if _, err := p.l.Launch(req); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	prompt := p.lastArg(t)
	if !strings.HasPrefix(prompt, "ship the fix") || !strings.Contains(prompt, "## Standing orders") || !strings.Contains(prompt, guidanceText) {
		t.Errorf("prompt = %q, want the text, then a \"## Standing orders\" section holding the Guidance", prompt)
	}
	if strings.Index(prompt, "## Standing orders") < strings.Index(prompt, "ship the fix") {
		t.Errorf("prompt = %q: the section must come after the prompt", prompt)
	}
	if len(*asked) != 1 || (*asked)[0] != "work" {
		t.Errorf("Guidance asked for fleets %v, want [work]", *asked)
	}
}

func TestAShipThatIsNotWorkerLaunchedGetsItsPromptUnchanged(t *testing.T) {
	p := newPinRig(t, nil)
	p.guided(guidanceText, nil)
	if _, err := p.l.Launch(p.justRequest()); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if got := p.lastArg(t); got != "ship the fix" {
		t.Errorf("prompt = %q, want the text as written", got)
	}
}

func TestAWorkerLaunchWithNoGuidanceGetsNoStandingOrdersSection(t *testing.T) {
	for name, text := range map[string]string{"empty": "", "blank": " \n\t"} {
		t.Run(name, func(t *testing.T) {
			p := newPinRig(t, nil)
			asked := p.guided(text, nil)
			req := p.justRequest()
			req.WorkerLaunched = true
			if _, err := p.l.Launch(req); err != nil {
				t.Fatalf("Launch: %v", err)
			}
			if got := p.lastArg(t); got != "ship the fix" {
				t.Errorf("prompt = %q, want the text as written", got)
			}
			if len(*asked) != 1 {
				t.Errorf("Guidance asked for fleets %v, want it consulted once", *asked)
			}
		})
	}
}

func TestACrewMemberWorkerLaunchGetsTheGuidanceAfterTheMembersPrompt(t *testing.T) {
	p := newPinRig(t, nil)
	member(t, p.builtIn(), "scout", "scribe", "", "Work as scout.")
	p.guided(guidanceText, nil)
	req := p.asMember("scout", "claude")
	req.WorkerLaunched = true
	if _, err := p.l.Launch(req); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	prompt := p.lastArg(t)
	if !strings.HasPrefix(prompt, "Work as scout.\n\nship the fix") || !strings.Contains(prompt, "## Standing orders") || !strings.Contains(prompt, guidanceText) {
		t.Errorf("prompt = %q, want the member's prompt and text, then the Standing orders section", prompt)
	}
}

func TestGuidanceIsNeverWrittenIntoTheWorktreeOrAnUntrustedFolder(t *testing.T) {
	p := newPinRig(t, nil)
	p.guided(guidanceText, nil)
	req := p.justRequest()
	req.WorkerLaunched = true
	if _, err := p.l.Launch(req); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if !strings.Contains(p.lastArg(t), "Never touch the billing module.") {
		t.Fatal("the ship's prompt lacks the Guidance, so there is nothing to keep out of files")
	}
	hold := func(root string, onlyUntrusted bool) {
		filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if onlyUntrusted && !strings.Contains(path, string(os.PathSeparator)+"untrusted"+string(os.PathSeparator)) {
				return nil
			}
			if b, err := os.ReadFile(path); err == nil && strings.Contains(string(b), "Never touch the billing module.") {
				t.Errorf("%s holds the Guidance", path)
			}
			return nil
		})
	}
	hold(p.work, false)
	hold(p.home, true)
}

func TestGuidanceThatMakesTheCommandTooLongRefusesTheLaunchAndIsNotDropped(t *testing.T) {
	p := newPinRig(t, nil)
	p.guided(strings.Repeat("g", 2000), nil)
	req := p.justRequest()
	req.Text = strings.Repeat("x", 13500)
	if _, err := p.l.Launch(req); err != nil {
		t.Fatalf("a non-worker launch of the same text: %v (the test's text must fit alone)", err)
	}
	req.WorkerLaunched = true
	_, err := p.l.Launch(req)
	var re *Error
	if !errors.As(err, &re) || re.Status != http.StatusBadRequest || !strings.Contains(re.Msg, "too long to launch") {
		t.Errorf("worker launch with the Guidance = %v, want a 400 \"too long to launch\"", err)
	}
}

func TestAGuidanceReadErrorLaunchesWithoutTheSection(t *testing.T) {
	p := newPinRig(t, nil)
	p.guided("", errors.New("the file is unreadable"))
	var warned []string
	p.l.Warn = func(msg string) { warned = append(warned, msg) }
	req := p.justRequest()
	req.WorkerLaunched = true
	if _, err := p.l.Launch(req); err != nil {
		t.Fatalf("Launch: %v, want the ship to launch without the section", err)
	}
	if got := p.lastArg(t); got != "ship the fix" {
		t.Errorf("prompt = %q, want the text as written", got)
	}
	if len(warned) != 1 || !strings.Contains(warned[0], "the file is unreadable") {
		t.Errorf("warnings = %q, want one carrying the read error", warned)
	}
}

func TestAClientCannotSetWorkerLaunchedFromTheRequestBody(t *testing.T) {
	var req Request
	for _, body := range []string{`{"WorkerLaunched":true}`, `{"worker_launched":true}`, `{"workerLaunched":true}`} {
		req = Request{}
		if err := json.Unmarshal([]byte(body), &req); err != nil {
			t.Fatal(err)
		}
		if req.WorkerLaunched {
			t.Errorf("%s set WorkerLaunched", body)
		}
	}
}
