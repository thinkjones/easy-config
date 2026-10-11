package storeaudit

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/thinkjones/easy-config/cli/internal/backend"
)

const decls = `schemaVersion: v1
required:
  A: {type: str, desc: a}
  TOKEN: {type: secret, desc: t}
optional:
  B: {type: str, desc: b}
  URL: {type: url, desc: u}
`

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// build makes a store folder: manifest.yaml, the raw files as given (an encrypted file in the fake's
// own format may be given raw to hold an unencrypted line), and enc files written through the backend.
func build(t *testing.T, manifest string, raw map[string]string, enc map[string][]backend.Pair) (string, *backend.Fake) {
	t.Helper()
	dir := t.TempDir()
	write(t, filepath.Join(dir, "manifest.yaml"), manifest)
	for name, content := range raw {
		write(t, filepath.Join(dir, name), content)
	}
	fake := &backend.Fake{}
	write(t, filepath.Join(dir, backend.FakePolicy), "")
	for name, pairs := range enc {
		for _, p := range pairs {
			if err := fake.Set(filepath.Join(dir, name), p.Name, p.Value); err != nil {
				t.Fatal(err)
			}
		}
	}
	return dir, fake
}

func audit(t *testing.T, dir string, b backend.Backend) Result {
	t.Helper()
	res, err := Audit(dir, b)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// only fails unless res has exactly one finding, and returns it.
func only(t *testing.T, res Result) Finding {
	t.Helper()
	if len(res.Findings) != 1 {
		t.Fatalf("want one finding, got %+v", res.Findings)
	}
	return res.Findings[0]
}

func tok(v string) map[string][]backend.Pair {
	return map[string][]backend.Pair{"common.enc.yaml": {{Name: "TOKEN", Value: v}}}
}

func TestCleanStore(t *testing.T) {
	dir, b := build(t, decls, map[string]string{"common.yaml": "A: x\n"}, tok("s3"))
	res := audit(t, dir, b)
	if !res.OK() || res.Declared != 4 || !reflect.DeepEqual(res.Files, []string{"common.yaml", "common.enc.yaml"}) {
		t.Fatalf("got %+v", res)
	}
	want := "store " + dir + ": 4 declared, 2 files\n✓ nothing to fix\n"
	if got := res.Report(); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestUndeclared(t *testing.T) {
	dir, b := build(t, decls, map[string]string{"common.yaml": "A: x\nSTRAY: y\n"}, tok("s3"))
	f := only(t, audit(t, dir, b))
	want := Finding{Name: "STRAY", File: "common.yaml", Why: "not declared in manifest.yaml",
		Fix: "Declare it with a type and a desc, or remove it with `ezc unset -g STRAY`."}
	if f != want {
		t.Fatalf("got %+v", f)
	}
}

func TestUndeclaredInEncryptedFile(t *testing.T) {
	enc := map[string][]backend.Pair{"env.prod.enc.yaml": {{Name: "STRAY", Value: "v"}}}
	dir, b := build(t, decls, map[string]string{"common.yaml": "A: x\n"}, enc)
	write(t, filepath.Join(dir, "common.enc.yaml"), "")
	_ = b.Set(filepath.Join(dir, "common.enc.yaml"), "TOKEN", "s3")
	f := only(t, audit(t, dir, b))
	if f.Name != "STRAY" || f.File != "env.prod.enc.yaml" || f.Why != "not declared in manifest.yaml" ||
		!strings.Contains(f.Fix, "`ezc unset -g STRAY --env prod`") {
		t.Fatalf("got %+v", f)
	}
}

func TestMissingRequired(t *testing.T) {
	dir, b := build(t, decls, map[string]string{"common.yaml": "B: x\n"}, tok("s3"))
	f := only(t, audit(t, dir, b))
	want := Finding{Name: "A", Why: "declared but no file sets it",
		Fix: "Set it with `ezc set -g A`, or remove the declaration with `ezc unset -g A --undeclare`."}
	if f != want {
		t.Fatalf("got %+v", f)
	}
}

func TestOptionalMayBeAbsent(t *testing.T) {
	dir, b := build(t, decls, map[string]string{"common.yaml": "A: x\n"}, tok("s3"))
	if res := audit(t, dir, b); !res.OK() {
		t.Fatalf("got %+v", res.Findings)
	}
}

func TestSetOnlyInOneEnvironmentCounts(t *testing.T) {
	dir, b := build(t, decls, map[string]string{"env.prod.yaml": "A: x\n"}, tok("s3"))
	res := audit(t, dir, b)
	if !res.OK() || !reflect.DeepEqual(res.Files, []string{"common.enc.yaml", "env.prod.yaml"}) {
		t.Fatalf("got %+v", res)
	}
}

func TestSecretInPlaintext(t *testing.T) {
	dir, b := build(t, decls, map[string]string{"common.yaml": "A: x\nTOKEN: hunter2\n"}, nil)
	f := only(t, audit(t, dir, b))
	want := Finding{Name: "TOKEN", File: "common.yaml", Why: "is a secret but is in plaintext",
		Fix: "It is in git history in plaintext: remove it with `ezc unset -g TOKEN`, then give it a new value with `ezc set -g TOKEN`."}
	if f != want {
		t.Fatalf("got %+v", f)
	}
}

func TestPlaintextInSecrets(t *testing.T) {
	enc := tok("s3")
	enc["env.prod.enc.yaml"] = []backend.Pair{{Name: "A", Value: "x"}}
	dir, b := build(t, decls, nil, enc)
	f := only(t, audit(t, dir, b))
	want := Finding{Name: "A", File: "env.prod.enc.yaml", Why: "is not a secret but is in an encrypted file",
		Fix: "Declare it secret, or move it: `ezc unset -g A --env prod`, then `ezc set -g A --env prod`."}
	if f != want {
		t.Fatalf("got %+v", f)
	}
}

func TestUnencryptedValueInEncryptedFile(t *testing.T) {
	dir, b := build(t, decls, map[string]string{"common.yaml": "A: x\n", "common.enc.yaml": "TOKEN: plain\n"}, nil)
	f := only(t, audit(t, dir, b))
	want := Finding{Name: "TOKEN", File: "common.enc.yaml", Why: "is in an encrypted file but its value is not encrypted",
		Fix: "Set it again with `ezc set -g TOKEN` and treat the old value as leaked."}
	if f != want {
		t.Fatalf("got %+v", f)
	}
}

func TestPlaceholderAndShape(t *testing.T) {
	dir, b := build(t, decls, map[string]string{"common.yaml": "A: replace-me\nURL: ftp://x\n"}, tok("s3"))
	res := audit(t, dir, b)
	if len(res.Findings) != 2 {
		t.Fatalf("got %+v", res.Findings)
	}
	if f := res.Findings[0]; f.Name != "A" || f.File != "common.yaml" || f.Why != "is still a placeholder" || f.Fix != "" {
		t.Fatalf("got %+v", f)
	}
	if f := res.Findings[1]; f.Name != "URL" || f.Why != "not a URL (must start with http:// or https://)" {
		t.Fatalf("got %+v", f)
	}
}

func TestSecretPlaceholderInEncryptedFile(t *testing.T) {
	dir, b := build(t, decls, map[string]string{"common.yaml": "A: x\n"}, tok("changeme"))
	f := only(t, audit(t, dir, b))
	if f.Name != "TOKEN" || f.File != "common.enc.yaml" || f.Why != "is still a placeholder" {
		t.Fatalf("got %+v", f)
	}
}

func TestShapeAppliesToEveryOccurrence(t *testing.T) {
	dir, b := build(t, decls, map[string]string{"common.yaml": "A: x\nURL: http://ok\n", "env.prod.yaml": "URL: ftp://x\n"}, tok("s3"))
	f := only(t, audit(t, dir, b))
	if f.Name != "URL" || f.File != "env.prod.yaml" {
		t.Fatalf("got %+v", f)
	}
}

func TestPlaintextReferenceSkipsShape(t *testing.T) {
	dir, b := build(t, decls, map[string]string{"common.yaml": "A: x\nURL: $A\n"}, tok("s3"))
	if res := audit(t, dir, b); !res.OK() {
		t.Fatalf("got %+v", res.Findings)
	}
}

func TestSelectorNameIsAFinding(t *testing.T) {
	dir, b := build(t, decls, map[string]string{"common.yaml": "A: x\nEZC_ENV: prod\n"}, tok("s3"))
	f := only(t, audit(t, dir, b))
	if f.Name != "EZC_ENV" || f.File != "common.yaml" || f.Why != "names the environment and cannot be stored" {
		t.Fatalf("got %+v", f)
	}
}

func TestFindingsNeverContainValues(t *testing.T) {
	raw := map[string]string{
		"common.yaml":     "A: distinct-plain-1\nSTRAY: distinct-plain-2\nTOKEN: distinct-plain-3\nURL: distinct-plain-4\n",
		"common.enc.yaml": "B: distinct-leak-5\n",
	}
	enc := map[string][]backend.Pair{"env.prod.enc.yaml": {{Name: "A", Value: "distinct-enc-6"}, {Name: "NOPE", Value: "distinct-enc-7"}}}
	dir, b := build(t, decls, raw, enc)
	res := audit(t, dir, b)
	if len(res.Findings) < 5 {
		t.Fatalf("got %+v", res.Findings)
	}
	out := res.Report()
	for _, f := range res.Findings {
		out += f.String()
	}
	if strings.Contains(out, "distinct-") {
		t.Fatalf("a value leaked: %s", out)
	}
}

func TestKeyless(t *testing.T) {
	enc := map[string][]backend.Pair{"common.enc.yaml": {{Name: "TOKEN", Value: "replace-me"}, {Name: "STRAY", Value: "v"}, {Name: "A", Value: "v"}}}
	dir, b := build(t, decls, nil, enc)
	b.Locked = true
	res := audit(t, dir, b)
	var got []string
	for _, f := range res.Findings {
		got = append(got, f.Name+":"+f.Why)
	}
	want := []string{
		"A:is not a secret but is in an encrypted file",
		"STRAY:not declared in manifest.yaml",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
	if !reflect.DeepEqual(res.KeylessSkipped, []string{"common.enc.yaml"}) {
		t.Fatalf("got %v", res.KeylessSkipped)
	}
	if !strings.Contains(res.Report(), "! no key: value checks skipped for common.enc.yaml") {
		t.Fatalf("got %q", res.Report())
	}
}

func TestKeylessStillFindsMissingAndUnencrypted(t *testing.T) {
	dir, b := build(t, decls, map[string]string{"common.enc.yaml": "TOKEN: plain\n"}, nil)
	b.Locked = true
	res := audit(t, dir, b)
	var got []string
	for _, f := range res.Findings {
		got = append(got, f.Name+":"+f.Why)
	}
	want := []string{"A:declared but no file sets it", "TOKEN:is in an encrypted file but its value is not encrypted"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}

func TestNoManifestIsAnError(t *testing.T) {
	_, err := Audit(t.TempDir(), &backend.Fake{})
	if err == nil || !strings.Contains(err.Error(), "has no manifest.yaml") {
		t.Fatalf("got %v", err)
	}
}

func TestInvalidManifestIsAnError(t *testing.T) {
	dir, b := build(t, "schemaVersion: v9\nrequired: {}\n", nil, nil)
	_, err := Audit(dir, b)
	if err == nil || !strings.Contains(err.Error(), "schemaVersion") {
		t.Fatalf("got %v", err)
	}
}

func TestUnreadableValuesFileIsAnError(t *testing.T) {
	dir, b := build(t, decls, map[string]string{"common.yaml": "A:\n  nested: 1\n"}, nil)
	_, err := Audit(dir, b)
	if err == nil || !strings.Contains(err.Error(), "common.yaml") {
		t.Fatalf("got %v", err)
	}
}

func TestOtherDecryptErrorIsReturned(t *testing.T) {
	dir, b := build(t, decls, map[string]string{"common.yaml": "A: x\n", "common.enc.yaml": "garbage line\n"}, nil)
	if _, err := Audit(dir, b); err == nil {
		t.Fatal("want an error")
	}
}

func TestFindingsAreSorted(t *testing.T) {
	dir, b := build(t, decls, map[string]string{"common.yaml": "Z: 1\nA: x\nM: 1\n", "env.prod.yaml": "M: 2\n"}, tok("s3"))
	res := audit(t, dir, b)
	var got []string
	for _, f := range res.Findings {
		got = append(got, f.Name+"@"+f.File)
	}
	want := []string{"M@common.yaml", "M@env.prod.yaml", "Z@common.yaml"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}

func TestFindingString(t *testing.T) {
	f := Finding{Name: "A", File: "common.yaml", Why: "w", Fix: "f"}
	if got := f.String(); got != "  A (common.yaml) — w\n      f" {
		t.Fatalf("got %q", got)
	}
	if got := (Finding{Name: "A", File: "common.yaml", Why: "w"}).String(); got != "  A (common.yaml) — w" {
		t.Fatalf("got %q", got)
	}
}
