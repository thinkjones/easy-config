package backend_test

import (
	"testing"

	"github.com/thinkjones/easy-config/cli/internal/backend"
)

func TestSecretLine(t *testing.T) {
	for _, c := range []struct{ text, want string }{
		{"# created: 2026-10-11\n# public key: age1xyz\nAGE-SECRET-KEY-1ABC\n", "AGE-SECRET-KEY-1ABC\n"},
		{"\n  # a comment\r\n\t AGE-SECRET-KEY-1ABC \r\nAGE-SECRET-KEY-1SECOND\n", "AGE-SECRET-KEY-1ABC\n"},
		{"FAKE-KEY-aa01", "FAKE-KEY-aa01\n"},
		{"# public key: age1xyz\n\n", ""},
		{"", ""},
	} {
		if got := string(backend.SecretLine([]byte(c.text))); got != c.want {
			t.Errorf("SecretLine(%q) = %q, want %q", c.text, got, c.want)
		}
	}
	// The line is a copy: the text it was taken from is not written to.
	text := []byte("FAKE-KEY-aa01\r\nmore")
	backend.SecretLine(text)
	if string(text) != "FAKE-KEY-aa01\r\nmore" {
		t.Fatalf("the text is now %q", text)
	}
}
