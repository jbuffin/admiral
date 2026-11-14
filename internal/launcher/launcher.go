// Package launcher starts a ship: it checks the order, builds the harness's command line and
// environment, and creates the tmux session with its @admiral-* options already set.
package launcher

import (
	"errors"
	"fmt"
	"github.com/jbuffin/admiral/internal/standing"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/jbuffin/admiral/internal/codexpane"
	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/moor"
	"github.com/jbuffin/admiral/internal/repofinder"
	"github.com/jbuffin/admiral/internal/sessionhost"
	"github.com/jbuffin/admiral/internal/skillindex"
)

type Request struct {
	Fleet   string `json:"fleet"`
	Harness string `json:"harness"`
	Dir     string `json:"dir"`
	Clone   bool   `json:"clone"` // sent with no Dir: the server clones the order's repo and launches there
	// CloneRepo is the repo the form showed; a Clone launch whose derived repo differs is refused.
	CloneRepo string `json:"clone_repo"`
	Skill     Skill  `json:"skill"`
	Source    string `json:"source"`
	OrderID   string `json:"order_id"`
	Text      string `json:"text"`
	Note      string `json:"note"` // Just launch only: optional text sent before the order
	Model     string `json:"model"`
	Effort    string `json:"effort"`
	// Crew names the members the launch pins; only a Commodore launch may carry any.
	Crew []string `json:"crew"`
	// Member launches the ship as this crew member; only a Just launch may carry one.
	Member string `json:"member"`
	// Title and URL are set by the server from the source's order, never by the client.
	Title string `json:"-"`
	URL   string `json:"-"`
	// Link is the link the titler picked from a ship's first prompt, carried into its later rounds. It claims nothing.
	Link string `json:"-"`
	// Issues is every issue of a slice launch; URL is the first. A client may name them, never the URL.
	Issues []string `json:"issues"`
	Panel  string   `json:"-"`
	// ChatMessage is the id of the chat message the launch answers; it keys the launch as chat:<id>.
	ChatMessage string `json:"chat_message"`
	// Anyway gives a chat launch a fresh key so the same message can launch twice.
	Anyway bool `json:"anyway"`
	// ChatKey is a woken ship's stored chat key, which a wake carries over as it is.
	ChatKey string `json:"-"`
	// ByFleet is the fleet AdmiralBot launches for; empty for the engineer. It shapes a refusal's wording only.
	ByFleet string `json:"-"`
	// Autopilot runs the ship under the guard with ADMIRAL_LOW_STAKES=advisor, counted against the global cap.
	Autopilot bool     `json:"autopilot"`
	ExtraArgs []string `json:"-"`
	// Resources are held while the ship is underway or needs you; a hand launch holds none.
	Resources []string `json:"resources"`
	// Name and Session relaunch a moored ship under its own identity instead of a picked name; Round is
	// the feedback round it starts.
	Name    string `json:"-"`
	Session string `json:"-"`
	Round   int    `json:"-"`
	// Prompt, when set, is the harness's prompt verbatim: a wake's resume text already carries its skill invocation.
	Prompt string `json:"-"`
	// Before runs under the launch lock with the validated checkout, just before the session is made; its error refuses the launch.
	Before func(dir string) error `json:"-"`
	// By and PR say what started a round, for the `woken` event.
	By string `json:"-"`
	PR string `json:"-"`
	// WokenVia stamps the `woken` event with the actor that woke the ship, apart from the ship's own via.
	WokenVia string `json:"-"`
	// Via is "admiralbot" when AdmiralBot made the launch, and the ship carries it.
	Via string `json:"-"`
	// RoutineKey is routine:<id>: a routine's ship holds it, so a second launch of the routine is refused while the ship is listed.
	RoutineKey string `json:"-"`
	// CommitmentKey is commitment:<id>: a commitment's ship holds it, so a second launch of the commitment is refused while the ship is listed.
	CommitmentKey string `json:"-"`
	// QuietGate makes an autopilot launch check the guard and the advisor without posting an Inbox notice when it refuses.
	QuietGate bool `json:"-"`
	// Unattended marks a launch no one asked for in the moment (a routine run, a commitment, a feedback round); only the quota hold reads it.
	Unattended bool `json:"-"`
	// TriggerID is the id of the trigger-log record the launch answers; the `launched` outcome points at it.
	TriggerID string `json:"-"`
	// WorkerLaunched marks a ship started by an outside event, which gets the fleet's Guidance in its prompt.
	WorkerLaunched bool `json:"-"`
	// Restricted launches the ship on the restricted profile at this level; empty is a normal launch.
	Restricted standing.Restricted `json:"-"`
	// EventText is the raw event; an event ship reads it from a file, never from its prompt.
	EventText string `json:"-"`
	// Template is the routine's launch text, the start of an event ship's prompt.
	Template string `json:"-"`
	// StartFrom starts the ship from a restricted ship's work: its own clone, with Branch of Clone as the start.
	StartFrom *StartFrom `json:"-"`
}

