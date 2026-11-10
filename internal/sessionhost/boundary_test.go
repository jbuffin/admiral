package sessionhost

import (
	"go/ast"
	goparser "go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The boundary guard: no package outside sessionhost runs tmux or builds tmux
// arguments (admiral#256, ADR 0002). It parses Go source and never runs tmux,
// so it runs under `go test -short` in make check.
//
// Holes it leaves open, by design (each would need type information or flow
// analysis): os/exec imported under another name, an exec.Cmd{Path: ...} literal,
// and a program variable with no "tmux" in its name.

var tmuxCommands = map[string]bool{}

func init() {
	for _, c := range strings.Fields(`new-session new-window respawn-pane respawn-window
		kill-session kill-server kill-pane kill-window list-sessions list-windows list-panes
		send-keys set-option show-options set-window-option show-window-options
		set-environment show-environment set-hook capture-pane pipe-pane load-buffer
		paste-buffer set-buffer delete-buffer resize-window resize-pane attach-session
		has-session display-message refresh-client select-pane select-window wait-for
			if if-shell run-shell split-window rename-window source-file show-buffer save-buffer
			switch-client detach-client`) {
		tmuxCommands[c] = true
	}
}

var tmuxAliases = map[string]bool{
	"set": true, "setw": true, "show": true, "showw": true,
	"display": true, "attach": true, "has": true, "send": true, "kill-ses": true,
	"neww": true, "splitw": true, "lsw": true, "ls": true, "killw": true, "if": true,
}

var tmuxFlags = map[string]bool{"-t": true, "-p": true, "-g": true, "-w": true, "-u": true, "-S": true}

// execProgramArg maps pkg.Func to the index of its program argument.
var execProgramArg = map[string]int{
	"exec.Command":        0,
	"exec.CommandContext": 1,
	"exec.LookPath":       0,
	"syscall.Exec":        0,
	"os.StartProcess":     0,
}

func stringLit(e ast.Expr) (string, bool) {
	bl, ok := e.(*ast.BasicLit)
	if !ok || bl.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(bl.Value)
	return s, err == nil
}

// namesTmux reports whether a program argument is the literal "tmux" or an
// identifier or field whose name contains "tmux" in any case.
func namesTmux(e ast.Expr) bool {
	if s, ok := stringLit(e); ok {
		return s == "tmux"
	}
	switch v := e.(type) {
	case *ast.Ident:
		return strings.Contains(strings.ToLower(v.Name), "tmux")
	case *ast.SelectorExpr:
		return strings.Contains(strings.ToLower(v.Sel.Name), "tmux")
	}
	return false
}

func callName(c *ast.CallExpr) string {
	sel, ok := c.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return ""
	}
	return pkg.Name + "." + sel.Sel.Name
}

// scanFile returns one reason per source line that holds a tmux call or
// tmux arguments. Comments are not parsed, so they never count.
func scanFile(path string) (map[int]string, error) {
	fset := token.NewFileSet()
	f, err := goparser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil, err
	}
	found := map[int]string{}
	add := func(p token.Pos, reason string) {
		line := fset.Position(p).Line
		if _, dup := found[line]; !dup {
			found[line] = reason
		}
	}
	// ast.Inspect visits a call before its arguments, so the call's reason
	// wins over the literals inside it.
	ast.Inspect(f, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.CallExpr:
			if idx, ok := execProgramArg[callName(v)]; ok && idx < len(v.Args) && namesTmux(v.Args[idx]) {
				add(v.Pos(), "runs tmux via "+callName(v))
				return true
			}
			if len(v.Args) > 1 {
				if first, ok := stringLit(v.Args[0]); ok && tmuxAliases[first] {
					for _, a := range v.Args[1:] {
						if s, ok := stringLit(a); ok && tmuxFlags[s] {
							add(v.Pos(), "tmux alias "+strconv.Quote(first)+" with flag "+s)
							break
						}
					}
				}
			}
		case *ast.CompositeLit:
			if at, ok := v.Type.(*ast.ArrayType); ok && len(v.Elts) > 0 {
				if id, ok := at.Elt.(*ast.Ident); ok && id.Name == "string" && at.Len == nil {
					if first, ok := stringLit(v.Elts[0]); ok && tmuxAliases[first] {
						add(v.Pos(), "tmux alias "+strconv.Quote(first)+" opens a []string")
					}
				}
			}
		case *ast.BasicLit:
			s, ok := stringLit(v)
			if !ok {
				return true
			}
			switch {
			case strings.HasPrefix(s, "@admiral-"):
				add(v.Pos(), "tmux option name "+strconv.Quote(s))
			case strings.Contains(s, "#{"):
				add(v.Pos(), "tmux format "+strconv.Quote(s))
			default:
				w := strings.Fields(s)
				switch {
				case len(w) > 0 && tmuxCommands[w[0]]:
					add(v.Pos(), "tmux command "+strconv.Quote(w[0]))
				case len(w) > 1 && tmuxAliases[w[0]] && slices.ContainsFunc(w[1:], func(f string) bool { return tmuxFlags[f] }):
					add(v.Pos(), "tmux alias "+strconv.Quote(w[0])+" with a flag in one literal")
				}
			}
		}
		return true
	})
	return found, nil
}

// scanTmuxViolations walks the Go source under root and lists each violation
// as "path:line: reason", sorted.
func scanTmuxViolations(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel == "." {
				return nil
			}
			name := d.Name()
			switch {
			case rel == "internal/sessionhost", rel == "e2e", rel == "web",
				name == "node_modules", name == "testdata", strings.HasPrefix(name, "."):
				return filepath.SkipDir
			}
			if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
				return filepath.SkipDir // a separate module
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		lines, err := scanFile(path)
		if err != nil {
			return err
		}
		for line, reason := range lines {
			out = append(out, rel+":"+strconv.Itoa(line)+": "+reason)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan %s: %v", root, err)
	}
	sort.Strings(out)
	return out
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test directory")
		}
		dir = parent
	}
}

func TestNoTmuxOutsideSessionhost(t *testing.T) {
	violations := scanTmuxViolations(t, moduleRoot(t))
	if len(violations) > 0 {
		t.Errorf("tmux belongs behind internal/sessionhost; %d call sites run it or build its arguments:\n%s",
			len(violations), strings.Join(violations, "\n"))
	}
}

func TestTmuxGuardCatchesPlantedCall(t *testing.T) {
	got := scanTmuxViolations(t, filepath.Join("testdata", "guard"))
	var files []string
	for _, v := range got {
		files = append(files, v[:strings.Index(v, ": ")])
	}
	wantLines := []string{
		"cmd/app/planted.go:7", "cmd/app/planted.go:8", "cmd/app/planted.go:9",
		"cmd/app/planted.go:10", "cmd/app/planted.go:11", "cmd/app/planted.go:12",
		"cmd/app/planted.go:16", "cmd/app/planted.go:17", "cmd/app/planted.go:18",
	}
	sort.Strings(wantLines) // lines 10-18 sort before 7-9 as strings
	if strings.Join(files, "\n") != strings.Join(wantLines, "\n") {
		t.Errorf("planted violations:\n got  %v\n want %v\nfull report:\n%s",
			files, wantLines, strings.Join(got, "\n"))
	}
}
