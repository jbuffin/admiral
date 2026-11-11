package outcomes

import (
	"bufio"
	"bytes"
)

// MaxLine leaves room for any `ended`: a 1 MiB report can grow sixfold when the
// writer escapes <, > and &.
const MaxLine = 7 << 20

// ReadLine returns a line longer than MaxLine empty and discards the rest unread,
// so one huge record cannot hide the others.
func ReadLine(r *bufio.Reader) ([]byte, error) {
	var line []byte
	tooLong := false
	for {
		chunk, err := r.ReadSlice('\n')
		if !tooLong {
			line = append(line, chunk...)
			tooLong = len(bytes.TrimSuffix(line, []byte("\n"))) > MaxLine
		}
		if err == bufio.ErrBufferFull {
			if tooLong {
				line = nil
			}
			continue
		}
		if tooLong {
			return nil, err
		}
		return bytes.TrimSuffix(line, []byte("\n")), err
	}
}
