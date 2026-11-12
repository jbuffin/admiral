package sessionhost

import (
	"context"
	"errors"
	"strconv"
	"strings"
)

// MaxStatuslineRead bounds a status line the read takes: the wrapper keeps at most 4 KB, and a pane can write more.
const MaxStatuslineRead = 4096

// LivePane is what one pane says about its live usage: a Claude pane's kept status line, a Copilot ship's transcript.
type LivePane struct {
	Session    string // the ship's session name, or a chat's
	Harness    string
	Transcript string // the @admiral-transcript value
	StartCmd   string
	Statusline string // the @admiral-statusline value
}

// livePaneFilter keeps panes that hold a status line or a transcript and are not crew boats, whose figures are not live-tracked.
const livePaneFilter = "#{&&:#{||:#{" + StatuslineOption + "},#{" + TranscriptOption + "}},#{==:" + CrewNameFormat + ",}}"

// ReadLivePanes reads every ship's and the chat's pane in one tmux call. The values are untrusted: anything
// running in a pane can write them. The status line goes last, so a separator in it cannot shift the others.
func (s Server) ReadLivePanes(ctx context.Context) ([]LivePane, error) {
	format := strings.Join([]string{unprintable("#{session_name}"), unprintable("#{" + Prefix + "harness}"),
		unprintable("#{" + TranscriptOption + "}"), "#{s/" + ctrl + "/ /:pane_start_command}", "#{=" + strconv.Itoa(MaxStatuslineRead+1) + ":" + unprintable("#{"+StatuslineOption+"}") + "}"}, sep)
	out, err := s.run(call{ctx: ctx, utf8After: true, args: []string{"list-panes", "-a", "-f", livePaneFilter, "-F", format}})
	if err != nil {
		if strings.Contains(out, "no server running") || strings.Contains(out, "error connecting") {
			return nil, nil
		}
		return nil, errors.New(strings.TrimSpace(out))
	}
	var panes []LivePane
	for _, row := range strings.Split(out, "\n") {
		if f := strings.SplitN(row, sep, 5); len(f) == 5 {
			panes = append(panes, LivePane{Session: f[0], Harness: f[1], Transcript: f[2], StartCmd: f[3], Statusline: f[4]})
		}
	}
	return panes, nil
}
