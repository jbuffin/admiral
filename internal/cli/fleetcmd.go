package cli

import (
	"fmt"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/jbuffin/admiral/internal/config"
)

// Fleet is `admiral fleet list | add | edit | check` (stories 22, 23).
func Fleet(args []string, std IO) error {
	if len(args) == 0 {
		return usagef("usage: admiral fleet list | add <key> | edit <key> | check [<key>]")
	}
	switch args[0] {
	case "list":
		return fleetList(std)
	case "add":
		return fleetChange(args[1:], std, true)
	case "edit":
		return fleetChange(args[1:], std, false)
	case "check":
		return fleetCheck(args[1:], std)
	}
	return usagef("unknown command %q: want list, add, edit or check", "fleet "+args[0])
}

func loadConfig() (*config.Config, error) {
	dir, err := admiralHome()
	if err != nil {
		return nil, err
	}
	data, err := config.ReadConfig(dir)
	if err != nil {
		return nil, err
	}
	return config.Parse(data)
}

func fleetList(std IO) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(std.Out, 0, 4, 2, ' ', 0)
	for _, f := range cfg.Fleets {
		var hs []string
		for _, h := range f.Harnesses {
			hs = append(hs, h.Name)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", f.Key, f.Name, strings.Join(hs, ","))
	}
	return tw.Flush()
}

// fleetCheck resolves each executable (PATH, file mode) and never runs it.
func fleetCheck(args []string, std IO) error {
	fs := newFlags("fleet check")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return usagef("usage: admiral fleet check [<key>]")
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	fleets := cfg.Fleets
	if fs.NArg() == 1 {
		f, ok := cfg.Fleet(fs.Arg(0))
		if !ok {
			return fmt.Errorf("no fleet %q", fs.Arg(0))
		}
		fleets = []config.Fleet{f}
	}
	missing := 0
	for _, f := range fleets {
		for _, h := range f.Harnesses {
			if p, ok := h.Resolve(); ok {
				fmt.Fprintf(std.Out, "%s %s ok %s\n", f.Key, h.Name, p)
			} else {
				missing++
				fmt.Fprintf(std.Out, "%s %s missing %s\n", f.Key, h.Name, h.Executable)
			}
		}
	}
	if missing > 0 {
		return fmt.Errorf("%d executable(s) not found", missing)
	}
	return nil
}

// fleetChange is add (create) and edit. The key comes first, then flags; every
// flag may repeat. A harness named by --executable, --arg or --env needs no
// --harness. On edit, the --arg values given for a harness replace its
// arguments; --env sets one variable and --unset-env removes one.
func fleetChange(args []string, std IO, create bool) error {
	verb := "edit"
	if create {
		verb = "add"
	}
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return usagef("usage: admiral fleet %s <key> [flags]", verb)
	}
	key := args[0]
	fs := newFlags("fleet " + verb)
	name := fs.String("name", "", "the fleet's name")
	color := fs.String("color", "", "#RRGGBB")
	var roots, rmRoots, harness, exe, arg, env, unset list
	fs.Var(&roots, "search-root", "add a search root")
	fs.Var(&rmRoots, "remove-search-root", "remove a search root")
	fs.Var(&harness, "harness", "give the fleet a dock for claude, codex or copilot")
	fs.Var(&exe, "executable", "<harness>=<path>")
	fs.Var(&arg, "arg", "<harness>=<argument>")
	fs.Var(&env, "env", "<harness>=<NAME>=<value>")
	fs.Var(&unset, "unset-env", "<harness>=<NAME>")
	if err := parseFlags(fs, args[1:]); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usagef("unexpected %q: the key comes first, then flags", fs.Arg(0))
	}
	if create && *name == "" {
		return usagef("fleet add needs --name")
	}

	dir, err := admiralHome()
	if err != nil {
		return err
	}
	fleets, err := config.Fleets(dir)
	if err != nil {
		return err
	}
	raw, exists := fleets.Get(key)
	switch {
	case create && exists:
		return fmt.Errorf("fleet %s already exists; use fleet edit", key)
	case !create && !exists:
		return fmt.Errorf("no fleet %q", key)
	}
	fleet := config.NewObject()
	if exists {
		if fleet, err = config.ParseObject(raw); err != nil {
			return err
		}
	}
	if *name != "" {
		fleet.SetValue("name", *name)
	}
	if *color != "" {
		fleet.SetValue("color", *color)
	}
	if len(roots) > 0 || len(rmRoots) > 0 || create {
		cur, has := fleet.Get("search_roots")
		kept := stringsOf(cur, has)
		kept = slices.DeleteFunc(kept, func(r string) bool { return slices.Contains(rmRoots, r) })
		for _, r := range roots {
			if !slices.Contains(kept, r) {
				kept = append(kept, r)
			}
		}
		if kept == nil {
			kept = []string{}
		}
		fleet.SetValue("search_roots", kept)
	}

	docks := map[string]*config.Object{}
	var order []string
	use := func(h string) (*config.Object, error) {
		if d, ok := docks[h]; ok {
			return d, nil
		}
		d, err := dockOf(fleet, h)
		if err == nil {
			docks[h] = d
			order = append(order, h)
		}
		return d, err
	}
	for _, h := range harness {
		if !isHarness(h) {
			return usagef("--harness %q: want claude, codex or copilot", h)
		}
		if _, err := use(h); err != nil {
			return err
		}
	}
	for _, v := range exe {
		h, path, err := splitPair("executable", v)
		if err != nil {
			return err
		}
		d, err := use(h)
		if err != nil {
			return err
		}
		d.SetValue("executable", path)
	}
	given := map[string][]string{}
	var givenOrder []string
	for _, v := range arg {
		h, a, err := splitPair("arg", v)
		if err != nil {
			return err
		}
		if _, ok := given[h]; !ok {
			givenOrder = append(givenOrder, h)
		}
		given[h] = append(given[h], a)
	}
	for _, h := range givenOrder {
		d, err := use(h)
		if err != nil {
			return err
		}
		d.SetValue("args", given[h])
	}
	for _, v := range env {
		h, kv, err := splitPair("env", v)
		if err != nil {
			return err
		}
		n, val, ok := strings.Cut(kv, "=")
		if !ok || n == "" {
			return usagef("--env %q: want <harness>=<NAME>=<value>", v)
		}
		d, err := use(h)
		if err != nil {
			return err
		}
		e, err := envOf(d)
		if err != nil {
			return err
		}
		e.SetValue(n, val)
		d.Set("env", e.JSON())
	}
	for _, v := range unset {
		h, n, err := splitPair("unset-env", v)
		if err != nil {
			return err
		}
		d, err := use(h)
		if err != nil {
			return err
		}
		e, err := envOf(d)
		if err != nil {
			return err
		}
		e.Delete(n)
		if e.Len() == 0 {
			d.Delete("env")
		} else {
			d.Set("env", e.JSON())
		}
	}
	for _, h := range order {
		fleet.Set(h, docks[h].JSON())
	}

	fleets.Set(key, fleet.JSON())
	p, err := newPlan(dir, fleets, []string{key})
	if err != nil {
		return err
	}
	if err := p.write(dir); err != nil {
		return err
	}
	done := "Updated"
	if create {
		done = "Added"
	}
	fmt.Fprintf(std.Out, "%s fleet %s\n", done, key)
	return nil
}
