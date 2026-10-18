package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/sessionhost"
)

// Setup is `admiral setup [--answers <file> --yes] [--dry-run]` (stories 12-18).
//
// The answers file is JSON holding the same fleets object as config.json:
//
//	{"fleets": {"work": {"name": "Work", "search_roots": ["~/code"],
//	                     "claude": {"env": {"CLAUDE_CONFIG_DIR": "~/.claude-work"}}}}}
//
// Fleets keep the order of the file. A harness executable left out is looked up
// on PATH and written as the absolute path found; the identity variable is
// always written (the harness default when the file leaves it out). A fleet
// already in config.json is updated field by field (env entries are merged) and
// keeps its place; fleets and top-level keys the answers don't name are left
// alone.
func Setup(args []string, std IO) error {
	err := setup(args, std)
	if err != nil && os.Getenv(sessionhost.SetupTerminalEnv) != "" {
		// In the browser's setup terminal the session ends with setup, and the terminal
		// closes with it: hold the error on screen until it has been read.
		fmt.Fprintln(std.Err, "admiral:", err)
		fmt.Fprint(std.Out, "\nPress Enter to close.")
		_, _ = bufio.NewReader(std.In).ReadString('\n')
	}
	return err
}

func setup(args []string, std IO) error {
	fs := newFlags("setup")
	answers := fs.String("answers", "", "JSON file with the fleets to set up")
	yes := fs.Bool("yes", false, "write without asking (needs --answers)")
	dry := fs.Bool("dry-run", false, "show what would change and write nothing")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usagef("setup takes no arguments, got %q", fs.Arg(0))
	}
	if *yes && *answers == "" {
		return usagef("--yes needs --answers: without answers setup has to ask")
	}
	dir, err := admiralHome()
	if err != nil {
		return err
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	det := Detect(userHome, os.Getenv("PATH"), dir)
	det.Report(std.Out)

	current, err := config.Fleets(dir)
	if err != nil {
		return err
	}
	s := &session{io: std, det: det, dir: dir, fleets: current}
	if *answers != "" {
		if err := s.fromFile(*answers); err != nil {
			return err
		}
	} else if err := s.ask(); err != nil {
		return err
	}
	if len(s.touched) == 0 {
		fmt.Fprintln(std.Out, "No fleets to set up.")
		return nil
	}

	p, err := newPlan(dir, s.fleets, s.touched)
	if err != nil {
		return err
	}
	if !printDiff(std.Out, p.before, p.after) {
		return nil
	}
	if *dry {
		fmt.Fprintln(std.Out, "Dry run: nothing was changed.")
		return nil
	}
	if !*yes {
		ok, err := confirm(s.reader(), std.Out, "Write config.json?")
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(std.Out, "Nothing changed.")
			return nil
		}
	}
	if err := p.write(dir); err != nil {
		return err
	}
	fmt.Fprintf(std.Out, "Saved %s\n", filepath.Join(dir, config.FileName))
	return nil
}

// session is one run of setup: the fleets as they will be written.
type session struct {
	io      IO
	det     Detection
	dir     string
	fleets  *config.Object
	touched []string
	in      *bufio.Reader
}

func (s *session) reader() *bufio.Reader {
	if s.in == nil {
		s.in = bufio.NewReader(s.io.In)
	}
	return s.in
}

func (s *session) touch(key string) {
	for _, k := range s.touched {
		if k == key {
			return
		}
	}
	s.touched = append(s.touched, key)
}

// fromFile merges the fleets of an answers file.
func (s *session) fromFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	top, err := config.ParseObject(data)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	raw, ok := top.Get("fleets")
	if !ok {
		return fmt.Errorf(`%s: expected {"fleets": {...}}`, path)
	}
	answers, err := config.ParseObject(raw)
	if err != nil {
		return fmt.Errorf("%s: fleets: %w", path, err)
	}
	for _, key := range answers.Keys() {
		raw, _ := answers.Get(key)
		a, err := config.ParseObject(raw)
		if err != nil {
			return fmt.Errorf("%s: fleets.%s: %w", path, key, err)
		}
		if err := s.merge(key, a); err != nil {
			return err
		}
	}
	return nil
}

