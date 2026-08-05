// Package prompt reads a line from standard input with interactive editing
// when stdin is a terminal, and falls back to plain line reading otherwise.
package prompt

import (
	"bufio"
	"errors"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

var ErrInterrupted = errors.New("interrupted")

// Reader reuses one buffered reader across ReadLine calls so piped, multi-line
// input is not lost between prompts.
type Reader struct {
	br    *bufio.Reader
	in    io.Reader
	out   io.Writer
	fd    int
	isTTY bool
}

func New() *Reader {
	fd := int(os.Stdin.Fd())
	return &Reader{
		in:    os.Stdin,
		out:   os.Stdout,
		fd:    fd,
		isTTY: term.IsTerminal(fd),
		br:    bufio.NewReader(os.Stdin),
	}
}

func (r *Reader) ReadLine(prompt string) (string, error) {
	if r.isTTY {
		return readLineInteractive(r.fd, r.in, r.out, prompt)
	}
	_, _ = io.WriteString(r.out, prompt)
	line, err := r.br.ReadString('\n')
	return strings.TrimRight(line, "\r\n"), err
}

func newForTest(in io.Reader, out io.Writer) *Reader {
	return &Reader{in: in, out: out, fd: -1, isTTY: false, br: bufio.NewReader(in)}
}
