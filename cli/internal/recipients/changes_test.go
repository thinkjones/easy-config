package recipients

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func base() Registry {
	return Registry{Recipients: map[string]Recipient{
		"gene":      {Key: "k1", Kind: KindUser},
		"gene-mini": {Key: "k2", Kind: KindMachine, Owner: "gene"},
		"ci":        {Key: "k3", Kind: KindBot},
	}}
}

func TestWith(t *testing.T) {
	r := base()
	next, err := r.With("sam", Recipient{Key: "k4", Kind: KindUser})
	if err != nil || next.Recipients["sam"].Key != "k4" || len(next.Recipients) != 4 {
		t.Fatalf("With = %v, %v", next, err)
	}
	if len(r.Recipients) != 3 {
		t.Fatal("receiver changed")
	}
	cases := map[string]struct {
		name string
		rec  Recipient
	}{
		"bad name":     {"Sam!", Recipient{Key: "k4", Kind: KindUser}},
		"taken":        {"gene", Recipient{Key: "k4", Kind: KindUser}},
		"key taken":    {"sam", Recipient{Key: "k2", Kind: KindUser}},
		"owner absent": {"m", Recipient{Key: "k4", Kind: KindMachine, Owner: "nobody"}},
		"owner is bot": {"m", Recipient{Key: "k4", Kind: KindMachine, Owner: "ci"}},
		"bad kind":     {"sam", Recipient{Key: "k4", Kind: "robot"}},
	}
	for label, c := range cases {
		before := base()
		if _, err := before.With(c.name, c.rec); err == nil {
			t.Errorf("%s: no error", label)
		}
		if !reflect.DeepEqual(before, base()) {
			t.Errorf("%s: receiver changed", label)
		}
	}
	if _, err := r.With("sam", Recipient{Key: "k2", Kind: KindUser}); err == nil || !strings.Contains(err.Error(), "gene-mini") {
		t.Errorf("key error does not name its holder: %v", err)
	}
}

func TestWithout(t *testing.T) {
	r := base()
	next, err := r.Without("ci")
	if err != nil || len(next.Recipients) != 2 || len(r.Recipients) != 3 {
		t.Fatalf("Without = %v, %v", next, err)
	}
	if _, err := r.Without("nobody"); err == nil {
		t.Error("no error for a missing name")
	}
	_, err = r.Without("gene")
	var owns *OwnsError
	if !errors.As(err, &owns) || !reflect.DeepEqual(owns.Machines, []string{"gene-mini"}) {
		t.Errorf("owner removal = %v", err)
	}
	one := Registry{Recipients: map[string]Recipient{"a": {Key: "k", Kind: KindUser}}}
	if _, err := one.Without("a"); !errors.Is(err, ErrLast) {
		t.Errorf("last = %v", err)
	}
	if len(one.Recipients) != 1 || len(r.Recipients) != 3 {
		t.Error("receiver changed")
	}
}

func TestWithKey(t *testing.T) {
	r := base()
	next, err := r.WithKey("gene", "k9")
	if err != nil || next.Recipients["gene"].Key != "k9" || r.Recipients["gene"].Key != "k1" {
		t.Fatalf("WithKey = %v, %v", next, err)
	}
	if _, err := r.WithKey("gene", "k3"); err == nil || !strings.Contains(err.Error(), "ci") {
		t.Errorf("duplicate key = %v", err)
	}
	if _, err := r.WithKey("nobody", "k9"); err == nil {
		t.Error("no error for a missing name")
	}
	if r.Recipients["gene"].Key != "k1" {
		t.Error("receiver changed")
	}
}
