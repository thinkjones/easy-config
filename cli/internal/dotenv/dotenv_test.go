package dotenv

import (
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	src := "# a comment\n" +
		"\n" +
		"PLAIN=alice\n" +
		"export EXPORTED=yes\n" +
		"  SPACED = padded  \n" +
		"COMMENTED=value # trailing\n" +
		"HASH=a#b\n" +
		"SINGLE='lit $HOME \\n' # c\n" +
		"DOUBLE=\"tab\\tnl\\nq\\\" \\$x\"\n" +
		"MULTI=\"line one\n" +
		"line two\"\n" +
		"EMPTY=\n" +
		"WINDOWS=crlf\r\n"
	got, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	want := []Entry{
		{"PLAIN", "alice", 3},
		{"EXPORTED", "yes", 4},
		{"SPACED", "padded", 5},
		{"COMMENTED", "value", 6},
		{"HASH", "a#b", 7},
		{"SINGLE", "lit $HOME \\n", 8},
		{"DOUBLE", "tab\tnl\nq\" $x", 9},
		{"MULTI", "line one\nline two", 10},
		{"EMPTY", "", 12},
		{"WINDOWS", "crlf", 13},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d entries: %+v", len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestParseLastValueWins(t *testing.T) {
	got, err := Parse([]byte("A=1\nB=2\nA=3\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != (Entry{"A", "3", 3}) || got[1] != (Entry{"B", "2", 2}) {
		t.Fatalf("got %+v", got)
	}
}

func TestParseErrorsNameTheLineNeverTheText(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"A=1\nsecret-without-equals\n", "line 2: not NAME=VALUE"},
		{"9BAD=s3cr3t\n", "line 1: not a variable name"},
		{"A=\"s3cr3t\n", "line 1: the quoted value is not closed"},
		{"A='s3cr3t\n", "line 1: the quoted value is not closed"},
		{"A='s3cr3t' tail\n", "line 1: text after the closing quote"},
	} {
		_, err := Parse([]byte(tc.src))
		if err == nil || err.Error() != tc.want {
			t.Errorf("%q: got %v, want %s", tc.src, err, tc.want)
		}
		if err != nil && (strings.Contains(err.Error(), "s3cr3t") || strings.Contains(err.Error(), "secret")) {
			t.Errorf("%q: the error echoes the file: %v", tc.src, err)
		}
	}
}
