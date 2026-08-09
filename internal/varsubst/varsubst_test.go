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

func TestExpandPrecedenceAndRefs(t *testing.T) {
	env := map[string]string{
		"DB_HOST": "localhost",
		"DB_URL":  "postgres://$DB_HOST/db", // intra-profile ref
		"TOKEN":   "$SECRET",                // resolves from osEnv
	}
	osEnv := map[string]string{"SECRET": "sk-live", "DB_HOST": "ignored"}
	out := Expand(env, osEnv)
	if out["DB_URL"] != "postgres://localhost/db" {
		t.Errorf("DB_URL = %q, want postgres://localhost/db (profile wins)", out["DB_URL"])
	}
	if out["TOKEN"] != "sk-live" {
		t.Errorf("TOKEN = %q, want sk-live (osEnv fallback)", out["TOKEN"])
	}
	if out["DB_HOST"] != "localhost" {
		t.Errorf("DB_HOST = %q, want localhost", out["DB_HOST"])
	}
}

func TestExpandForwardRefAndFixpoint(t *testing.T) {
	// DB_URL references DB_HOST which sorts after it; depth-first must still resolve.
	env := map[string]string{"DB_URL": "$DB_HOST/db", "DB_HOST": "h"}
	if out := Expand(env, nil); out["DB_URL"] != "h/db" {
		t.Errorf("forward ref: DB_URL = %q, want h/db", out["DB_URL"])
	}
}

func TestExpandCyclesAndSelfEmpty(t *testing.T) {
	out := Expand(map[string]string{"A": "$B", "B": "$A"}, nil) // 2-key cycle
	if out["A"] != "" || out["B"] != "" {
		t.Errorf("cycle should resolve empty: A=%q B=%q", out["A"], out["B"])
	}
	out = Expand(map[string]string{"A": "$A"}, nil) // self-ref
	if out["A"] != "" {
		t.Errorf("self-ref should resolve empty: A=%q", out["A"])
	}
}

func TestExpandUndefinedEmpty(t *testing.T) {
	if out := Expand(map[string]string{"A": "$NOPE"}, nil); out["A"] != "" {
		t.Errorf("undefined should be empty: A=%q", out["A"])
	}
}
