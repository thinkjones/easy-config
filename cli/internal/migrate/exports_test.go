package migrate

import (
	"fmt"
	"strings"
	"testing"
)

func TestParseExportsRoundTripsEveryByte(t *testing.T) {
	values := []string{"", "plain", "it's", "a\nb", "$HOME and $$", `back\slash`, "trail ", "'''"}
	var out strings.Builder
	for i, v := range values {
		fmt.Fprintf(&out, "export A%d='%s'\n", i, strings.ReplaceAll(v, "'", `'\''`))
	}
	got, err := parseExports([]byte(out.String()))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(values) {
		t.Fatalf("got %d names, want %d", len(got), len(values))
	}
	for i, v := range values {
		if g := got[fmt.Sprintf("A%d", i)]; g != v {
			t.Errorf("A%d = %q, want %q", i, g, v)
		}
	}
}

func TestParseExportsRefusesOtherText(t *testing.T) {
	for _, in := range []string{"echo hi\n", "export A=unquoted\n", "export A='open\n", "export 1A='x'\n", "export A='x' trailing\n"} {
		_, err := parseExports([]byte(in))
		if err == nil {
			t.Errorf("%q: no error", in)
			continue
		}
		if err.Error() != "the old tool printed something that is not an export line" {
			t.Errorf("%q: error %q", in, err)
		}
	}
}

func TestParseExportsEmpty(t *testing.T) {
	got, err := parseExports(nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v", got, err)
	}
}
