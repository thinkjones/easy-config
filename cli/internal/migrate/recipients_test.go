package migrate

import (
	"reflect"
	"strings"
	"testing"

	"github.com/thinkjones/easy-config/cli/internal/backend"
)

func fakeKeys(t *testing.T, n int) []string {
	t.Helper()
	var keys []string
	for range n {
		pub, _, err := (&backend.Fake{}).NewKey()
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, pub)
	}
	return keys
}

func oldRecipients(gene, ci string) string {
	return `# Who can decrypt this store.
#
# purpose decides where a rotated private half may be delivered.
recipients:
  gene:
    key: ` + gene + `
    purpose: human
    # Restored from a password manager on a new machine.
    repos: []

  ci:
    key: ` + ci + `
    purpose: machine
    repos:
      - owner/a
      - owner/b
`
}

func TestConvertRecipients(t *testing.T) {
	k := fakeKeys(t, 2)
	valid := (&backend.Fake{}).ValidRecipient
	users, repos, err := convert([]byte(oldRecipients(k[0], k[1])), "2026-10-15", valid)
	if err != nil {
		t.Fatal(err)
	}
	gene, ci := users.Recipients["gene"], users.Recipients["ci"]
	if gene.Key != k[0] || gene.Kind != "user" || len(gene.Repos) != 0 || gene.Added != "2026-10-15" || gene.AddedBy != "migrate" {
		t.Errorf("gene = %+v", gene)
	}
	if ci.Key != k[1] || ci.Kind != "bot" || !reflect.DeepEqual(ci.Repos, []string{"owner/a", "owner/b"}) || ci.Added != "2026-10-15" || ci.AddedBy != "migrate" {
		t.Errorf("ci = %+v", ci)
	}
	if len(users.Recipients) != 2 {
		t.Errorf("recipients = %v", users.Recipients)
	}
	if len(repos.Repos) != 2 {
		t.Fatalf("repos = %v", repos.Repos)
	}
	for _, id := range []string{"owner/a", "owner/b"} {
		r := repos.Repos[id]
		if r.Added != "2026-10-15" || r.AddedBy != "migrate" {
			t.Errorf("%s = %+v", id, r)
		}
	}
}

func TestConvertRecipientsRefuses(t *testing.T) {
	k := fakeKeys(t, 2)
	valid := (&backend.Fake{}).ValidRecipient
	base := oldRecipients(k[0], k[1])
	cases := []struct{ name, in, want, never string }{
		{"purpose", strings.Replace(base, "purpose: human", "purpose: robot", 1), "recipient 1: purpose must be human or machine", ""},
		{"name", strings.Replace(base, "  gene:", "  Bad Name:", 1), "recipient 1: not a valid name", "Bad Name"},
		{"key", strings.Replace(base, k[1], "not-a-key", 1), "recipient 2: the key is not valid", "not-a-key"},
		{"repo", strings.Replace(base, "owner/b", "../x", 1), "recipient 2: a repo is not a valid repo ID", "../x"},
		{"yaml", "recipients: [", "recipients.yaml is not valid YAML", ""},
	}
	for _, c := range cases {
		_, _, err := convert([]byte(c.in), "2026-10-15", valid)
		if err == nil {
			t.Errorf("%s: no error", c.name)
			continue
		}
		if !strings.HasPrefix(err.Error(), c.want) {
			t.Errorf("%s: error %q, want prefix %q", c.name, err, c.want)
		}
		if c.never != "" && strings.Contains(err.Error(), c.never) {
			t.Errorf("%s: error %q echoes %q", c.name, err, c.never)
		}
	}
}

func TestConvertRecipientsSameKeyTwice(t *testing.T) {
	k := fakeKeys(t, 1)
	_, _, err := convert([]byte(oldRecipients(k[0], k[0])), "2026-10-15", (&backend.Fake{}).ValidRecipient)
	if err == nil || !strings.HasPrefix(err.Error(), "recipients.yaml would not load: ") {
		t.Fatalf("error %v", err)
	}
}
