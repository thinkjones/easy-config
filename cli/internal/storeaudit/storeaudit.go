// Package storeaudit audits a value store against its own manifest.
//
// This is the inverse of package check, and the difference is the whole point. `check` validates a
// repo's ENVIRONMENT: it walks the manifest and asks "is each declared variable present and usable",
// ignoring anything the store carries that this repo does not want. That is right for a consumer,
// which must not care about values belonging to other repos.
//
// A store is the other direction. It is a catalogue, so it has to be complete in BOTH directions:
// every value in the files must be declared, and every required declaration must correspond to a
// value. Walking only the manifest, the way `check` does, would let an undocumented value be
// committed silently, which is exactly what a commit gate exists to stop.
//
// It also enforces the first rule declaratively. `type: secret` means the value lives in *.enc.yaml;
// anything else lives in a plaintext *.yaml. The manifest says which is which by name, so the audit
// knows, instead of guessing from whether the name happens to contain "TOKEN" or "KEY".
//
// Nothing here ever returns a value. A Finding names a variable and says what is wrong with it.
package storeaudit

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/thinkjones/easy-config/cli/internal/backend"
	"github.com/thinkjones/easy-config/cli/internal/check"
	"github.com/thinkjones/easy-config/cli/internal/manifest"
	"github.com/thinkjones/easy-config/cli/internal/resolve"
)

// ManifestName is the store's catalogue, beside the values it describes.
const ManifestName = "manifest.yaml"

// Finding is one problem: a variable, what is wrong, and what to do. Never a value. File is empty
// for a problem that belongs to no one file.
type Finding struct{ Name, File, Why, Fix string }

// String is the finding as it appears in a report: a line, and the fix indented under it when there is one.
func (f Finding) String() string {
	s := "  " + f.Name
	if f.File != "" {
		s += " (" + f.File + ")"
	}
	s += " — " + f.Why
	if f.Fix != "" {
		s += "\n      " + f.Fix
	}
	return s
}

// Result is the audit outcome.
type Result struct {
	Dir      string
	Files    []string // values files examined, base names, in precedence order
	Declared int
	Findings []Finding // sorted by Name, then File
	// KeylessSkipped is the encrypted files whose values could not be checked for want of a key.
	KeylessSkipped []string
	// Unreadable is the encrypted files holding a value that is not encrypted: the backend cannot read
	// such a file, so their values were not checked.
	Unreadable []string
}

func (r Result) OK() bool { return len(r.Findings) == 0 }

// Report is the outcome as text. The counts come first, so a passing run still says how much it covered.
func (r Result) Report() string {
	var b strings.Builder
	fmt.Fprintf(&b, "store %s: %d declared, %d files\n", r.Dir, r.Declared, len(r.Files))
	if r.OK() {
		b.WriteString("✓ nothing to fix\n")
	}
	for _, f := range r.Findings {
		b.WriteString(f.String() + "\n")
	}
	if len(r.KeylessSkipped) > 0 {
		fmt.Fprintf(&b, "! no key: value checks skipped for %s\n", strings.Join(r.KeylessSkipped, ", "))
	}
	if len(r.Unreadable) > 0 {
		fmt.Fprintf(&b, "! damaged: value checks skipped for %s\n", strings.Join(r.Unreadable, ", "))
	}
	return b.String()
}

// occurrence is one name as found in one values file.
type occurrence struct {
	file, env string
	encrypted bool
	unencoded bool   // in an encrypted file, but its value is not encrypted
	value     string // meaningful only when known
	known     bool
}

// Audit checks the store in dir (a .ezc folder) against its manifest. It returns an error only when
// the audit could not run: no manifest or an invalid one, a values file that cannot be read, or a
// backend failure other than a missing key on a file whose values are all encrypted. Everything else is a Finding. It never returns early on
// a finding: a store that has drifted should produce one list to work through.
func Audit(dir string, b backend.Backend) (Result, error) {
	res := Result{Dir: dir}
	m, err := loadManifest(dir)
	if err != nil {
		return res, err
	}
	res.Declared = len(m.Required) + len(m.Optional)

	envs, err := resolve.Envs(dir)
	if err != nil {
		return res, err
	}
	bases := []struct{ base, env string }{{"common", ""}}
	for _, e := range envs {
		bases = append(bases, struct{ base, env string }{"env." + e, e})
	}

	var found []struct {
		name string
		occ  occurrence
	}
	for _, bs := range bases {
		for _, enc := range []bool{false, true} {
			file := bs.base + ".yaml"
			if enc {
				file = bs.base + ".enc.yaml"
			}
			path := filepath.Join(dir, file)
			if _, err := os.Stat(path); err != nil {
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				return res, fmt.Errorf("%s: %w", path, err)
			}
			res.Files = append(res.Files, file)
			var occs map[string]occurrence
			var order []string
			if enc {
				occs, order, err = encryptedOccurrences(path, file, bs.env, b, &res)
			} else {
				occs, order, err = plainOccurrences(path, file, bs.env)
			}
			if err != nil {
				return res, err
			}
			for _, n := range order {
				found = append(found, struct {
					name string
					occ  occurrence
				}{n, occs[n]})
			}
		}
	}

	set := map[string]bool{}
	for _, f := range found {
		if !slices.Contains(resolve.SelectorVars, f.name) {
			set[f.name] = true
		}
		res.Findings = append(res.Findings, judge(m, f.name, f.occ)...)
	}
	res.Findings = append(res.Findings, missing(m, set)...)

	sort.SliceStable(res.Findings, func(i, j int) bool {
		a, b := res.Findings[i], res.Findings[j]
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.File < b.File
	})
	return res, nil
}

