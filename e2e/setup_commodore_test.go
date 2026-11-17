package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Setup offers to install or update Commodore for every harness of every saved fleet.

var identityVarOf = map[string]string{"claude": "CLAUDE_CONFIG_DIR", "codex": "CODEX_HOME", "copilot": "COPILOT_HOME"}

// cenv is the process environment of a setup run: the fake harness records plugin runs in w.rec.
func (w *world) cenv(extra ...string) []string {
	return append([]string{"FAKE_RECORD_DIR=" + w.rec}, extra...)
}

func (w *world) identity(harness, name string) string {
	return filepath.Join(w.userHom, "."+harness+"-"+name)
}

// fleetObj is one fleet object whose harnesses each use their own identity directory.
func (w *world) fleetObj(key, name, extra string, harnesses ...string) string {
	var docks []string
	for _, h := range harnesses {
		docks = append(docks, `"`+h+`":{"env":{"`+identityVarOf[h]+`":`+quote(w.identity(h, key))+`}}`)
	}
	if extra != "" {
		extra += ","
	}
	return `"` + key + `":{"name":"` + name + `",` + extra + `"search_roots":["~/code"],` + strings.Join(docks, ",") + `}`
}

// saveFleets writes config.json holding these fleets, as an earlier setup left it.
func (w *world) saveFleets(fleets ...string) {
	w.t.Helper()
	if err := os.MkdirAll(filepath.Join(w.userHom, "code"), 0o755); err != nil {
		w.t.Fatal(err)
	}
	w.writeRaw(configPath(w.home), `{"version":1,"fleets":{`+strings.Join(fleets, ",")+`}}`+"\n", 0o600)
}

// seedPlugin installs Commodore as a plugin at version through the fake harness, recording elsewhere than w.rec.
func (w *world) seedPlugin(harness, identity, version string) {
	w.t.Helper()
	cmd := exec.Command(w.fakeExe2(harness), "plugin", "install", "jasonbuffin@jbuffinskills")
	cmd.Env = append(os.Environ(), "HOME="+w.userHom, identityVarOf[harness]+"="+identity,
		"FAKE_RECORD_DIR="+filepath.Join(w.root, "seed-record"), "FAKE_PLUGIN_VERSION="+version)
	if out, err := cmd.CombinedOutput(); err != nil {
		w.t.Fatalf("seeding Commodore %s: %v\n%s", version, err, out)
	}
}

func (w *world) seedLoose(identity string) string {
	w.t.Helper()
	d := filepath.Join(identity, "skills", "commodore")
	w.writeRaw(filepath.Join(d, "SKILL.md"), "---\nname: commodore\ndescription: a loose copy\n---\n", 0o644)
	return d
}

// pluginRuns are the arguments of every plugin command the fake harness ran, in order; none when it ran none.
func (w *world) pluginRuns() []string {
	b, err := os.ReadFile(filepath.Join(w.rec, "plugin-runs"))
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimRight(string(b), "\n"), "\n")
}

func (w *world) requireNoPluginRuns(r cliResult) {
	w.t.Helper()
	if runs := w.pluginRuns(); len(runs) != 0 {
		w.t.Fatalf("no installer should have run, but the harness ran %q\nstdout: %s", runs, r.stdout)
	}
}

func (w *world) hasPlugin(harness, name string) bool {
	_, err := os.Stat(filepath.Join(w.identity(harness, name), "plugins", "installed_plugins.json"))
	return err == nil
}

// idle is what a run of setup with no fleet to change takes from the keyboard: no fleet key, no Tailscale.
const idle = "\n\n"

const (
	installClaudeWork = "Install Commodore for claude in fleet Work?"
	updateClaudeWork  = "Update Commodore for claude in fleet Work?"
	askedSomething    = "Commodore for "
)