// StartFrom names the restricted clone and the branch a promoted ship starts from.
type StartFrom struct{ Clone, Branch string }

// eventShip is a normal ship an outside event started, which works in its own clone with the event in a file.
func (r Request) eventShip() bool {
	return r.Restricted == "" && r.WorkerLaunched && (r.EventText != "" || r.Template != "")
}

// ChatOption is the chat:<fleet>:<id> key the ship stores, so a fleet's message ids never meet another's;
// an anyway launch stores none, so it never holds its message.
func (r Request) ChatOption() string {
	if r.ChatKey != "" {
		return r.ChatKey
	}
	if id := strings.TrimSpace(r.ChatMessage); id != "" && !r.Anyway {
		return "chat:" + r.Fleet + ":" + id
	}
	return ""
}

// Skill is the order's skill: Commodore, a named one, or none (Just launch).
type Skill struct {
	Kind string `json:"kind"` // "commodore", "skill" or "none"
	Name string `json:"name"`
}

// AutopilotCapText opens the 409 a launch gets when autopilot is at its cap; the MCP server keys its interactive relaunch on it.
const AutopilotCapText = "autopilot is at its cap"

type Error struct {
	Status int
	Msg    string
	// Wait is set on a refusal that a later try can clear, and holds what the engineer reads while the ship waits.
	Wait string
	// Cause says which refusal this is when a caller acts on the kind, not the text: one of the Cause constants.
	Cause string
}

// Why a launch was refused, for a caller that handles each differently.
const (
	CauseCap         = "global_cap"
	CauseQuota       = "quota_hold"
	CauseResources   = "resources"
	CauseHost        = "host_unknown"
	CauseRoutineHeld = "routine_busy"
)

func (e *Error) Error() string { return e.Msg }

func refuse(status int, format string, a ...any) *Error {
	return &Error{Status: status, Msg: fmt.Sprintf(format, a...)}
}

type Launched struct {
	Session string
	Options map[string]string // without the @admiral- prefix
}

type Launcher struct {
	Tmux   string // the tmux binary
	Socket string
	Home   string // $ADMIRAL_HOME
	Port   string // the port Admiral listens on; the chat's ADMIRAL_PORT
	// BeforeChat runs just before the chat's session is created, when the launcher's
	// checks have passed: the chat service withdraws that fleet's old chat questions there.
	BeforeChat func(fleet string)
	Config     func() *config.Config
	// A kept session's name is never picked, so a launch cannot clear its inbox.
	Kept func(session string) bool
	// AutopilotGate decides whether a crew launch may carry autopilot mode. It lives
	// outside this package because the Inbox it writes to imports the launcher.
	AutopilotGate func(fleet config.Fleet, harness string) error
	// Warn gets what a launch as a crew member left out (a dropped model, ignored skills, a bad note file);
	// the server log when nil.
	Warn func(msg string)
	// Guidance is a fleet's Guidance text; a worker-launched ship's prompt carries it.
	Guidance func(fleet string) (string, error)
	// Checkouts makes a restricted ship's own checkout; nil refuses a restricted launch.
	Checkouts Checkouts

	mu sync.Mutex // one launch at a time, so two don't pick one name
}

// Checkouts makes the checkout a restricted ship works in.
type Checkouts interface {
	// Make returns the path of a new checkout of dir for the session, on branch admiral/<session>.
	Make(home, session, dir string) (path string, err error)
}

