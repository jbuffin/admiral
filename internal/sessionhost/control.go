package sessionhost

import (
	"bufio"
	"errors"
	"io"
	"strings"
)

// A control-mode client prints one block per command it was given, in order:
//
//	%begin <time> <number> <flags>
//	...output lines...
//	%end <time> <number> <flags>      (or %error)
//
// and, between blocks, one-line notifications that start with %. tmux prints
// an option value raw, newlines included, so an output line can look like
// anything, a %end too. A block therefore closes only on the %end or %error
// that repeats its %begin's time, number and flags. The flags field is 1 for a
// block that answers a command this client sent and 0 for any other (the
// command that started it, a hook's output), so only flag-1 blocks are replies.

type reply struct {
	lines []string
	err   error
}

// parser turns control-mode output into replies, in command order, and calls
// notify for each notification line. With onReply set, replies are handed to it
// on the parser's own goroutine instead of the channel, so a reply and the
// notifications around it are seen in the order tmux sent them.
type parser struct {
	replies chan reply
	onReply func(reply)
	notify  func(line string)
}

func (p *parser) deliver(r reply) {
	if p.onReply != nil {
		p.onReply(r)
		return
	}
	p.replies <- r
}

// run reads r until it ends.
func (p *parser) run(r io.Reader) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	var block []string
	in, mine, id := false, false, ""
	for sc.Scan() {
		line := sc.Text()
		switch {
		case !in && strings.HasPrefix(line, "%begin "):
			f := strings.Fields(line)
			in, block, mine = true, nil, len(f) == 4 && f[3] == "1"
			id = strings.TrimPrefix(line, "%begin ")
		case in && strings.HasPrefix(line, "%end ") && strings.TrimPrefix(line, "%end ") == id:
			in = false
			if mine {
				p.deliver(reply{lines: block})
			}
		case in && strings.HasPrefix(line, "%error ") && strings.TrimPrefix(line, "%error ") == id:
			in = false
			if mine {
				p.deliver(reply{lines: block, err: errors.New(strings.Join(block, "\n"))})
			}
		case in:
			block = append(block, line)
		case strings.HasPrefix(line, "%"):
			p.notify(line)
		}
	}
	return sc.Err()
}
