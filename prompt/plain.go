package prompt

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
)

// PlainSession is a [Session] over ordinary streams: one line at a time, with no
// editing and no completion. It is what the tests drive a form with, which keeps
// the retry loop testable without a pty.
type PlainSession struct {
	in  *bufio.Reader
	out io.Writer
}

// NewPlainSession returns a session reading lines from in and writing to out.
func NewPlainSession(in io.Reader, out io.Writer) *PlainSession {
	return &PlainSession{in: bufio.NewReader(in), out: out}
}

func (s *PlainSession) Write(p []byte) (int, error) { return s.out.Write(p) }

// Completes reports that Tab does nothing here.
func (s *PlainSession) Completes() bool { return false }

// Width is the terminal width a stream does not have. Eighty is what every tool
// falls back to and what a captured transcript is read at.
func (s *PlainSession) Width() int { return 80 }

// ReadLine writes the label and reads one line. A stream cannot be edited, so
// the seed is shown in the prompt rather than placed on the line — the value
// being corrected is still in front of the reader either way.
func (s *PlainSession) ReadLine(q Question) (string, error) {
	label := q.Label
	if q.Seed != "" {
		label = fmt.Sprintf("%s(was %q) ", label, q.Seed)
	}
	_, _ = fmt.Fprint(s.out, label)
	line, err := s.in.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	if line == "" && errors.Is(err, io.EOF) {
		return "", io.EOF
	}
	return strings.TrimRight(line, "\r\n"), nil
}
