package varsubst

import "testing"

func testLookup(m map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		v, ok := m[name]
		return v, ok
	}
}

func TestExpandValueBare(t *testing.T) {
	lk := testLookup(map[string]string{"HOST": "db", "EMPTY": ""})
	cases := []struct{ in, want string }{
		{"plain", "plain"},
		{"$HOST", "db"},
		{"pre-$HOST-post", "pre-db-post"},
		{"$MISSING", ""},           // undefined -> empty
		{"a$$b", "a$b"},            // $$ literal $
		{"$$$HOST", "$db"},         // $$ then $HOST
		{"$(whoami)", "$(whoami)"}, // not a ref -> literal
		{"$5", "$5"},               // digit after $ -> literal
		{"trailing$", "trailing$"}, // trailing $ literal
	}
	for _, c := range cases {
		if got := expandValue(c.in, lk); got != c.want {
			t.Errorf("expandValue(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestExpandValueBraced(t *testing.T) {
	lk := testLookup(map[string]string{"SET": "v", "EMPTY": ""})
	cases := []struct{ in, want string }{
		{"${SET}", "v"},
		{"${MISSING}", ""},
		{"${EMPTY:-def}", "def"},     // empty -> default
		{"${SET:-def}", "v"},         // set non-empty -> value
		{"${EMPTY-def}", ""},         // set (even empty) -> value
		{"${MISSING-def}", "def"},    // unset -> default
		{"${SET:+alt}", "alt"},       // set non-empty -> alt
		{"${EMPTY:+alt}", ""},        // empty -> nothing
		{"${EMPTY+alt}", "alt"},      // set (even empty) -> alt
		{"${MISSING+alt}", ""},       // unset -> nothing
		{"${SET:-$EMPTY}", "v"},      // operand expanded
		{"${unclosed", "${unclosed"}, // no closing brace -> literal
	}
	for _, c := range cases {
		if got := expandValue(c.in, lk); got != c.want {
			t.Errorf("expandValue(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
