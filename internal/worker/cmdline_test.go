package worker

import (
	"reflect"
	"testing"
)

// The expectations below follow BOINC's own parse_command_line
// (lib/str_util.cpp): only a token that starts with a quote is quoted.
func TestSplitLikeBOINC(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"   ", nil},
		{"--a 1 --b", []string{"--a", "1", "--b"}},
		{"  --a   1 ", []string{"--a", "1"}},
		{`'two words' next`, []string{"two words", "next"}},
		{`"two words" next`, []string{"two words", "next"}},
		{`--name="a b"`, []string{`--name="a`, `b"`}}, // a quote inside a token is literal
		{`it's fine`, []string{"it's", "fine"}},
		{`"unterminated rest`, []string{"unterminated rest"}},
		{"a\tb\nc", []string{"a", "b", "c"}},
		{`'single "double" inside'`, []string{`single "double" inside`}},
	}
	for _, c := range cases {
		if got := splitLikeBOINC(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("splitLikeBOINC(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSplitQuotedWordsKeepsTheWindowsBehaviour(t *testing.T) {
	got := splitQuotedWords(`--name="a b" -x`)
	want := []string{"--name=a b", "-x"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}
