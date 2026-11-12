package sessionhost

import (
	"context"
	"slices"
	"strings"
)

// RolloutsOption lists, space-separated, every Codex rollout a pane's hook has seen: /new and a resume each
// start a new one, and each keeps its own token total.
const RolloutsOption = Prefix + "rollouts"

// OptClosedCrew is a session option holding the boats closed before their ship exited, as `name:harness:path`
// tokens, so the ship's exit can still count what they used.
const OptClosedCrew = "closed-crew"

const (
	MaxRollouts   = 32
	MaxClosedCrew = 64
)

// RolloutPathOK is a path that fits one space-separated list item.
func RolloutPathOK(p string) bool {
	return ValidCrewTranscriptPath(p) && !strings.ContainsRune(p, ' ')
}

// ParseRollouts keeps the valid, distinct paths of an option's text, newest MaxRollouts at most.
func ParseRollouts(raw string) []string {
	var out []string
	for _, p := range strings.Fields(raw) {
		if RolloutPathOK(p) && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	if len(out) > MaxRollouts {
		out = out[len(out)-MaxRollouts:]
	}
	return out
}

// AppendRollout returns the option text with p added last. A path already listed or not fit for the list
// leaves the list as it is.
func AppendRollout(raw, p string) string {
	cur := ParseRollouts(raw)
	if !RolloutPathOK(p) || slices.Contains(cur, p) {
		return strings.Join(cur, " ")
	}
	cur = append(cur, p)
	if len(cur) > MaxRollouts {
		cur = cur[len(cur)-MaxRollouts:]
	}
	return strings.Join(cur, " ")
}

// ClosedBoat is one transcript of a boat that was closed.
type ClosedBoat struct{ Name, Harness, Path string }

func (b ClosedBoat) token() string { return b.Name + ":" + b.Harness + ":" + b.Path }

// ParseClosed keeps the tokens whose name, harness and path are all valid, since any process in the ship
// can write the option.
func ParseClosed(raw string) []ClosedBoat {
	var out []ClosedBoat
	for _, tok := range strings.Fields(raw) {
		f := strings.SplitN(tok, ":", 3)
		if len(f) != 3 || !CrewName.MatchString(f[0]) || !CrewName.MatchString(f[1]) || !RolloutPathOK(f[2]) {
			continue
		}
		if b := (ClosedBoat{f[0], f[1], f[2]}); !slices.Contains(out, b) {
			out = append(out, b)
		}
	}
	if len(out) > MaxClosedCrew {
		out = out[len(out)-MaxClosedCrew:]
	}
	return out
}

// AppendClosed returns the option text with boats added last, the oldest dropped past MaxClosedCrew.
func AppendClosed(raw string, boats []ClosedBoat) string {
	cur := ParseClosed(raw)
	for _, b := range boats {
		if CrewName.MatchString(b.Name) && CrewName.MatchString(b.Harness) && RolloutPathOK(b.Path) && !slices.Contains(cur, b) {
			cur = append(cur, b)
		}
	}
	if len(cur) > MaxClosedCrew {
		cur = cur[len(cur)-MaxClosedCrew:]
	}
	toks := make([]string, len(cur))
	for i, b := range cur {
		toks[i] = b.token()
	}
	return strings.Join(toks, " ")
}

// ReadSessionOption is "" when the option is unset.
func (s Server) ReadSessionOption(ctx context.Context, session, name string) (string, error) {
	out, err := s.run(call{ctx: ctx, utf8After: true, args: []string{"show-options", "-qv", "-t", "=" + session + ":", Prefix + name}})
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(out, "\n"), nil
}

func (s Server) SetSessionOption(ctx context.Context, session, name, value string) error {
	_, err := s.run(call{ctx: ctx, utf8After: true, args: []string{"set-option", "-t", "=" + session + ":", Prefix + name, value}})
	return err
}

// ReadPaneFacts reads the options usage needs from one pane in one call: its transcript option, its start
// command and its rollout list. Any of them may be empty.
func (s Server) ReadPaneFacts(ctx context.Context, pane string) (transcript, startCmd, rollouts string, err error) {
	const sep = "\x1f"
	out, err := s.run(call{ctx: ctx, utf8After: true, args: []string{"display-message", "-p", "-t", pane,
		"#{" + TranscriptOption + "}" + sep + "#{pane_start_command}" + sep + "#{" + RolloutsOption + "}"}})
	if err != nil {
		return "", "", "", err
	}
	f := strings.Split(strings.TrimSuffix(out, "\n"), sep)
	if len(f) != 3 {
		return "", "", "", nil
	}
	return f[0], f[1], f[2], nil
}