// Checked is a request that passed CheckSpec: the checkout is cleaned and Commodore resolved.
type Checked struct {
	Req   Request
	Fleet config.Fleet
	Dock  config.Harness
	Skill string
	Opts  [][2]string
	// Exe is the dock's resolved executable.
	Exe string
	// Pins, MemberArgs and MemberPrompt are what a crew launch resolved; Warnings is what it left out.
	Pins         []pinnedFile
	MemberArgs   []string
	MemberPrompt *string
	Warnings     []string
}

// CheckSpec runs every refusal of Launch that needs no live capacity, so a launch recorded for later is judged by the same rules.
// A clone request has no checkout yet: the checks that read one are left to the launch after the clone.
func CheckSpec(cfg *config.Config, home string, req Request) (Checked, *Error) {
	fleet, ok := cfg.Fleet(req.Fleet)
	if !ok {
		return Checked{}, refuse(http.StatusNotFound, "fleet %q not found", req.Fleet)
	}
	dock, ok := fleet.Harness(req.Harness)
	if !ok {
		return Checked{}, refuse(http.StatusBadRequest, "fleet %s has no %s dock", fleet.Name, req.Harness)
	}
	if !fleet.CanLaunch() {
		return Checked{}, refuse(http.StatusBadRequest, "fleet %s has no search roots, so nothing can be launched from it", fleet.Name)
	}
	skill, err := skillName(req.Skill)
	if err != nil {
		return Checked{}, asError(err)
	}
	if lerr := CheckMember(req.Skill.Kind, req.Member); lerr != nil {
		return Checked{}, lerr
	}
	if lerr := CheckPins(req.Skill.Kind, req.Crew); lerr != nil {
		return Checked{}, lerr
	}
	if _, merr := modelFlags(req.Harness, req.Model, req.Effort); merr != nil {
		return Checked{}, asError(merr)
	}
	if req.dockLaunch() {
		req.Text, req.Note = strings.TrimSpace(req.Text), ""
	}
	// The options are tmux's record of the ship, read back over the control
	// connection one line each; a control character would break the framing.
	// The name and the time are ours.
	opts := [][2]string{{"name", ""}, {"fleet", fleet.Key}, {"fleet-name", fleet.Name}, {"fleet-color", fleet.Color},
		{"harness", req.Harness}, {"dir", req.Dir}, {"skill", skill}, {"source", req.Source},
		{"order", req.OrderID}, {"launched", time.Now().UTC().Format(time.RFC3339)}, {"title", orderTitle(req)}, {"url", req.URL}, {"link", req.Link}, {"titled", titledOption(req)}, {"issues", strings.Join(req.Issues, ",")}, {"panel", req.Panel}, {"resources", strings.Join(req.Resources, ",")},
		{"model", req.Model}, {"skill-kind", req.Skill.Kind}, {"round", roundOption(req.Round)}, {"via", req.Via}, {"chat", req.ChatOption()}, {"routine", req.RoutineKey}, {"commitment", req.CommitmentKey}}
	for _, o := range opts {
		if hasControl(o[1]) {
			return Checked{}, refuse(http.StatusBadRequest, "%s contains a control character", o[0])
		}
	}
	for _, u := range req.Issues {
		if strings.Contains(u, ",") {
			return Checked{}, refuse(http.StatusBadRequest, "issues contains a comma")
		}
	}
	var pins []pinnedFile
	var memberArgs []string
	var memberPrompt *string
	var warnings []string
	if !(req.Clone && req.Dir == "") {
		st, serr := os.Stat(req.Dir)
		switch {
		case req.Dir == "" || serr != nil:
			return Checked{}, refuse(http.StatusBadRequest, "directory %s not found", req.Dir)
		case !st.IsDir():
			return Checked{}, refuse(http.StatusBadRequest, "%s is not a directory", req.Dir)
		}
		// The list is read again: a checkout can vanish, or be named in a request that never
		// came from the list.
		dir, listed := repofinder.Contains(repofinder.List(repofinder.FleetRoots(fleet.SearchRoots, fleet.CloneRoot)), req.Dir)
		if !listed {
			return Checked{}, refuse(http.StatusBadRequest, "directory %s is not a checkout or search root of fleet %s", req.Dir, fleet.Name)
		}
		req.Dir = dir
		if req.Skill.Kind == "commodore" {
			name, ok := skillindex.FindCommodore(req.Harness, dock.Environ(), req.Dir)
			ok = ok && skillindex.SafeInvocation(name)
			if !ok {
				return Checked{}, refuse(http.StatusBadRequest, "Commodore not found for %s in fleet %s", req.Harness, fleet.Name)
			}
			skill = name
		}
		if len(req.Crew) > 0 {
			abs, err := filepath.Abs(home)
			if err != nil {
				return Checked{}, refuse(http.StatusInternalServerError, "could not resolve %s: %v", home, err)
			}
			if pins, err = resolvePins(req, dock, abs, fleet.Key); err != nil {
				return Checked{}, asError(err)
			}
		}
		if req.Member != "" {
			got, lerr := resolveMember(home, memberAsk{Name: req.Member, Fleet: fleet, Explicit: req.Harness, Default: req.Harness,
				Dir: req.Dir, Prompt: promptFor(req.Harness, skill, req.Skill.Kind, req.Note, req.Text), Model: req.Model, Args: req.ExtraArgs})
			if lerr != nil {
				return Checked{}, lerr
			}
			memberArgs, memberPrompt, warnings = got.ModelArgs, &got.Prompt, got.Warnings
		}
	}
	// opts was built before the directory was cleaned and Commodore resolved.
	for i := range opts {
		switch opts[i][0] {
		case "dir":
			opts[i][1] = req.Dir
		case "skill":
			opts[i][1] = skill
		}
	}
	exe, found := dock.Resolve()
	if !found {
		return Checked{}, refuse(http.StatusBadRequest, "%s executable %s not found for fleet %s", req.Harness, dock.Executable, fleet.Name)
	}
	if req.Harness == "codex" {
		if bad, why := codexDockArgRefused(dock.Args); bad != "" {
			return Checked{}, refuse(http.StatusBadRequest, "the %s dock of fleet %s has %q in its args, which Admiral refuses because %s", req.Harness, fleet.Name, bad, why)
		}
	}
	if req.Restricted != "" {
		if lerr := checkRestricted(req, dock); lerr != nil {
			return Checked{}, lerr
		}
	}
	return Checked{Req: req, Fleet: fleet, Dock: dock, Skill: skill, Opts: opts, Exe: exe, Pins: pins, MemberArgs: memberArgs, MemberPrompt: memberPrompt, Warnings: warnings}, nil
}

