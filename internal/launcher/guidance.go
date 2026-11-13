package launcher

import "strings"

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