// loadManifest reads and validates the store's manifest; the error lists every problem.
func loadManifest(dir string) (manifest.Manifest, error) {
	path := filepath.Join(dir, ManifestName)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return manifest.Manifest{}, fmt.Errorf("%s has no %s.", dir, ManifestName)
	}
	m, err := manifest.Load(path)
	if err != nil {
		return m, err
	}
	if errs := m.Validate(); len(errs) > 0 {
		var b strings.Builder
		fmt.Fprintf(&b, "%s is invalid:", path)
		for _, e := range errs {
			fmt.Fprintf(&b, "\n  %v", e)
		}
		return m, errors.New(b.String())
	}
	return m, nil
}

// plainOccurrences reads a plaintext file as written. Its values are final only when they hold no
// reference, so a value with a $ is left unchecked for shape.
func plainOccurrences(path, file, env string) (map[string]occurrence, []string, error) {
	pairs, _, err := resolve.Plain(path)
	if err != nil {
		return nil, nil, err
	}
	occs := map[string]occurrence{}
	var order []string
	for _, p := range pairs {
		occs[p.Name] = occurrence{file: file, env: env, value: p.Value, known: true}
		order = append(order, p.Name)
	}
	return occs, order, nil
}

// encryptedOccurrences reads an encrypted file.
//
// Names are read first, without a key. They are plaintext inside an encrypted file, so completeness
// and the boundary rule are checkable without one, and this is the only way to catch a value written
// back unencrypted, which is how a fail-open encryption rule leaks. Decrypting then makes the value
// checks possible; with no key that is skipped, and said so, rather than reported as a clean run.
// A file holding an unencrypted value is not decrypted at all: the backend refuses the whole file,
// and that refusal must not cost the finding.
func encryptedOccurrences(path, file, env string, b backend.Backend, res *Result) (map[string]occurrence, []string, error) {
	names, unencrypted, err := b.Inspect(path)
	if err != nil {
		return nil, nil, err
	}
	values := map[string]string{}
	if len(unencrypted) > 0 {
		res.Unreadable = append(res.Unreadable, file)
	} else {
		pairs, err := b.Decrypt(path)
		switch {
		case errors.Is(err, backend.ErrNoKey):
			res.KeylessSkipped = append(res.KeylessSkipped, file)
			pairs = nil
		case err != nil:
			return nil, nil, err
		}
		for _, p := range pairs {
			values[p.Name] = p.Value
		}
	}
	occs := map[string]occurrence{}
	for _, n := range names {
		v, known := values[n]
		occs[n] = occurrence{file: file, env: env, encrypted: true, unencoded: slices.Contains(unencrypted, n), value: v, known: known}
	}
	return occs, names, nil
}

// flag is how a command names the env of a file.
func (o occurrence) flag() string {
	if o.env == "" {
		return ""
	}
	return " --env " + o.env
}

// judge applies the per-occurrence rules: a leak, a selector name, undeclared, the secret boundary,
// and the shape of the value. Every file that sets a declared name is judged, and reported with that file.
func judge(m manifest.Manifest, name string, o occurrence) []Finding {
	var out []Finding
	add := func(why, fix string) { out = append(out, Finding{Name: name, File: o.file, Why: why, Fix: fix}) }
	fl := o.flag()

	if o.unencoded {
		add("is in an encrypted file but its value is not encrypted",
			fmt.Sprintf("The file was changed by hand and can no longer be decrypted. Restore %s from git, then set the value with `ezc set -g %s%s`; treat the value as leaked.", o.file, name, fl))
	}
	if slices.Contains(resolve.SelectorVars, name) {
		add("names the environment and cannot be stored", "")
		return out
	}
	d, ok := m.Lookup(name)
	if !ok {
		add("not declared in "+ManifestName,
			fmt.Sprintf("Declare it with a type and a desc, or remove it with `ezc unset -g %s%s`.", name, fl))
		return out
	}

	switch {
	case d.Type == manifest.TypeSecret && !o.encrypted:
		add("is a secret but is in plaintext",
			fmt.Sprintf("It is in git history in plaintext: remove it with `ezc unset -g %s%s`, then give it a new value with `ezc set -g %s%s`.", name, fl, name, fl))
		return out
	case d.Type != manifest.TypeSecret && o.encrypted:
		add("is not a secret but is in an encrypted file",
			fmt.Sprintf("Declare it secret, or move it: `ezc unset -g %s%s`, then `ezc set -g %s%s`.", name, fl, name, fl))
	}

	if !o.known {
		return out
	}
	val := strings.TrimSpace(o.value)
	switch {
	case check.IsPlaceholder(val):
		// Never echo the value: for a secret that would print the secret.
		add("is still a placeholder", "")
	case !o.encrypted && strings.ContainsRune(val, '$'):
		// A reference: the final text is not known here.
	default:
		if why := check.ValidateValue(d, val); why != "" {
			add(why, "")
		}
	}
	return out
}

// missing is the other direction: a required declaration with nothing behind it in any file.
func missing(m manifest.Manifest, set map[string]bool) []Finding {
	var out []Finding
	for _, n := range manifest.SortedNames(m.Required) {
		if !set[n] {
			out = append(out, Finding{Name: n, Why: "declared but no file sets it",
				Fix: fmt.Sprintf("Set it with `ezc set -g %s`, or remove the declaration with `ezc unset -g %s --undeclare`.", n, n)})
		}
	}
	return out
}
