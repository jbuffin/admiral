package launcher

import (
	"path/filepath"
	"strings"
)

// withGuidance puts the fleet's Guidance after the prompt of a ship an outside event started; a read error launches without it.
func (l *Launcher) withGuidance(req Request, prompt string) string {
	if !req.WorkerLaunched || l.Guidance == nil {
		return prompt
	}
	text, err := l.Guidance(req.Fleet)
	if err != nil {
		l.warn([]string{"the fleet's Guidance was left out of the prompt: " + err.Error()})
		return prompt
	}
	if strings.TrimSpace(text) == "" {
		return prompt
	}
	section := "## Standing orders\n\n" + text
	if prompt == "" {
		return section
	}
	return prompt + "\n\n" + section
}

func scoutOption(scout bool) string {
	if scout {
		return "1"
	}
	return ""
}

const scoutContract = "## Scout\n\n" +
	"A scout changes nothing: your guard refuses commits, pushes, pull request changes and edits in your checkout, " +
	"so read, run and look things up, and write what you found to your report. " +
	"Anything you cannot settle goes in your report as an open question; do not ask the engineer."

// withScoutContract puts a scout's contract after its prompt.
func withScoutContract(req Request, prompt string) string {
	if !req.Scout || req.Prompt != "" || req.Bare {
		return prompt
	}
	if prompt == "" {
		return scoutContract
	}
	return prompt + "\n\n" + scoutContract
}

// resolveScoutDir is the checkout path a scout's ADMIRAL_SCOUT names: absolute, with symlinks resolved.
func resolveScoutDir(dir string) (string, error) {
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	return filepath.Abs(resolved)
}
