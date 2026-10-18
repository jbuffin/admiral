package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jbuffin/admiral/internal/auth"
	"github.com/jbuffin/admiral/internal/server"
)

// ServiceLabel names the per-user LaunchAgent (ADR 0005).
const ServiceLabel = "dev.jasonbuffin.admiral"

// fallbackPath is what ships inherit when the login shell's PATH can't be read;
// launchd's own is only /usr/bin:/bin:/usr/sbin:/sbin (ADR 0010).
const fallbackPath = "/opt/homebrew/bin:/opt/homebrew/sbin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"

// Service is `admiral service install | uninstall | status` (story 3). It runs
// launchctl from PATH, so tests can put a fake first.
func Service(args []string, std IO) error {
	if len(args) != 1 {
		return usagef("usage: admiral service install | uninstall | status")
	}
	switch args[0] {
	case "install":
		return serviceInstall(std)
	case "uninstall":
		return serviceUninstall(std)
	case "status":
		return serviceStatus(std)
	}
	return usagef("usage: admiral service install | uninstall | status")
}

func servicePlistPath() (string, error) {
	user, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory: %w", err)
	}
	return filepath.Join(user, "Library", "LaunchAgents", ServiceLabel+".plist"), nil
}

func serviceDomain() string { return "gui/" + strconv.Itoa(os.Getuid()) }

func launchctl(args ...string) (string, error) {
	var errb bytes.Buffer
	cmd := exec.Command("launchctl", args...)
	cmd.Stderr = &errb
	err := cmd.Run()
	return strings.TrimSpace(errb.String()), err
}

func serviceInstall(std IO) error {
	dir, err := admiralHome()
	if err != nil {
		return err
	}
	logDir := filepath.Join(dir, "logs")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return err
	}
	prog, err := stableProgramPath()
	if err != nil {
		return err
	}
	plist, err := servicePlistPath()
	if err != nil {
		return err
	}
	if err := writePlist(plist, plistSpec{
		Program: prog,
		Log:     filepath.Join(logDir, "admiral.log"),
		Path:    loginShellPath(),
		Home:    os.Getenv("ADMIRAL_HOME"),
	}); err != nil {
		return err
	}
	// A job that isn't loaded makes bootout fail; that's the normal first install.
	_, _ = launchctl("bootout", serviceDomain()+"/"+ServiceLabel)
	msg, err := launchctl("bootstrap", serviceDomain(), plist)
	if err != nil {
		// A job that was running may not have finished unloading yet.
		time.Sleep(time.Second)
		msg, err = launchctl("bootstrap", serviceDomain(), plist)
	}
	if err != nil {
		return fmt.Errorf("launchctl bootstrap: %v: %s", err, msg)
	}
	fmt.Fprintf(std.Out, "Admiral will start at login and restart if it stops. Logs: %s\n", filepath.Join(logDir, "admiral.log"))
	return printTokenURL(std, dir)
}

func serviceUninstall(std IO) error {
	plist, err := servicePlistPath()
	if err != nil {
		return err
	}
	_, _ = launchctl("bootout", serviceDomain()+"/"+ServiceLabel)
	if err := os.Remove(plist); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	fmt.Fprintln(std.Out, "Admiral will no longer start at login.")
	return nil
}

// serviceStatus reads launchctl print's exit code only: its output isn't API.
func serviceStatus(std IO) error {
	plist, err := servicePlistPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(plist); err != nil {
		fmt.Fprintln(std.Out, "Service: not installed")
		return nil
	}
	if _, err := launchctl("print", serviceDomain()+"/"+ServiceLabel); err != nil {
		fmt.Fprintln(std.Out, "Service: installed, not loaded")
	} else {
		fmt.Fprintln(std.Out, "Service: installed, loaded")
	}
	dir, err := admiralHome()
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(dir, "token")); err != nil {
		fmt.Fprintln(std.Out, "No token yet; Admiral creates it the first time it starts.")
		return nil
	}
	return printTokenURL(std, dir)
}

func printTokenURL(std IO, dir string) error {
	token, err := auth.LoadOrCreateToken(dir)
	if err != nil {
		return err
	}
	if ip, ok := server.LANIPv4(); ok {
		fmt.Fprintf(std.Out, "On other devices: http://%s/?token=%s\n", net.JoinHostPort(ip, DefaultPort), token)
	} else {
		fmt.Fprintf(std.Out, "On other devices: no network address found; this Mac is reachable only at http://127.0.0.1:%s/\n", DefaultPort)
	}
	return nil
}

// stableProgramPath is the path the plist runs. A Homebrew Cellar path is
// deleted by brew cleanup, so a Homebrew install gets the prefix's bin/admiral.
func stableProgramPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	exe, err = filepath.Abs(exe)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil || !strings.Contains(resolved, "/Cellar/") {
		return exe, nil
	}
	for _, args := range [][]string{{"--prefix"}, {"--prefix", "admiral"}} {
		out, err := exec.Command("brew", args...).Output()
		if err != nil {
			continue
		}
		cand := filepath.Join(strings.TrimSpace(string(out)), "bin", "admiral")
		if r, err := filepath.EvalSymlinks(cand); err == nil && r == resolved {
			return cand, nil
		}
	}
	// brew isn't on PATH: the usual prefix, if it points at this install.
	const fixed = "/opt/homebrew/opt/admiral/bin/admiral"
	if r, err := filepath.EvalSymlinks(fixed); err == nil && r == resolved {
		return fixed, nil
	}
	return exe, nil
}

// loginShellPath is the PATH of the engineer's login shell, which ships
// inherit; launchd's own PATH finds almost nothing.
func loginShellPath() string {
	shell := os.Getenv("SHELL")
	if shell == "" {
		return fallbackPath
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, shell, "-lc", `printf %s "$PATH"`).Output()
	if p := strings.TrimSpace(string(out)); err == nil && p != "" {
		return p
	}
	return fallbackPath
}

type plistSpec struct {
	Program string
	Log     string
	Path    string
	Home    string // ADMIRAL_HOME as set at install; empty when unset
}

// writePlist writes the LaunchAgent. Every path is absolute: launchd expands
// neither ~ nor variables.
func writePlist(path string, s plistSpec) error {
	x := html.EscapeString
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + ServiceLabel + `</string>
	<key>ProgramArguments</key>
	<array>
		<string>` + x(s.Program) + `</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key>
		<false/>
		<key>Crashed</key>
		<true/>
	</dict>
	<key>ProcessType</key>
	<string>Interactive</string>
	<key>StandardOutPath</key>
	<string>` + x(s.Log) + `</string>
	<key>StandardErrorPath</key>
	<string>` + x(s.Log) + `</string>
	<key>EnvironmentVariables</key>
	<dict>
`)
	if s.Home != "" {
		abs, err := filepath.Abs(s.Home)
		if err != nil {
			return err
		}
		b.WriteString("\t\t<key>ADMIRAL_HOME</key>\n\t\t<string>" + x(abs) + "</string>\n")
	}
	b.WriteString("\t\t<key>PATH</key>\n\t\t<string>" + x(s.Path) + "</string>\n\t</dict>\n</dict>\n</plist>\n")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}
