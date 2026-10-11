package recipients

import (
	"errors"
	"strings"
	"testing"
)

func okKey(string) error { return nil }

const goodReq = "name: gene-mini\nkey: k2\nkind: machine\nowner: gene\nhost: mini.local\nrequested: 2026-10-11\n"

func TestParseRequest(t *testing.T) {
	q, err := ParseRequest("gene-mini", []byte(goodReq), okKey)
	if err != nil || q.Owner != "gene" || q.Host != "mini.local" {
		t.Fatalf("ParseRequest = %+v, %v", q, err)
	}
	back, err := ParseRequest("gene-mini", q.Marshal(), okKey)
	if err != nil || back != q {
		t.Fatalf("round trip = %+v, %v", back, err)
	}
	if RequestPath("x") != "requests/x.yaml" {
		t.Errorf("RequestPath = %s", RequestPath("x"))
	}
	bad := map[string]struct{ name, body string }{
		"name differs":  {"other", goodReq},
		"invalid name":  {"Bad Name", strings.Replace(goodReq, "gene-mini", "Bad Name", 1)},
		"kind bot":      {"gene-mini", strings.Replace(goodReq, "machine", "bot", 1)},
		"no owner":      {"gene-mini", strings.Replace(goodReq, "owner: gene\n", "", 1)},
		"unknown field": {"gene-mini", goodReq + "extra: 1\n"},
		"not yaml":      {"gene-mini", "::: ["},
	}
	for label, c := range bad {
		if _, err := ParseRequest(c.name, []byte(c.body), okKey); err == nil {
			t.Errorf("%s: no error", label)
		}
	}
	refuse := func(string) error { return errors.New("no") }
	if _, err := ParseRequest("gene-mini", []byte(goodReq), refuse); err == nil {
		t.Error("a key the validator refuses was accepted")
	}
	user := "name: sam\nkey: k\nkind: user\nhost: h\nrequested: 2026-10-11\n"
	if _, err := ParseRequest("sam", []byte(user), okKey); err != nil {
		t.Errorf("user request: %v", err)
	}
}

func TestPrintable(t *testing.T) {
	if got := Printable("a\x1b[31mb"); got != "a?[31mb" {
		t.Errorf("Printable = %q", got)
	}
	if got := Printable(strings.Repeat("x", 81)); len(got) != 80 {
		t.Errorf("len = %d", len(got))
	}
	if got := Printable("é"); got != "?" {
		t.Errorf("Printable(é) = %q", got)
	}
}
