package launcher

import (
	"crypto/rand"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/sessionhost"
)

// claudeSessionFlags are the flags with which the engineer picks, resumes or
// continues a Claude Code session. A ship launched with one has a session
// Admiral did not name, so it gets no conversation (#264).
var claudeSessionFlags = []string{"--session-id", "--resume", "-r", "--continue", "-c"}

// choosesSession is whether args hold one of claudeSessionFlags, alone or as
// --flag=value.
func choosesSession(args []string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		name, _, _ := strings.Cut(a, "=")
		for _, f := range claudeSessionFlags {
			if name == f {
				return true
			}
		}
	}
	return false
}

// ProjectSlug is the directory name Claude Code gives a working directory under
// <config dir>/projects/: every character outside [A-Za-z0-9] becomes "-".
// Claude Code may shorten a long one, so readers also look a session up by its id.
func ProjectSlug(dir string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, dir)
}

// newUUID is a random (version 4) UUID.
func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// conversationAnchor names a new Claude Code session for a ship, so its
// transcript is known before the first hook (ADR 0030). It returns the flags to
// add and the pane option that records where the transcript will be, or nothing
// when the harness is not Claude Code, the engineer's args choose a session, or
// the path is not one Admiral may record.
func conversationAnchor(req Request, dock config.Harness, now time.Time) (flags []string, pane [][2]string) {
	if req.Harness != "claude" || choosesSession(dock.Args) || choosesSession(req.ExtraArgs) {
		return nil, nil
	}
	id, err := newUUID()
	if err != nil {
		return nil, nil
	}
	path := filepath.Join(config.IdentityDir("claude", dock.Env), "projects", ProjectSlug(req.Dir), id+".jsonl")
	if !sessionhost.ValidTranscriptPath(path) {
		return nil, nil
	}
	return []string{"--session-id", id},
		[][2]string{{"transcript", strconv.FormatInt(now.UnixNano(), 10) + " launch " + path}}
}