func asError(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return refuse(http.StatusBadRequest, "%v", err)
}

// Nothing is created when Launch refuses.
func (l *Launcher) Launch(req Request) (*Launched, error) {
	if l.Tmux == "" {
		return nil, refuse(http.StatusServiceUnavailable, "tmux not found on PATH, so ships cannot be launched")
	}
	chk, lerr := CheckSpec(l.Config(), l.Home, req)
	if lerr != nil {
		return nil, lerr
	}
	req, fleet, dock, skill, opts := chk.Req, chk.Fleet, chk.Dock, chk.Skill, chk.Opts
	mflags, _ := modelFlags(req.Harness, req.Model, req.Effort)
	pins, memberArgs, memberPrompt, exe := chk.Pins, chk.MemberArgs, chk.MemberPrompt, chk.Exe
	l.warn(chk.Warnings)
	argv := append([]string{exe}, dock.Args...)
	argv = append(argv, mflags...)
	// The member rule, not modelFlags' list, has judged a member's model.
	argv = append(argv, memberArgs...)
	argv = append(argv, req.ExtraArgs...)
	sessionFlags, paneOpts := conversationAnchor(req, dock, time.Now())
	argv = append(argv, sessionFlags...)
	if len(sessionFlags) > 0 && req.Harness == "copilot" {
		// Only a session Admiral named is observed, so the server rides with the session id.
		argv = append(argv, uiServerLaunchFlags(req, dock, exe)...)
	}
	prompt := promptFor(req.Harness, skill, req.Skill.Kind, req.Note, req.Text)
	if memberPrompt != nil {
		prompt = *memberPrompt
	}
	if req.Prompt != "" {
		prompt = req.Prompt
	}
	prompt = l.withGuidance(req, prompt)
	flags, known := l.hookFlags(req.Harness)
	if !known {
		return nil, refuse(http.StatusBadRequest, "unknown harness %q", req.Harness)
	}
	restricted := req.Restricted != ""
	evented := req.eventShip()
	promoted := req.StartFrom != nil
	if (restricted || evented || promoted) && l.Checkouts == nil {
		return nil, refuse(http.StatusServiceUnavailable, "event ships cannot be launched: no checkout maker is configured")
	}
	addPrompt := func(argv []string, prompt string) []string {
		if req.Harness == "copilot" {
			if prompt != "" {
				return append(argv, "--interactive="+prompt)
			}
			return argv
		}
		return appendPrompt(argv, prompt, req.dockLaunch())
	}
	// An event ship's checkout and prompt depend on its session, so they come after nameFor.
	if !restricted && !evented {
		argv = append(argv, flags...)
		argv = addPrompt(argv, prompt)
	}
	if req.Harness == "codex" {
		sock, err := codexpane.SocketPath(os.Getuid())
		if err != nil {
			return nil, refuse(http.StatusInternalServerError, "could not name the Codex socket: %v", err)
		}
		argv = append([]string{filepath.Join(l.Home, "bin", "admiral"), "codex-pane", "--socket", sock, "--"}, argv...)
		paneOpts = append(paneOpts, [2]string{"codex-socket", sock})
	}
	env := l.shipEnv(dock)
	tooLong := func() error {
		if n := sessionhost.CommandSize(argv); n > sessionhost.MaxMessage {
			return refuse(http.StatusBadRequest, "the order is too long to launch: its command line is %d bytes and tmux carries at most %d", n, sessionhost.MaxMessage)
		}
		return nil
	}
	if err := tooLong(); err != nil {
		return nil, err
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	name, session, err := l.nameFor(req)
	if err != nil {
		return nil, err
	}
	opts[0][1] = name
	launched := false
	if restricted || evented || promoted {
		var made restrictedShip
		if restricted {
			argv, made, err = l.restrictedSetup(req, session, argv)
		} else if promoted {
			made, err = l.startFromSetup(req, session)
		} else {
			argv, made, err = l.eventSetup(req, session, skill, argv, flags, addPrompt)
		}
		if err != nil {
			return nil, err
		}
		defer func() {
			if !launched {
				made.remove(l.Home, session)
			}
		}()
		req.Dir = made.checkout
		for i := range opts {
			if opts[i][0] == "dir" {
				opts[i][1] = made.checkout
			}
		}
		if err := tooLong(); err != nil {
			return nil, err
		}
	}
	if req.Before != nil {
		if err := req.Before(req.Dir); err != nil {
			return nil, err
		}
	}
	if err := l.shipContract(env, session, fleet.Key); err != nil {
		return nil, err
	}
	var runCrew string
	if len(pins) > 0 {
		home, _ := filepath.Abs(l.Home)
		if runCrew, err = writeRunCrew(home, session, pins); err != nil {
			return nil, err
		}
	}

	// Admiral owns these names (ADMIRAL_PORT, ADMIRAL_CHAT_FLEET and ADMIRAL_CHAT_HARNESS are the chat's alone): a hand launch has none of them, an autopilot
	// launch has ADMIRAL_AUTOPILOT and ADMIRAL_LOW_STAKES=advisor, and a launch with pins has ADMIRAL_RUN_CREW, even if
	// the fleet's env or Admiral's own environment (which the tmux server
	// inherits) sets them.
	var unset []string
	ours := map[string]string{}
	if req.Autopilot {
		ours["ADMIRAL_AUTOPILOT"] = "1"
		ours["ADMIRAL_LOW_STAKES"] = "advisor"
	}
	if runCrew != "" {
		ours["ADMIRAL_RUN_CREW"] = runCrew
	}
	for _, k := range []string{"ADMIRAL_AUTOPILOT", "ADMIRAL_LOW_STAKES", "ADMIRAL_AUTO_MERGE", "ADMIRAL_PORT", "ADMIRAL_CHAT_FLEET", "ADMIRAL_CHAT_HARNESS", "ADMIRAL_PROBE_NONCE", "ADMIRAL_PROBE_DIR", "ADMIRAL_RUN_CREW"} {
		if v, ok := ours[k]; ok {
			env[k] = v
			continue
		}
		delete(env, k)
		unset = append(unset, k)
	}
	if restricted {
		unset = append(unset, ScrubRestrictedEnv(env)...)
	}

	// A Commodore on Claude compacts at 200k tokens; only the fleet's own env
	// overrides that, not a value Admiral inherited.
	if req.Skill.Kind == "commodore" && req.Harness == "claude" {
		if _, set := dock.Env["CLAUDE_CODE_AUTO_COMPACT_WINDOW"]; !set {
			env["CLAUDE_CODE_AUTO_COMPACT_WINDOW"] = "200000"
		}
	}

	if err := l.createSession(session, req.Dir, opts, paneOpts, env, unset, argv, true); err != nil {
		if runCrew != "" {
			home, _ := filepath.Abs(l.Home)
			_ = removeRunCrew(home, session)
		}
		return nil, err
	}
	launched = true
	out := &Launched{Session: session, Options: map[string]string{}}
	for _, o := range opts {
		out.Options[o[0]] = o[1]
	}
	return out, nil
}

// A source's order is titled at launch, and a later round keeps its first round's title.
func titledOption(req Request) string {
	if req.Source != "" || req.Round > 0 {
		return "1"
	}
	return ""
}

func roundOption(round int) string {
	if round <= 0 {
		return ""
	}
	return strconv.Itoa(round)
}

// nameFor is the name a launch takes: a relaunch keeps its own and refuses while that session is live;
// the caller holds l.mu, so no other launch takes the name in between.
func (l *Launcher) nameFor(req Request) (name, session string, err error) {
	if req.Session == "" {
		name, err = l.pickName()
		return name, Slug(name), err
	}
	rows, err := l.server().SessionRows()
	if err != nil {
		return "", "", refuse(http.StatusInternalServerError, "tmux could not list its sessions: %s", err)
	}
	for _, r := range rows {
		if r[0] == req.Session {
			return "", "", refuse(http.StatusConflict, "ship %s is already live", req.Session)
		}
	}
	return req.Name, req.Session, nil
}

func (l *Launcher) hookFlags(harness string) (flags []string, known bool) {
	switch harness {
	case "claude":
		return []string{"--settings", ClaudeSettingsPath(l.Home)}, true
	case "codex":
		return codexFlags(), true
	case "copilot":
		return []string{"--plugin-dir", copilotPluginDir(l.Home)}, true
	}
	return nil, false
}

// dockLaunch is a launch from a dock with no order: the text is the prompt.
func (r Request) dockLaunch() bool { return r.Source == "" && r.OrderID == "" }

// appendPrompt ends argv with the prompt after "--". A dock launch with no
// prompt starts the harness bare, with no "--" either.
func appendPrompt(argv []string, prompt string, dock bool) []string {
	if dock && prompt == "" {
		return argv
	}
	argv = append(argv, "--")
	if prompt != "" {
		argv = append(argv, prompt)
	}
	return argv
}

// The title never refuses a launch. A launch with an order id but no source has no title.
func orderTitle(req Request) string {
	if req.Source == "" && req.OrderID != "" {
		return ""
	}
	line := req.Text
	if req.Title != "" {
		line = req.Title
	}
	return CleanTitle(line)
}

// CleanTitle is what a title may carry into a tmux option or a log line.
func CleanTitle(s string) string {
	first, _, _ := strings.Cut(strings.TrimLeft(s, " \t\r\n"), "\n")
	first = strings.Map(func(r rune) rune {
		if isControl(r) {
			return -1
		}
		return r
	}, first)
	if r := []rune(first); len(r) > 200 {
		first = string(r[:200])
	}
	return strings.TrimSpace(first)
}

func hasControl(s string) bool {
	return strings.IndexFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0
}

// isControl is a character a title must not carry: C0 and C1 controls, the bidi
// controls and the line and paragraph separators.
func isControl(r rune) bool {
	return r < 0x20 || (r >= 0x7f && r <= 0x9f) || (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) || r == 0x2028 || r == 0x2029
}

// CheckSkill is the launch form's check of a skill: the error a launch would refuse it with.
func CheckSkill(s Skill) error {
	_, err := skillName(s)
	return err
}

// skillName is the skill's name without its / or $ prefix; "" for Just launch
// and for Commodore, whose name Launch looks up in the skill index.
func skillName(s Skill) (string, error) {
	switch s.Kind {
	case "commodore", "none":
		return "", nil
	case "skill":
		name := s.Name
		if strings.HasPrefix(name, "/") || strings.HasPrefix(name, "$") {
			name = name[1:] // one prefix, not both
		}
		if name == "" {
			return "", refuse(http.StatusBadRequest, "skill name is empty")
		}
		if strings.IndexFunc(name, unicode.IsSpace) >= 0 {
			return "", refuse(http.StatusBadRequest, "skill name %q contains whitespace", s.Name)
		}
		return name, nil
	}
	return "", refuse(http.StatusBadRequest, "skill kind %q is not commodore, skill or none", s.Kind)
}

// promptFor builds the prompt argument: /skill text (Claude Code, Copilot) or
// $skill text (Codex), the text trimmed; Just launch sends the text as written,
// after the note and a blank line when there is one.
func promptFor(harness, skill, kind, note, text string) string {
	if kind == "none" {
		switch note = strings.TrimSpace(note); {
		case note == "":
			return text
		case text == "":
			return note
		}
		return note + "\n\n" + text
	}
	mark := skillindex.Mark(harness)
	text = strings.TrimSpace(text)
	if text == "" {
		return mark + skill
	}
	return mark + skill + " " + text
}

// shipEnv is the fleet's environment over Admiral's, minus the tmux variables
// the pane gets from its own tmux, with $ADMIRAL_HOME/bin first on PATH (hooks
// run `admiral`) and the tmux directory last.
func (l *Launcher) shipEnv(dock config.Harness) map[string]string {
	env := map[string]string{}
	for _, kv := range dock.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok && k != "TMUX" && k != "TMUX_PANE" {
			env[k] = v
		}
	}
	path := filepath.Join(l.Home, "bin")
	if p := env["PATH"]; p != "" {
		path += string(os.PathListSeparator) + p
	}
	env["PATH"] = path + string(os.PathListSeparator) + filepath.Dir(l.Tmux)
	return env
}