// merge applies one fleet's answers to the fleets.
func (s *session) merge(key string, a *config.Object) error {
	fleet := config.NewObject()
	if raw, ok := s.fleets.Get(key); ok {
		var err error
		if fleet, err = config.ParseObject(raw); err != nil {
			return fmt.Errorf("fleets.%s: %w", key, err)
		}
	}
	for _, k := range a.Keys() {
		raw, _ := a.Get(k)
		if !isHarness(k) {
			fleet.Set(k, raw) // an unknown key is for the parser to refuse, naming its path
			continue
		}
		dock, err := dockOf(fleet, k)
		if err != nil {
			return fmt.Errorf("fleets.%s.%s: %w", key, k, err)
		}
		given, err := config.ParseObject(raw)
		if err != nil {
			return fmt.Errorf("fleets.%s.%s: %w", key, k, err)
		}
		if err := s.mergeDock(dock, given, k); err != nil {
			return fmt.Errorf("fleets.%s.%s: %w", key, k, err)
		}
		fleet.Set(k, dock.JSON())
	}
	s.fleets.Set(key, fleet.JSON())
	s.touch(key)
	return nil
}

func (s *session) mergeDock(dock, given *config.Object, harness string) error {
	for _, k := range given.Keys() {
		raw, _ := given.Get(k)
		switch k {
		case "env":
			env, err := envOf(dock)
			if err != nil {
				return err
			}
			add, err := config.ParseObject(raw)
			if err != nil {
				return fmt.Errorf("env: %w", err)
			}
			for _, name := range add.Keys() {
				v, _ := add.Get(name)
				env.Set(name, v)
			}
			dock.Set("env", env.JSON())
		case "executable":
			var exe string
			if json.Unmarshal(raw, &exe) == nil && exe != "" && !strings.Contains(exe, "/") {
				if p, ok := s.det.Lookup(exe); ok {
					raw = mustRaw(p)
				}
			}
			dock.Set(k, raw)
		default:
			dock.Set(k, raw)
		}
	}
	if _, ok := dock.Get("executable"); !ok {
		if p, found := s.det.Harness[harness]; found {
			dock.Set("executable", mustRaw(p))
		} else {
			fmt.Fprintf(s.io.Err, "setup: %s was not found; the fleet will look it up when it launches\n", harness)
		}
	}
	_, def, _ := config.IdentityVar(harness)
	return withIdentity(dock, harness, def)
}

