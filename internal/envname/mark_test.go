package envname

import "testing"

func TestSplitMarked(t *testing.T) {
	cases := []struct {
		in     string
		name   string
		marked bool
	}{
		{"PROJECT_PATH", "PROJECT_PATH", false},
		{"~PROJECT_PATH", "PROJECT_PATH", true},
		{"~", "", true},
		{"~~KEY", "~KEY", true},
		{"A~B", "A~B", false},
	}
	for _, c := range cases {
		name, marked := SplitMarked(c.in)
		if name != c.name || marked != c.marked {
			t.Errorf("SplitMarked(%q) = (%q,%v), want (%q,%v)", c.in, name, marked, c.name, c.marked)
		}
	}
}

func TestJoinMarkedRoundTrip(t *testing.T) {
	for _, in := range []string{"KEY", "~KEY"} {
		name, marked := SplitMarked(in)
		if got := JoinMarked(name, marked); got != in {
			t.Errorf("JoinMarked(SplitMarked(%q)) = %q", in, got)
		}
	}
}