func TestSetupOffersCommodore(t *testing.T) {
	t.Parallel()

	t.Run("a yes installs where Commodore is missing and a no runs nothing, asking again next time", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.saveFleets(w.fleetObj("work", "Work", "", "claude"))

		r := w.admiralWith(cliOpts{env: w.cenv(), stdin: idle + "n\n"}, "setup")
		w.expectExit(r, 0, "setup, answering no")
		if !strings.Contains(r.stdout, installClaudeWork) {
			t.Fatalf("setup should ask %q:\n%s", installClaudeWork, r.stdout)
		}
		w.requireNoPluginRuns(r)
		if w.hasPlugin("claude", "work") {
			t.Fatal("a no installed Commodore anyway")
		}

		r = w.admiralWith(cliOpts{env: w.cenv(), stdin: idle + "y\n"}, "setup")
		w.expectExit(r, 0, "setup, answering yes")
		if !strings.Contains(r.stdout, installClaudeWork) {
			t.Fatalf("a no is not remembered: setup should ask again:\n%s", r.stdout)
		}
		if !strings.Contains(r.stdout, "Installing Commodore for claude in fleet Work...") {
			t.Errorf("setup should say what it is running:\n%s", r.stdout)
		}
		if len(w.pluginRuns()) == 0 {
			t.Error("a yes ran no installer")
		}
		if !w.hasPlugin("claude", "work") {
			t.Error("the plugin is not in the fleet's identity directory")
		}
		after := r.stdout[strings.Index(r.stdout, "Installing Commodore"):]
		if !strings.Contains(after, "Commodore 3.5.0") {
			t.Errorf("after the install setup should print the new state, Commodore 3.5.0:\n%s", after)
		}
	})

	t.Run("an outdated Commodore says so and offers Update", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.saveFleets(w.fleetObj("work", "Work", "", "claude"))
		w.seedPlugin("claude", w.identity("claude", "work"), "3.1.0")

		r := w.admiralWith(cliOpts{env: w.cenv(), stdin: idle + "n\n"}, "setup")
		w.expectExit(r, 0, "setup, answering no to the update")
		if !strings.Contains(r.stdout, "Commodore 3.1.0 is older than this Admiral needs (3.5.0)") {
			t.Errorf("setup should print the outdated line:\n%s", r.stdout)
		}
		if !strings.Contains(r.stdout, updateClaudeWork) {
			t.Fatalf("setup should ask %q:\n%s", updateClaudeWork, r.stdout)
		}
		w.requireNoPluginRuns(r)

		r = w.admiralWith(cliOpts{env: w.cenv("FAKE_PLUGIN_VERSION=3.6.0"), stdin: idle + "y\n"}, "setup")
		w.expectExit(r, 0, "setup, answering yes to the update")
		if runs := w.pluginRuns(); len(runs) != 1 || !strings.Contains(runs[0], "update") {
			t.Errorf("plugin runs = %q, want one update and no install", runs)
		}
		if !strings.Contains(r.stdout, "Updating Commodore for claude in fleet Work...") || !strings.Contains(r.stdout[strings.Index(r.stdout, "Updating Commodore"):], "Commodore 3.6.0") {
			t.Errorf("setup should say it is updating and then print the new version, 3.6.0:\n%s", r.stdout)
		}
	})

	t.Run("a loose copy prints the note, offers nothing and deletes nothing", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.saveFleets(w.fleetObj("work", "Work", "", "claude"))
		loose := w.seedLoose(w.identity("claude", "work"))

		// No answer is given: a question here would hit the end of input and fail setup.
		r := w.admiralWith(cliOpts{env: w.cenv(), stdin: idle}, "setup")
		w.expectExit(r, 0, "setup with a loose Commodore")
		if !strings.Contains(r.stdout, "Commodore version unknown (installed as loose skills)") {
			t.Errorf("setup should print the loose-skills line:\n%s", r.stdout)
		}
		if strings.Contains(r.stdout, askedSomething) {
			t.Errorf("a loose copy gets no offer:\n%s", r.stdout)
		}
		w.requireNoPluginRuns(r)
		if _, err := os.Stat(filepath.Join(loose, "SKILL.md")); err != nil {
			t.Errorf("setup deleted the loose skill: %v", err)
		}

		// install_commodore leaves it alone as well.
		file := w.answers(t, `"work":{"install_commodore":true}`)
		r = w.admiralWith(cliOpts{env: w.cenv()}, "setup", "--answers", file, "--yes")
		w.expectExit(r, 0, "setup --answers with install_commodore on a loose copy")
		w.requireNoPluginRuns(r)
	})

	t.Run("a current Commodore gets no offer", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.saveFleets(w.fleetObj("work", "Work", "", "claude"))
		w.seedPlugin("claude", w.identity("claude", "work"), "3.5.0")

		r := w.admiralWith(cliOpts{env: w.cenv(), stdin: idle}, "setup")
		w.expectExit(r, 0, "setup with a current Commodore")
		if strings.Contains(r.stdout, "Install Commodore") || strings.Contains(r.stdout, "Update Commodore") {
			t.Errorf("an ok Commodore gets no offer:\n%s", r.stdout)
		}
		w.requireNoPluginRuns(r)
	})

	t.Run("every harness of every fleet is offered in turn, each by its own answer", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.saveFleets(w.fleetObj("work", "Work", "", "claude", "codex"))

		r := w.admiralWith(cliOpts{env: w.cenv(), stdin: idle + "y\nn\n"}, "setup")
		w.expectExit(r, 0, "setup, yes for claude and no for codex")
		iClaude := strings.Index(r.stdout, installClaudeWork)
		iCodex := strings.Index(r.stdout, "Install Commodore for codex in fleet Work?")
		if iClaude < 0 || iCodex < iClaude {
			t.Fatalf("setup should ask for claude and then codex:\n%s", r.stdout)
		}
		if !w.hasPlugin("claude", "work") {
			t.Error("claude's yes did not install")
		}
		for _, l := range w.pluginRuns() {
			if strings.Contains(l, "plugin add") {
				t.Errorf("codex's no ran %q", l)
			}
		}
	})

	t.Run("--dry-run offers nothing and runs nothing", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.saveFleets(w.fleetObj("work", "Work", "", "claude"))

		r := w.admiralWith(cliOpts{env: w.cenv(), stdin: idle}, "setup", "--dry-run")
		w.expectExit(r, 0, "interactive --dry-run")
		if strings.Contains(r.stdout, "Install Commodore") {
			t.Errorf("--dry-run must not offer:\n%s", r.stdout)
		}
		w.requireNoPluginRuns(r)

		file := w.answers(t, w.fleetObj("fresh", "Fresh", `"install_commodore":true`, "claude"))
		r = w.admiralWith(cliOpts{env: w.cenv()}, "setup", "--answers", file, "--dry-run")
		w.expectExit(r, 0, "--answers --dry-run with install_commodore")
		w.requireNoPluginRuns(r)
	})

	t.Run("a declined write offers nothing and runs nothing", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		if err := os.MkdirAll(filepath.Join(w.userHom, "code"), 0o755); err != nil {
			t.Fatal(err)
		}
		file := w.answers(t, w.fleetObj("fresh", "Fresh", `"install_commodore":true`, "claude"))

		r := w.admiralWith(cliOpts{env: w.cenv(), stdin: "n\n"}, "setup", "--answers", file)
		w.expectExit(r, 0, "setup --answers, declining the write")
		w.requireNoConfig()
		w.requireNoPluginRuns(r)
		if strings.Contains(r.stdout, "Install Commodore") || strings.Contains(r.stdout, "Installing Commodore") {
			t.Errorf("a declined write must not lead to an offer:\n%s", r.stdout)
		}
	})
}

