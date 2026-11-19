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
	"so read, run and look things up. " +
	"Write what you found, in markdown, to the file named by $ADMIRAL_FINDINGS. " +
	"Run `echo $ADMIRAL_FINDINGS` to read the path, then write the file with your file-writing tool at that absolute path; a shell write to $ADMIRAL_FINDINGS is refused. " +
	"A scout that leaves no findings ends failed. " +
	"Anything you cannot settle goes in your findings as an open question; do not ask the engineer."

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

const closingSteps = "## Closing steps\n\n" +
	"When the work is done: commit it on your branch, push the branch, and open a pull request. " +
	"Then write your Admiral report to the file named by $ADMIRAL_REPORT as JSON with version 2, a result of done, partial or failed, " +
	"and prs listing the pull request you opened as [{\"url\": ...}], each url the full URL (https://host/owner/repo/pull/N). " +
	"If you found something worth reading later, write it in markdown to the file named by $ADMIRAL_FINDINGS."

const localClosingSteps = "## Closing steps\n\n" +
	"When the work is done: commit it on your branch. This checkout has no GitHub remote, so do not push and do not open a pull request. " +
	"Then write your Admiral report to the file named by $ADMIRAL_REPORT as JSON with version 2, a result of done, partial or failed, and prs: [] because there is no pull request. " +
	"If you found something worth reading later, write it in markdown to the file named by $ADMIRAL_FINDINGS."

// withClosingSteps puts Admiral's closing steps after a fresh quick ship's launch text: a chat launch, no skill, not a scout, not a wake.
func withClosingSteps(req Request, prompt string, localOnly bool) string {
	if !req.OnInstruction || req.Skill.Kind != "none" || req.Scout || req.Prompt != "" || req.Bare {
		return prompt
	}
	steps := closingSteps
	if localOnly {
		steps = localClosingSteps
	}
	if prompt == "" {
		return steps
	}
	return prompt + "\n\n" + steps
}

// resolveScoutDir is the checkout path a scout's ADMIRAL_SCOUT names: absolute, with symlinks resolved.
func resolveScoutDir(dir string) (string, error) {
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	return filepath.Abs(resolved)
}
