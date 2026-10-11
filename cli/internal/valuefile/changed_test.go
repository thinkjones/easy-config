package valuefile

import (
	"slices"
	"strings"
	"testing"
)

func TestChangedNames(t *testing.T) {
	tests := []struct {
		name     string
		old, new string
		want     []string
	}{
		{"a changed value", "A: one\nB: two\n", "A: one\nB: three\n", []string{"B"}},
		{"an added name", "A: one\n", "A: one\nB: two\n", []string{"B"}},
		{"a removed name", "A: one\nB: two\n", "A: one\n", []string{"B"}},
		{"unchanged", "A: one\nB: two\n", "A: one\nB: two\n", nil},
		{"reordered", "A: one\nB: two\n", "B: two\nA: one\n", nil},
		{"new's order, then old's", "Z: 1\nB: 1\nA: 1\n", "C: 1\nB: 2\n", []string{"C", "B", "Z", "A"}},
		{"a mapping that differs", "A: one\nsops:\n  mac: x\n", "A: one\nsops:\n  mac: y\n", nil},
		{"a mapping only one holds", "A: one\n", "A: one\nsops:\n  mac: y\n", nil},
		{"a scalar that became a mapping", "A: one\n", "A:\n  b: c\n", []string{"A"}},
		{"the fake backend's format", "A: FAKE[b25l]\nB: FAKE[dHdv]\n", "A: FAKE[b25l]\nB: FAKE[dGhyZWU=]\n", []string{"B"}},
		{"an empty new file", "A: one\n", "", []string{"A"}},
		{"comments only", "# nothing\n", "# still nothing\n", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ChangedNames([]byte(tt.old), []byte(tt.new))
			if err != nil {
				t.Fatalf("ChangedNames: %v", err)
			}
			if !slices.Equal(got, tt.want) {
				t.Fatalf("ChangedNames = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestChangedNamesWithoutOld(t *testing.T) {
	got, err := ChangedNames(nil, []byte("B: two\nA: one\nsops:\n  mac: x\n"))
	if err != nil {
		t.Fatalf("ChangedNames: %v", err)
	}
	if want := []string{"B", "A"}; !slices.Equal(got, want) {
		t.Fatalf("ChangedNames = %v, want %v", got, want)
	}
}

func TestChangedNamesInvalid(t *testing.T) {
	for _, bad := range []string{"A: [s3cret\n", "- s3cret\n", "s3cret\n"} {
		for _, args := range [][2]string{{bad, "A: one\n"}, {"A: one\n", bad}} {
			_, err := ChangedNames([]byte(args[0]), []byte(args[1]))
			if err == nil {
				t.Fatalf("ChangedNames(%q, %q): no error", args[0], args[1])
			}
			if strings.Contains(err.Error(), "s3cret") {
				t.Fatalf("the error carries the file's text: %v", err)
			}
		}
	}
}
