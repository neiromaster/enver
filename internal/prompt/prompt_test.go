package prompt

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseSeq(t *testing.T) {
	cases := []struct {
		name     string
		in       []byte
		consumed int
		key      int
	}{
		{"csi left", []byte{'\x1b', '[', 'D'}, 3, keyLeft},
		{"csi right", []byte{'\x1b', '[', 'C'}, 3, keyRight},
		{"csi up", []byte{'\x1b', '[', 'A'}, 3, keyUp},
		{"csi down", []byte{'\x1b', '[', 'B'}, 3, keyDown},
		{"csi home", []byte{'\x1b', '[', 'H'}, 3, keyHome},
		{"csi end", []byte{'\x1b', '[', 'F'}, 3, keyEnd},
		{"csi delete", []byte{'\x1b', '[', '3', '~'}, 4, keyDelete},
		{"csi home tilde", []byte{'\x1b', '[', '1', '~'}, 4, keyHome},
		{"csi end tilde", []byte{'\x1b', '[', '4', '~'}, 4, keyEnd},
		{"ss3 left", []byte{'\x1b', 'O', 'D'}, 3, keyLeft},
		{"ss3 home", []byte{'\x1b', 'O', 'H'}, 3, keyHome},
		{"modified csi consumed", []byte{'\x1b', '[', '1', ';', '2', 'C'}, 6, keyNone},
		{"lone esc", []byte{'\x1b'}, 1, keyNone},
		{"esc then non-sequence", []byte{'\x1b', 'x'}, 1, keyNone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			consumed, key := parseSeq(c.in)
			if consumed != c.consumed || key != c.key {
				t.Errorf("parseSeq(% x) = (%d, %d); want (%d, %d)", c.in, consumed, key, c.consumed, c.key)
			}
		})
	}
}

func TestReadLineNonTTY(t *testing.T) {
	var out bytes.Buffer
	r := newForTest(strings.NewReader("hello world\nsecond line\n"), &out)

	got, err := r.ReadLine("p1> ")
	if err != nil {
		t.Fatalf("first read err = %v", err)
	}
	if got != "hello world" {
		t.Fatalf("first read = %q; want %q", got, "hello world")
	}

	got, err = r.ReadLine("p2> ")
	if got != "second line" {
		t.Fatalf("second read = %q, err=%v; want %q", got, err, "second line")
	}

	if !strings.Contains(out.String(), "p1> ") || !strings.Contains(out.String(), "p2> ") {
		t.Fatalf("prompts not written: %q", out.String())
	}
}

func TestReadLineNonTTYEmpty(t *testing.T) {
	r := newForTest(strings.NewReader("\n"), &bytes.Buffer{})
	got, err := r.ReadLine("p> ")
	if got != "" {
		t.Fatalf("blank line read = %q, err=%v; want %q", got, err, "")
	}
}