func TestSetupAnswersInstallCommodore(t *testing.T) {
	t.Parallel()

	t.Run("true installs for every harness of the fleet and never reaches config.json", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		if err := os.MkdirAll(filepath.Join(w.userHom, "code"), 0o755); err != nil {
			t.Fatal(err)
		}
		file := w.answers(t, w.fleetObj("work", "Work", `"install_commodore":true`, "claude", "codex"))

		r := w.admiralWith(cliOpts{env: w.cenv()}, "setup", "--answers", file, "--yes")
		w.expectExit(r, 0, "setup --answers --yes with install_commodore true")
		if !w.hasPlugin("claude", "work") {
			t.Error("claude has no Commodore")
		}
		var codex bool
		for _, l := range w.pluginRuns() {
			codex = codex || strings.Contains(l, "plugin add")
		}
		if !codex {
			t.Errorf("plugin runs = %q, want codex installed too", w.pluginRuns())
		}
		if strings.Contains(string(w.configBytes()), "install_commodore") {
			t.Errorf("install_commodore reached config.json:\n%s", w.configBytes())
		}
		if strings.Contains(r.stdout, "install_commodore") {
			t.Errorf("install_commodore is an answer and must not show in the diff:\n%s", r.stdout)
		}
		if strings.Contains(r.stdout, "[y/N]") {
			t.Errorf("--answers never asks the install question:\n%s", r.stdout)
		}
	})

	t.Run("after a confirmed write the install still follows", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		if err := os.MkdirAll(filepath.Join(w.userHom, "code"), 0o755); err != nil {
			t.Fatal(err)
		}
		file := w.answers(t, w.fleetObj("work", "Work", `"install_commodore":true`, "claude"))

		// Only the write is answered; the install question is never asked.
		r := w.admiralWith(cliOpts{env: w.cenv(), stdin: "y\n"}, "setup", "--answers", file)
		w.expectExit(r, 0, "setup --answers, confirming the write")
		if !w.hasPlugin("claude", "work") {
			t.Errorf("Commodore was not installed after the write:\n%s", r.stdout)
		}
		if strings.Contains(r.stdout, installClaudeWork) {
			t.Errorf("--answers never asks the install question:\n%s", r.stdout)
		}
	})

	t.Run("false, absent and a fleet the file doesn't name run no installer and ask nothing", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.saveFleets(w.fleetObj("off", "Off", "", "claude"), w.fleetObj("unnamed", "Unnamed", "", "claude"))
		file := w.answers(t,
			w.fleetObj("off", "Off", `"install_commodore":false`, "claude"),
			w.fleetObj("fresh", "Fresh", "", "claude"))

		// Only the write of the new fleet is answered: an install question would hit the end of input and fail setup.
		r := w.admiralWith(cliOpts{env: w.cenv(), stdin: "y\n"}, "setup", "--answers", file)
		w.expectExit(r, 0, "setup --answers without --yes, install_commodore false or absent")
		w.requireNoPluginRuns(r)
		if strings.Contains(r.stdout, "Install Commodore") {
			t.Errorf("setup asked an install question:\n%s", r.stdout)
		}
	})

	t.Run("a fleet the file doesn't name gets no install while a named one does", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.saveFleets(w.fleetObj("work", "Work", "", "claude"), w.fleetObj("idle", "Idle", "", "claude"))
		before := string(w.configBytes())
		file := w.answers(t, `"work":{"install_commodore":true}`)

		r := w.admiralWith(cliOpts{env: w.cenv()}, "setup", "--answers", file)
		w.expectExit(r, 0, "setup --answers naming only install_commodore")
		if !strings.Contains(r.stdout, "No changes.") {
			t.Errorf("an answer holding only install_commodore changes nothing:\n%s", r.stdout)
		}
		if !w.hasPlugin("claude", "work") {
			t.Errorf("the named fleet got no Commodore:\n%s", r.stdout)
		}
		if w.hasPlugin("claude", "idle") {
			t.Error("a fleet the answers file doesn't name got Commodore")
		}
		if got := string(w.configBytes()); got != before {
			t.Errorf("config.json changed to:\n%s", got)
		}
	})

	t.Run("an outdated Commodore is updated", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.saveFleets(w.fleetObj("work", "Work", "", "claude"))
		w.seedPlugin("claude", w.identity("claude", "work"), "3.1.0")
		file := w.answers(t, `"work":{"install_commodore":true}`)

		r := w.admiralWith(cliOpts{env: w.cenv()}, "setup", "--answers", file)
		w.expectExit(r, 0, "setup --answers on an outdated Commodore")
		if runs := w.pluginRuns(); len(runs) != 1 || !strings.Contains(runs[0], "update") {
			t.Errorf("plugin runs = %q, want one update and no install", runs)
		}
	})

	t.Run("a fleet that is not in config.json and has only install_commodore is not created", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		file := w.answers(t, `"ghost":{"install_commodore":true}`)

		r := w.admiralWith(cliOpts{env: w.cenv()}, "setup", "--answers", file, "--yes")
		w.expectExit(r, 0, "setup --answers naming an unknown fleet with only install_commodore")
		w.requireNoPluginRuns(r)
		if _, err := os.Stat(configPath(w.home)); err == nil && strings.Contains(string(w.configBytes()), "ghost") {
			t.Errorf("the fleet was created:\n%s", w.configBytes())
		}
	})

	t.Run("a value that is not a boolean is refused with its path and nothing is written", func(t *testing.T) {
		t.Parallel()
		for _, v := range []string{`"yes"`, `null`} {
			w := newWorld(t)
			if err := os.MkdirAll(filepath.Join(w.userHom, "code"), 0o755); err != nil {
				t.Fatal(err)
			}
			file := w.answers(t, w.fleetObj("work", "Work", `"install_commodore":`+v, "claude"))

			r := w.admiralWith(cliOpts{env: w.cenv()}, "setup", "--answers", file, "--yes")
			w.expectExit(r, 1, "install_commodore: "+v)
			if !strings.Contains(r.both(), "fleets.work.install_commodore") {
				t.Errorf("install_commodore %s: the error should name fleets.work.install_commodore, got:\n%s", v, r.both())
			}
			if !strings.Contains(r.both(), "boolean") {
				t.Errorf("install_commodore %s: the error should say a boolean is expected, got:\n%s", v, r.both())
			}
			w.requireNoConfig()
			w.requireNoPluginRuns(r)
		}
	})
}