// ShipFiles are the report file and the inbox directory of a ship under the
// home, the paths ADMIRAL_REPORT and ADMIRAL_INBOX name. ok is false for a
// session name that would reach outside reports/ and inbox/.
func ShipFiles(home, session string) (report, inbox string, ok bool) {
	if session == "." || !filepath.IsLocal(session) || strings.ContainsRune(session, filepath.Separator) {
		return "", "", false
	}
	return filepath.Join(home, "reports", session+".json"), filepath.Join(home, "inbox", session), true
}

// RemoveShipFiles deletes a ship's report, inbox and run layer. The deletes go through a
// root on home, so none leaves it, and each is skipped unless reports/ or
// inbox/ is a real directory and not a link.
func RemoveShipFiles(home, session string) {
	report, inbox, ok := ShipFiles(home, session)
	if !ok {
		return
	}
	root, err := os.OpenRoot(home)
	if err != nil {
		return
	}
	defer root.Close()
	if rel, err := filepath.Rel(home, report); err == nil && realDir(root, filepath.Dir(rel)) {
		_ = root.Remove(rel)
	}
	if rel, err := filepath.Rel(home, inbox); err == nil && realDir(root, filepath.Dir(rel)) {
		_ = root.RemoveAll(rel)
	}
	_ = removeRunCrew(home, session)
	RemoveRestrictedFiles(home, session)
}

