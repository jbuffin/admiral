package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/jbuffin/admiral/internal/config"
)

// Config is `admiral config show | validate | edit` (story 23).
func Config(args []string, std IO) error {
	if len(args) != 1 {
		return usagef("usage: admiral config show | validate | edit")
	}
	dir, err := admiralHome()
	if err != nil {
		return err
	}
	switch args[0] {
	case "show":
		data, err := os.ReadFile(filepath.Join(dir, config.FileName))
		if err != nil {
			return err
		}
		_, err = std.Out.Write(data)
		return err
	case "validate":
		data, err := readFile(dir)
		if err != nil {
			return err
		}
		if _, err := config.Parse(data); err != nil {
			return fmt.Errorf("%s: %w", filepath.Join(dir, config.FileName), err)
		}
		return nil
	case "edit":
		return configEdit(dir, std)
	}
	return usagef("unknown command %q: want show, validate or edit", "config "+args[0])
}

// readFile is config.json, or the empty config when there is no file yet.
func readFile(dir string) ([]byte, error) { return config.ReadConfig(dir) }

// configEdit opens a copy of config.json in $EDITOR (vi without one) and saves
// the result only when it loads, so it also works on a file that is invalid
// now. An edit that doesn't load is kept in the temporary file and named.
func configEdit(dir string, std IO) error {
	data, err := readFile(dir)
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "admiral-config-")
	if err != nil {
		return err
	}
	path := filepath.Join(tmp, config.FileName)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	cmd := exec.Command("sh", "-c", editor+` "$1"`, "sh", path)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = std.In, std.Out, std.Err
	if f, ok := std.In.(*os.File); ok {
		cmd.Stdin = f
	}
	if err := cmd.Run(); err != nil {
		_ = os.RemoveAll(tmp)
		return fmt.Errorf("the editor failed: %w", err)
	}
	edited, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if string(edited) == string(data) {
		_ = os.RemoveAll(tmp)
		fmt.Fprintln(std.Out, "No changes.")
		return nil
	}
	if err := config.WriteFile(dir, edited); err != nil {
		return fmt.Errorf("%w\nconfig.json was not changed; your edit is in %s", err, path)
	}
	_ = os.RemoveAll(tmp)
	fmt.Fprintln(std.Out, "Saved config.json.")
	return nil
}
