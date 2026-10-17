package sessionhost

import (
	"bufio"
	"errors"
	"io"
	"strings"
)

// A control-mode client prints one block per command it was given, in order.
// The flags field of %begin is 1 for a block that answers a command this client
// sent and 0 for any other (the command that started it, a hook's output), so
// only flag-1 blocks are replies:
//
//	%begin <time> <number> <flags>
//	...output lines...
//	%end <time> <number> <flags>      (or %error)
//
// and, between blocks, one-line notifications that start with %. Output lines
// of a block never start with % in a reply we ask for (our formats begin with
// a session name, which the launcher never lets start with %).

type reply struct {
	lines []string
	err   error
}

// parser turns control-mode output into replies, in command order, and calls
// notify for each notification line.
type parser struct {
	replies chan reply
	notify  func(line string)
}

// run reads r until it ends.
func (p *parser) run(r io.Reader) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	var block []string
	in, mine := false, false
	for sc.Scan() {
		line := sc.Text()
		switch {
		case !in && strings.HasPrefix(line, "%begin "):
			f := strings.Fields(line)
			in, block, mine = true, nil, len(f) == 4 && f[3] == "1"
		case in && strings.HasPrefix(line, "%end "):
			in = false
			if mine {
				p.replies <- reply{lines: block}
			}
		case in && strings.HasPrefix(line, "%error "):
			in = false
			if mine {
				p.replies <- reply{lines: block, err: errors.New(strings.Join(block, "\n"))}
			}
		case in:
			block = append(block, line)
		case strings.HasPrefix(line, "%"):
			p.notify(line)
		}
	}
	return sc.Err()
}