func mustRaw(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// ---- diff ----

// printDiff prints the lines that differ between two indented documents, "+"
// for added and "-" for removed. A line that differs only in whitespace is not
// a change. It reports whether anything differs.
func printDiff(w io.Writer, before, after []byte) bool {
	a, b := splitLines(before), splitLines(after)
	trim := func(s string) string { return strings.TrimSpace(s) }
	// Longest common subsequence of the trimmed lines.
	n, m := len(a), len(b)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if trim(a[i]) == trim(b[j]) {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var out []string
	for i, j := 0, 0; i < n || j < m; {
		switch {
		case i < n && j < m && trim(a[i]) == trim(b[j]):
			i++
			j++
		case j < m && (i == n || lcs[i][j+1] >= lcs[i+1][j]):
			out = append(out, "+"+b[j])
			j++
		default:
			out = append(out, "-"+a[i])
			i++
		}
	}
	if len(out) == 0 {
		fmt.Fprintln(w, "No changes.")
		return false
	}
	fmt.Fprintln(w, "Changes to config.json:")
	for _, l := range out {
		fmt.Fprintln(w, l)
	}
	return true
}

func splitLines(b []byte) []string {
	s := strings.TrimRight(string(b), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// ---- questions ----

var fleetKeyRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
var colorRe = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

var errInputEnded = errors.New("input ended before setup was finished")

// prompt asks one question and returns the trimmed answer, def when it is
// empty. The label is the text the terminal shows.
func prompt(in *bufio.Reader, w io.Writer, label, def string) (string, error) {
	if def != "" {
		fmt.Fprintf(w, "%s [%s]: ", label, def)
	} else {
		fmt.Fprintf(w, "%s: ", label)
	}
	line, err := in.ReadString('\n')
	if err != nil && line == "" {
		if errors.Is(err, io.EOF) {
			return "", errInputEnded
		}
		return "", err
	}
	if line = strings.TrimSpace(line); line != "" {
		return line, nil
	}
	return def, nil
}

func confirm(in *bufio.Reader, w io.Writer, label string) (bool, error) {
	for {
		a, err := prompt(in, w, label+" [y/N]", "")
		if err != nil {
			return false, err
		}
		switch strings.ToLower(a) {
		case "y", "yes":
			return true, nil
		case "", "n", "no":
			return false, nil
		}
	}
}

// ask runs the questions, one fleet after another.
func (s *session) ask() error {
	in, w := s.reader(), s.io.Out
	fmt.Fprintln(w, "\nAdmiral setup. Press Enter to take the answer in [brackets].")
	taken := s.identityDirsInUse()
	for {
		key, err := s.askKey(in)
		if err != nil {
			return err
		}
		a, err := s.askFleet(in, key, taken)
		if err != nil {
			return err
		}
		if err := s.merge(key, a); err != nil {
			return err
		}
		more, err := confirm(in, w, "Add another fleet")
		if err != nil {
			return err
		}
		if !more {
			return nil
		}
	}
}

func (s *session) askKey(in *bufio.Reader) (string, error) {
	for {
		key, err := prompt(in, s.io.Out, "Fleet key (letters, digits, - and _)", "")
		if err != nil {
			return "", err
		}
		if fleetKeyRe.MatchString(key) {
			return key, nil
		}
	}
}

// known is what the file already holds for a fleet key: the defaults of a
// re-run, so that answering every question with Enter changes nothing (story 17).
type known struct {
	name, color string
	roots       []string
	harnesses   []string
	exe, dir    map[string]string // by harness: executable, identity directory
}

func (s *session) known(key string) known {
	k := known{exe: map[string]string{}, dir: map[string]string{}}
	raw, ok := s.fleets.Get(key)
	if !ok {
		return k
	}
	fleet, err := config.ParseObject(raw)
	if err != nil {
		return k
	}
	str := func(o *config.Object, field string) string {
		var v string
		if r, ok := o.Get(field); ok {
			_ = json.Unmarshal(r, &v)
		}
		return v
	}
	k.name, k.color = str(fleet, "name"), str(fleet, "color")
	r, has := fleet.Get("search_roots")
	k.roots = stringsOf(r, has)
	for _, h := range config.Harnesses() {
		dock, err := dockOf(fleet, h)
		if err != nil {
			continue
		}
		if _, has := fleet.Get(h); !has {
			continue
		}
		k.harnesses = append(k.harnesses, h)
		k.exe[h] = str(dock, "executable")
		if env, err := envOf(dock); err == nil {
			v, _, _ := config.IdentityVar(h)
			k.dir[h] = str(env, v)
		}
	}
	return k
}

func (s *session) askFleet(in *bufio.Reader, key string, taken map[string]string) (*config.Object, error) {
	w := s.io.Out
	have := s.known(key)
	a := config.NewObject()
	defName := strings.ToUpper(key[:1]) + key[1:]
	if have.name != "" {
		defName = have.name
	}
	name, err := prompt(in, w, "Fleet name", defName)
	if err != nil {
		return nil, err
	}
	a.SetValue("name", name)

	next := config.PaletteColor(s.fleets.Len())
	if _, exists := s.fleets.Get(key); exists {
		next = have.color // a fleet without a color keeps the next palette color it is given
	}
	for {
		color, err := prompt(in, w, "Color", next)
		if err != nil {
			return nil, err
		}
		if color == "" {
			break
		}
		if colorRe.MatchString(color) {
			a.SetValue("color", color)
			break
		}
		fmt.Fprintln(w, "A color looks like #RRGGBB.")
	}

	detected := have.harnesses
	if len(detected) == 0 {
		for _, h := range harnessNames {
			if _, ok := s.det.Harness[h]; ok {
				detected = append(detected, h)
			}
		}
	}
	var chosen []string
	for len(chosen) == 0 {
		answer, err := prompt(in, w, "Harnesses (comma-separated)", strings.Join(detected, ","))
		if err != nil {
			return nil, err
		}
		for _, h := range strings.Split(answer, ",") {
			if h = strings.TrimSpace(h); isHarness(h) {
				chosen = append(chosen, h)
			}
		}
	}
	docks := map[string]*config.Object{}
	for _, h := range chosen {
		dock := config.NewObject()
		defExe := s.det.Harness[h]
		if have.exe[h] != "" {
			defExe = have.exe[h]
		}
		exe, err := prompt(in, w, "Executable for "+h, defExe)
		if err != nil {
			return nil, err
		}
		if exe != "" {
			dock.SetValue("executable", exe)
		}
		v, def, _ := config.IdentityVar(h)
		suggest := def
		if have.dir[h] != "" {
			suggest = have.dir[h]
		} else if owner, used := taken[h+"\x00"+expandTilde(def, s.det.Home)]; used && owner != key {
			suggest = def + "-" + key
		}
		dirAnswer, err := prompt(in, w, "Identity directory for "+h, suggest)
		if err != nil {
			return nil, err
		}
		env := config.NewObject()
		env.SetValue(v, dirAnswer)
		dock.Set("env", env.JSON())
		docks[h] = dock
		taken[h+"\x00"+expandTilde(dirAnswer, s.det.Home)] = key
	}

	fmt.Fprintln(w, "Extra variables (NAME=value lines, blank to finish):")
	extra := map[string]string{}
	var extraOrder []string
	for {
		line, err := prompt(in, w, "  NAME=value", "")
		if err != nil {
			return nil, err
		}
		if line == "" {
			break
		}
		n, v, ok := strings.Cut(line, "=")
		if !ok || n == "" {
			fmt.Fprintln(w, "Write it as NAME=value.")
			continue
		}
		if _, dup := extra[n]; !dup {
			extraOrder = append(extraOrder, n)
		}
		extra[n] = v
	}
	for _, h := range chosen {
		if len(extraOrder) == 0 {
			break
		}
		env, _ := envOf(docks[h])
		for _, n := range extraOrder {
			env.SetValue(n, extra[n])
		}
		docks[h].Set("env", env.JSON())
	}

	if len(s.det.Suggested) > 0 {
		fmt.Fprintln(w, "Folders under your home that hold git checkouts (none is picked for you):")
		for _, p := range s.det.Suggested {
			fmt.Fprintf(w, "  %s\n", p)
		}
	}
	roots := []string{}
	for {
		answer, err := prompt(in, w, "Search roots (comma-separated)", strings.Join(have.roots, ", "))
		if err != nil {
			return nil, err
		}
		roots = roots[:0]
		bad := ""
		for _, r := range strings.Split(answer, ",") {
			if r = strings.TrimSpace(r); r == "" {
				continue
			}
			if r != "~" && !strings.HasPrefix(r, "~/") && !filepath.IsAbs(r) {
				bad = r
			}
			roots = append(roots, r)
		}
		if bad == "" {
			break
		}
		fmt.Fprintf(w, "%q is not an absolute path or one starting with ~/.\n", bad)
	}
	a.SetValue("search_roots", roots)
	for _, h := range chosen {
		a.Set(h, docks[h].JSON())
	}
	return a, nil
}

func expandTilde(p, userHome string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		return filepath.Join(userHome, p[1:])
	}
	return filepath.Clean(p)
}

// identityDirsInUse maps harness+directory to the fleet using it, for the
// default a new fleet is offered.
func (s *session) identityDirsInUse() map[string]string {
	used := map[string]string{}
	cfg, err := config.Parse([]byte(`{"version":1,"fleets":` + string(s.fleets.JSON()) + `}`))
	if err != nil {
		return used
	}
	for _, f := range cfg.Fleets {
		for _, h := range f.Harnesses {
			used[h.Name+"\x00"+config.IdentityDir(h.Name, h.Env)] = f.Key
		}
	}
	return used
}