// A link would send a delete under it somewhere else.
func realDir(root *os.Root, name string) bool {
	fi, err := root.Lstat(name)
	return err == nil && fi.IsDir()
}

// A name can be reused, so any report or inbox left under it by a ship that never sailed
// off is removed.
func (l *Launcher) shipContract(env map[string]string, session, fleetKey string) error {
	home, err := filepath.Abs(l.Home)
	if err != nil {
		return refuse(http.StatusInternalServerError, "could not resolve %s: %v", l.Home, err)
	}
	report, inbox, ok := ShipFiles(home, session)
	if !ok {
		return refuse(http.StatusInternalServerError, "%q is not a name a ship can have", session)
	}
	crewDir, err := fleetCrewDir(home, fleetKey)
	if err != nil {
		return err
	}
	if err := removeRunCrew(home, session); err != nil {
		return refuse(http.StatusInternalServerError, "could not clear the run layer of %s: %v", session, err)
	}
	for _, p := range []string{report, inbox} {
		if err := os.RemoveAll(p); err != nil {
			return refuse(http.StatusInternalServerError, "could not clear %s: %v", p, err)
		}
	}
	for _, d := range []string{filepath.Dir(report), inbox} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return refuse(http.StatusInternalServerError, "could not create %s: %v", d, err)
		}
	}
	env["ADMIRAL_CREW_DIR"] = crewDir
	env["ADMIRAL_SHIP"] = session
	env["ADMIRAL_REPORT"] = report
	env["ADMIRAL_INBOX"] = inbox + string(os.PathSeparator)
	return nil
}

