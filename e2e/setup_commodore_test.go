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

func TestU3_SetupOffersCommodore(t *testing.T) {
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
		runs := w.pluginRuns()
		if len(runs) != 2 || runs[0] != "plugin marketplace add jbuffin/skills" || runs[1] != "plugin install jasonbuffin@jbuffinskills" {
			t.Errorf("plugin runs = %q, want the marketplace add and the install", runs)
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
		if runs := w.pluginRuns(); len(runs) != 1 || runs[0] != "plugin update jasonbuffin@jbuffinskills" {
			t.Errorf("plugin runs = %q, want one plugin update", runs)
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

func TestU3_SetupAnswersInstallCommodore(t *testing.T) {
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
		var claude, codex bool
		for _, l := range w.pluginRuns() {
			claude = claude || l == "plugin install jasonbuffin@jbuffinskills"
			codex = codex || l == "plugin add jasonbuffin@jbuffinskills"
		}
		if !claude || !codex {
			t.Errorf("plugin runs = %q, want the claude install and the codex add", w.pluginRuns())
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
		if runs := w.pluginRuns(); len(runs) != 1 || runs[0] != "plugin update jasonbuffin@jbuffinskills" {
			t.Errorf("plugin runs = %q, want one plugin update", runs)
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
		for _, v := range []string{`"yes"`, `1`, `"true"`, `null`} {
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
				t.Errorf("install_commodore %s: the error should say a boolean is expected (today it is refused as an unknown key), got:\n%s", v, r.both())
			}
			w.requireNoConfig()
			w.requireNoPluginRuns(r)
		}
	})
}

func TestU3_SetupCommodoreFailure(t *testing.T) {
	t.Parallel()

	for _, c := range []struct{ cue, want string }{
		{"fail", "boom"},
		{"not-logged-in", "not logged in"},
	} {
		t.Run("a "+c.cue+" run prints the output and the commands and setup still exits 0, then goes on", func(t *testing.T) {
			t.Parallel()
			w := newWorld(t)
			w.saveFleets(w.fleetObj("work", "Work", "", "claude", "codex"))
			file := w.answers(t, `"work":{"install_commodore":true}`)

			r := w.admiralWith(cliOpts{env: w.cenv("FAKE_PLUGIN=" + c.cue)}, "setup", "--answers", file)
			w.expectExit(r, 0, "setup with a failing install")
			if !strings.Contains(r.stdout, c.want) {
				t.Errorf("setup should print the installer's output (%q):\n%s", c.want, r.stdout)
			}
			if c.cue == "fail" && !strings.Contains(r.stdout, "cloning jbuffin/skills") {
				t.Errorf("setup should print the last lines of the output, not only the error:\n%s", r.stdout)
			}
			lines := strings.Split(r.stdout, "\n")
			for _, want := range []string{
				"claude plugin marketplace add jbuffin/skills", "claude plugin install jasonbuffin@jbuffinskills",
				"codex plugin marketplace add jbuffin/skills", "codex plugin add jasonbuffin@jbuffinskills",
			} {
				found := false
				for _, l := range lines {
					found = found || strings.TrimSpace(l) == want
				}
				if !found {
					t.Errorf("setup should print the manual command %q on a line of its own:\n%s", want, r.stdout)
				}
			}
			if runs := w.pluginRuns(); len(runs) != 2 {
				t.Errorf("plugin runs = %q: a failure on claude must not stop setup trying codex", runs)
			}
		})
	}

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
