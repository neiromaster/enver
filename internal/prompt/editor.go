package prompt

import (
	"io"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/term"
)

const (
	esc byte = 0x1b

	keyCtrlA = 0x01
	keyCtrlB = 0x02
	keyCtrlC = 0x03
	keyCtrlD = 0x04
	keyCtrlE = 0x05
	keyCtrlF = 0x06
	keyCtrlH = 0x08
	keyCtrlK = 0x0b
	keyCtrlU = 0x15
	keyCtrlW = 0x17
	keyDel   = 0x7f
)

const (
	keyNone = iota
	keyLeft
	keyRight
	keyUp
	keyDown
	keyHome
	keyEnd
	keyDelete
)

func readLineInteractive(fd int, in io.Reader, out io.Writer, prompt string) (string, error) {
	old, err := term.MakeRaw(fd)
	if err != nil {
		return "", err
	}
	defer term.Restore(fd, old)

	write := func(s string) { _, _ = io.WriteString(out, s) }

	var buf []rune
	pos := 0

	// redraw re-renders prompt + buffer, clears the remainder of the line, and
	// repositions the cursor at the editing position.
	redraw := func() {
		var b strings.Builder
		b.WriteString("\r")
		b.WriteString(prompt)
		b.WriteString(string(buf))
		b.WriteString("\x1b[K")
		for n := len(buf) - pos; n > 0; n-- {
			b.WriteString("\x1b[D")
		}
		write(b.String())
	}

	write(prompt)

	chunk := make([]byte, 256)
	for {
		n, readErr := in.Read(chunk)
		i := 0
		for i < n {
			b := chunk[i]
			switch {
			case b == '\n' || b == '\r':
				write("\r\n")
				return string(buf), nil
			case b == keyCtrlC:
				write("^C\r\n")
				return "", ErrInterrupted
			case b == keyCtrlD:
				if len(buf) == 0 {
					write("\r\n")
					return "", io.EOF
				}
				if pos < len(buf) {
					buf = append(buf[:pos], buf[pos+1:]...)
					redraw()
				}
			case b == keyDel || b == keyCtrlH:
				if pos > 0 {
					buf = append(buf[:pos-1], buf[pos:]...)
					pos--
					redraw()
				}
			case b == keyCtrlA:
				pos = 0
				redraw()
			case b == keyCtrlE:
				pos = len(buf)
				redraw()
			case b == keyCtrlB:
				if pos > 0 {
					pos--
					redraw()
				}
			case b == keyCtrlF:
				if pos < len(buf) {
					pos++
					redraw()
				}
			case b == keyCtrlK:
				if pos < len(buf) {
					buf = buf[:pos]
					redraw()
				}
			case b == keyCtrlU:
				if pos > 0 {
					buf = buf[pos:]
					pos = 0
					redraw()
				}
			case b == keyCtrlW:
				j := pos
				for j > 0 && unicode.IsSpace(buf[j-1]) {
					j--
				}
				for j > 0 && !unicode.IsSpace(buf[j-1]) {
					j--
				}
				if j < pos {
					buf = append(buf[:j], buf[pos:]...)
					pos = j
					redraw()
				}
			case b == esc:
				consumed, key := parseSeq(chunk[i:n])
				i += consumed
				switch key {
				case keyLeft:
					if pos > 0 {
						pos--
						redraw()
					}
				case keyRight:
					if pos < len(buf) {
						pos++
						redraw()
					}
				case keyHome:
					pos = 0
					redraw()
				case keyEnd:
					pos = len(buf)
					redraw()
				case keyDelete:
					if pos < len(buf) {
						buf = append(buf[:pos], buf[pos+1:]...)
						redraw()
					}
				}
				continue
			case b < 0x20:
			default:
				r, size := utf8.DecodeRune(chunk[i:n])
				if r == utf8.RuneError && size == 1 {
					i++
					continue
				}
				buf = slices.Insert(buf, pos, r)
				pos++
				redraw()
				i += size
				continue
			}
			i++
		}
		if readErr != nil {
			write("\r\n")
			if readErr == io.EOF {
				return string(buf), io.EOF
			}
			return string(buf), readErr
		}
	}
}

// parseSeq decodes a terminal escape sequence beginning at p[0] (ESC). It
// returns the number of bytes consumed and the logical key. A lone ESC not
// followed by a recognized sequence consumes only the ESC byte (keyNone) so the
// following input is processed normally.
func parseSeq(p []byte) (int, int) {
	if len(p) < 2 {
		return 1, keyNone
	}
	switch p[1] {
	case '[':
		if len(p) < 3 {
			return 1, keyNone
		}
		switch p[2] {
		case 'A':
			return 3, keyUp
		case 'B':
			return 3, keyDown
		case 'C':
			return 3, keyRight
		case 'D':
			return 3, keyLeft
		case 'H':
			return 3, keyHome
		case 'F':
			return 3, keyEnd
		case '3':
			if len(p) >= 4 && p[3] == '~' {
				return 4, keyDelete
			}
		case '1', '7':
			if len(p) >= 4 && p[3] == '~' {
				return 4, keyHome
			}
		case '4', '8':
			if len(p) >= 4 && p[3] == '~' {
				return 4, keyEnd
			}
		}
		// Unknown CSI (including modified keys such as "1;2C"): consume up to
		// and including the final byte so it is not mistaken for input.
		for j := 2; j < len(p); j++ {
			if p[j] >= 0x40 && p[j] <= 0x7e {
				return j + 1, keyNone
			}
		}
		return 1, keyNone
	case 'O':
		if len(p) < 3 {
			return 1, keyNone
		}
		switch p[2] {
		case 'A':
			return 3, keyUp
		case 'B':
			return 3, keyDown
		case 'C':
			return 3, keyRight
		case 'D':
			return 3, keyLeft
		case 'H':
			return 3, keyHome
		case 'F':
			return 3, keyEnd
		}
		return 3, keyNone
	}
	return 1, keyNone
}