// The fleet's crew directory outlives its ships.
func fleetCrewDir(home, fleetKey string) (string, error) {
	dir := home
	for _, part := range []string{"fleets", fleetKey, "crew"} {
		dir = filepath.Join(dir, part)
		fi, err := os.Lstat(dir)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			err := os.Mkdir(dir, 0o700)
			if errors.Is(err, fs.ErrExist) {
				// Made by someone else since the Lstat; check what it is.
				fi, err = os.Lstat(dir)
				if err == nil && !fi.IsDir() {
					return "", crewDirNotDir(dir, fi)
				}
			}
			if err != nil {
				return "", refuse(http.StatusInternalServerError, "could not create %s: %v", dir, err)
			}
		case err != nil:
			return "", refuse(http.StatusInternalServerError, "could not inspect %s: %v", dir, err)
		case !fi.IsDir():
			return "", crewDirNotDir(dir, fi)
		}
	}
	return dir, nil
}

func crewDirNotDir(dir string, fi fs.FileInfo) error {
	if fi.Mode()&fs.ModeSymlink != 0 {
		return refuse(http.StatusInternalServerError, "%s is a link, not a directory", dir)
	}
	return refuse(http.StatusInternalServerError, "%s is not a directory", dir)
}

// No tmux server yet means nothing is in use; any other error stops the launch, since a
// name in use would end with a clash.
func (l *Launcher) pickName() (string, error) {
	rows, err := l.server().SessionRows()
	if err != nil {
		return "", refuse(http.StatusInternalServerError, "tmux could not list its sessions: %s", err)
	}
	sessions, shipNames := map[string]bool{}, map[string]bool{}
	for _, r := range rows {
		sessions[r[0]], shipNames[r[1]] = true, true
	}
	moored, _ := moor.List(l.Home)
	for _, r := range moored {
		shipNames[r.Name], sessions[r.Session] = true, true
	}
	// A restricted ship's clone outlives the ship, and Make refuses a path that exists.
	kept := func(session string) bool {
		if _, err := os.Lstat(filepath.Join(l.Home, "checkouts", session)); err == nil {
			return true
		}
		return l.Kept != nil && l.Kept(session)
	}
	return pickName(taken(shipNames, sessions, kept)), nil
}

// A kept inbox takes a name too, and so does a moored ship's record.
func taken(shipNames, sessions map[string]bool, kept func(session string) bool) func(name string) bool {
	return func(n string) bool {
		return shipNames[n] || sessions[Slug(n)] || (kept != nil && kept(Slug(n)))
	}
}

func (l *Launcher) createSession(session, dir string, opts, paneOpts [][2]string, env map[string]string, unset, argv []string, anchor bool) error {
	err := l.server().StartSession(sessionhost.SessionSpec{Name: session, Dir: dir, Options: opts, PaneOptions: paneOpts, Env: env, Unset: unset, Argv: argv, Anchor: anchor})
	var limit *sessionhost.LimitError
	if errors.As(err, &limit) {
		return refuse(http.StatusBadRequest, "%s", limit.Msg)
	}
	if err != nil {
		return refuse(http.StatusInternalServerError, "%s", err)
	}
	return nil
}

func (l *Launcher) server() sessionhost.Server { return sessionhost.NewServer(l.Tmux, l.Socket) }