func TestSetupCommodoreFailure(t *testing.T) {
	t.Parallel()

	t.Run("a failing install prints the output and the commands and setup still exits 0, then goes on", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.saveFleets(w.fleetObj("work", "Work", "", "claude", "codex"))
		file := w.answers(t, `"work":{"install_commodore":true}`)

		r := w.admiralWith(cliOpts{env: w.cenv("FAKE_PLUGIN=fail")}, "setup", "--answers", file)
		w.expectExit(r, 0, "setup with a failing install")
		for _, want := range []string{"Commodore was not installed", "boom", "cloning jbuffin/skills"} {
			if !strings.Contains(r.stdout, want) {
				t.Errorf("setup should print %q:\n%s", want, r.stdout)
			}
		}
		for _, want := range []string{"claude plugin install jasonbuffin@jbuffinskills", "codex plugin add jasonbuffin@jbuffinskills"} {
			found := false
			for _, l := range strings.Split(r.stdout, "\n") {
				found = found || strings.TrimSpace(l) == want
			}
			if !found {
				t.Errorf("setup should print the manual command %q on a line of its own:\n%s", want, r.stdout)
			}
		}
		if !strings.Contains(r.stdout, "Installing Commodore for codex in fleet Work...") {
			t.Errorf("a failure on claude must not stop setup trying codex:\n%s", r.stdout)
		}
	})

	t.Run("a failing update says it was not updated and prints the output and the update command", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.saveFleets(w.fleetObj("work", "Work", "", "claude"))
		w.seedPlugin("claude", w.identity("claude", "work"), "3.1.0")
		file := w.answers(t, `"work":{"install_commodore":true}`)

		r := w.admiralWith(cliOpts{env: w.cenv("FAKE_PLUGIN=fail")}, "setup", "--answers", file)
		w.expectExit(r, 0, "setup with a failing update")
		if !strings.Contains(r.stdout, "Commodore was not updated") || strings.Contains(r.stdout, "updateed") {
			t.Errorf("the failure line should read \"Commodore was not updated\":\n%s", r.stdout)
		}
		if !strings.Contains(r.stdout, "boom") {
			t.Errorf("setup should print the output:\n%s", r.stdout)
		}
		found := false
		for _, l := range strings.Split(r.stdout, "\n") {
			found = found || strings.TrimSpace(l) == "claude plugin update jasonbuffin@jbuffinskills"
		}
		if !found {
			t.Errorf("setup should print the update command on a line of its own:\n%s", r.stdout)
		}
	})

	t.Run("a harness that is not installed prints the failure and the commands", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.writeRaw(filepath.Join(w.userHom, "code", ".keep"), "", 0o644)
		w.writeRaw(configPath(w.home), `{"version":1,"fleets":{"work":{"name":"Work","search_roots":["~/code"],"claude":{"executable":"/no/such/claude","env":{"CLAUDE_CONFIG_DIR":`+quote(w.identity("claude", "work"))+`}}}}}`+"\n", 0o600)
		file := w.answers(t, `"work":{"install_commodore":true}`)

		r := w.admiralWith(cliOpts{env: w.cenv()}, "setup", "--answers", file)
		w.expectExit(r, 0, "setup with a missing harness binary")
		if !strings.Contains(r.stdout, "not installed") {
			t.Errorf("setup should say the harness is not installed:\n%s", r.stdout)
		}
		if !strings.Contains(r.stdout, "claude plugin install jasonbuffin@jbuffinskills") {
			t.Errorf("setup should print the manual commands:\n%s", r.stdout)
		}
		w.requireNoPluginRuns(r)
	})
}
