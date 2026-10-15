// Package migrate converts a store or a repo from the layout of the tool ezc replaces (fsconfig, whose folder
// is .fs-config/) to ezc's, and proves that the resolved environment is the same before and after.
//
// It is the one place under cli/ allowed to name that tool. Nothing is written until the proof passes: the new
// .ezc is built in a temporary folder, the environment is composed by the old tool (the authority on what it
// composes) and by ezc's resolver over that folder, and only identical names and values let it into place.
package migrate

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/thinkjones/easy-config/cli/internal/backend"
	"github.com/thinkjones/easy-config/cli/internal/proc"
	"github.com/thinkjones/easy-config/cli/internal/recipients"
	"github.com/thinkjones/easy-config/cli/internal/resolve"
)

// OldTool is the command of the tool ezc replaces; the proof runs it to compose the environment as it stands.
const OldTool = "fsconfig"

// oldDir is the old tool's folder, in a store and in a repo.
const oldDir = ".fs-config"

// newDir is ezc's.
const newDir = ".ezc"

// Options is what Run needs from outside the package.
type Options struct {
	Dir             string // the folder holding .fs-config/
	Home            string
	Now             time.Time
	Environ         []string // the old tool's environment is built from it
	Exec            proc.Runner
	LookPath        func(string) (string, error)
	Backend         backend.Backend
	StoreEzc        string // the ezc store's .ezc, for a repo's proof
	SchemaURL       string
	KeyTextVariable string // the variable a CI workflow passed the key in
	Stdout          io.Writer
	DryRun          bool

	afterProof func() // tests: runs after the proof, before anything is written
}

// ErrNotProved is Run's error when the environment would change; the differences have been printed.
var ErrNotProved = errors.New("the environment would change")

// Run migrates the store or the repo in o.Dir. Any error but ErrNotProved has printed nothing about itself and
// is a sentence for the caller to print.
func Run(o Options) error {
	old := filepath.Join(o.Dir, oldDir)
	if st, err := os.Stat(old); err != nil || !st.IsDir() {
		return errors.New("Nothing to migrate here: no .fs-config/ folder.")
	}
	if _, err := os.Lstat(filepath.Join(o.Dir, newDir)); !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%s already has .ezc/. Nothing was changed.", o.Dir)
	}
	if isStore(old) {
		return runStore(o)
	}
	return runRepo(o)
}

// isStore reports whether an .fs-config/ folder is the old store's: it holds the global files or the registry.
func isStore(old string) bool {
	for _, name := range []string{"global.yaml", "global.enc.yaml", "recipients.yaml"} {
		if _, err := os.Lstat(filepath.Join(old, name)); err == nil {
			return true
		}
	}
	return false
}

func (o Options) say(format string, a ...any) { fmt.Fprintf(o.Stdout, format+"\n", a...) }

// runStore migrates the old store: .ezc/ is written beside .fs-config/, which the old tool still reads for
// every repo that has not moved yet.
func runStore(o Options) error {
	o.say("Migrating the store in %s", o.Dir)
	before, err := o.before("", true)
	if err != nil {
		return err
	}
	stage, err := stageStore(o)
	if stage.dir != "" {
		defer os.RemoveAll(stage.dir)
	}
	if err != nil {
		return err
	}
	after, err := o.after(resolve.Options{StoreDir: stage.dir}, stage.dir)
	if err != nil {
		return err
	}
	if !o.prove(storeLabel, before, after) {
		return o.notProved()
	}
	notes := stage.notes
	if o.DryRun {
		return o.dryRun(stage.files, notes)
	}
	if o.afterProof != nil {
		o.afterProof()
	}
	if err := o.place(stage); err != nil {
		return err
	}
	o.sayNotes(notes)
	o.say("Next: commit .ezc/ and push, then on each machine ezc setup --store <this repo>. Remove .fs-config/ once every repo has migrated.")
	return nil
}

// runRepo migrates a repo that loads its environment through the old tool: .ezc/ with the shim takes the place
// of .fs-config/, and mise.toml is rewritten to ezc. Each environment is proved over the ezc store.
func runRepo(o Options) error {
	if st, err := os.Stat(o.StoreEzc); o.StoreEzc == "" || err != nil || !st.IsDir() {
		return errors.New("Set up the ezc store first: run ezc migrate in the old store's folder, commit and push it, then ezc setup --store <it>.")
	}
	o.say("Migrating the repo in %s", o.Dir)
	envs, err := oldEnvs(filepath.Join(o.Dir, oldDir))
	if err != nil {
		return err
	}
	type proof struct {
		label  string
		before map[string]string
	}
	var proofs []proof
	if len(envs) == 0 {
		before, err := o.before("", false)
		if err != nil {
			return err
		}
		proofs = append(proofs, proof{storeLabel, before})
	}
	for _, env := range envs {
		before, err := o.before(env, false)
		if err != nil {
			return err
		}
		proofs = append(proofs, proof{env, before})
	}
	stage, err := stageRepo(o)
	if stage.dir != "" {
		defer os.RemoveAll(stage.dir)
	}
	if err != nil {
		return err
	}
	proved := true
	for _, p := range proofs {
		ro := resolve.Options{StoreDir: o.StoreEzc, RepoDir: stage.dir, Env: p.label}
		if p.label == storeLabel {
			ro = resolve.Options{StoreDir: o.StoreEzc}
		}
		after, err := o.after(ro, stage.dir)
		if err != nil {
			return err
		}
		proved = o.prove(p.label, p.before, after) && proved
	}
	if !proved {
		return o.notProved()
	}
	if len(envs) == 0 {
		stage.notes = append(stage.notes, "! with no environment named, ezc also loads this repo's common files; the old tool did not.")
	}
	mise := filepath.Join(o.Dir, "mise.toml")
	oldMise, hasMise, err := readMaybe(mise)
	if err != nil {
		return fmt.Errorf("could not read mise.toml (%s). Nothing was changed.", reason(err))
	}
	newMise := rewriteMise(oldMise)
	override := map[string]string{}
	if hasMise {
		override["mise.toml"] = newMise
	}
	stage.notes = append(stage.notes, oldNames(o.Dir, override)...)
	stage.notes = append(stage.notes, ciFindings(o.Dir, o.KeyTextVariable, botsOf(o.StoreEzc))...)
	if o.DryRun {
		if hasMise && newMise != oldMise {
			stage.notes = append([]string{"would rewrite mise.toml"}, stage.notes...)
		}
		return o.dryRun(stage.files, stage.notes)
	}
	if o.afterProof != nil {
		o.afterProof()
	}
	// Read again: what is written must be what was there, not what was read before the proof.
	if oldMise, hasMise, err = readMaybe(mise); err != nil {
		return fmt.Errorf("could not read mise.toml (%s). Nothing was changed.", reason(err))
	}
	newMise = rewriteMise(oldMise)
	if err := o.place(stage); err != nil {
		return err
	}
	if hasMise && newMise != oldMise {
		if err := replaceFile(mise, newMise); err != nil {
			os.RemoveAll(filepath.Join(o.Dir, newDir))
			return fmt.Errorf("could not write mise.toml (%s). Nothing was changed.", reason(err))
		}
		o.say("+ mise.toml rewritten for ezc")
	}
	if err := os.RemoveAll(filepath.Join(o.Dir, oldDir)); err != nil {
		o.say("! could not remove .fs-config/ (%s); remove it yourself.", reason(err))
	} else {
		o.say("- .fs-config/ removed")
	}
	o.sayNotes(stage.notes)
	o.say("Next: ezc repo add, commit .ezc/ and mise.toml, and run ezc doctor.")
	return nil
}

// oldEnvs are the environments a repo's .fs-config/ names, from its env.<name>.yaml files, sorted.
func oldEnvs(old string) ([]string, error) {
	entries, err := os.ReadDir(old)
	if err != nil {
		return nil, fmt.Errorf("could not read .fs-config/ (%s).", reason(err))
	}
	var envs []string
	for _, e := range entries {
		name, ok := strings.CutPrefix(e.Name(), "env.")
		if name, ok2 := strings.CutSuffix(name, ".yaml"); ok && ok2 && !strings.HasSuffix(name, ".enc") {
			envs = append(envs, name)
		}
	}
	sort.Strings(envs)
	return envs, nil
}

// botsOf maps each bot of the store's registry to its repos; none when the registry cannot be read.
func botsOf(storeEzc string) map[string][]string {
	reg, _, err := recipients.Load(storeEzc)
	if err != nil {
		return nil
	}
	bots := map[string][]string{}
	for name, r := range reg.Recipients {
		if r.Kind == "bot" {
			bots[name] = r.Repos
		}
	}
	return bots
}

// readMaybe is a file's text, and false with no error when it does not exist.
func readMaybe(path string) (string, bool, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	return string(b), err == nil, err
}

// replaceFile writes text over path through a temporary file and a rename, keeping path's mode.
func replaceFile(path, text string) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".mise.toml.ezc-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(text); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), st.Mode().Perm()); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func (o Options) notProved() error {
	o.say("The environment would change. Nothing was changed.")
	return ErrNotProved
}

func (o Options) sayNotes(notes []string) {
	for _, n := range notes {
		o.say("%s", n)
	}
}

func (o Options) dryRun(files, notes []string) error {
	for _, f := range files {
		o.say("would write .ezc/%s", f)
	}
	o.sayNotes(notes)
	o.say("Dry run: nothing was written.")
	return nil
}

// place copies the staged files into <Dir>/.ezc, which must not exist yet; on a failure it removes what it made.
func (o Options) place(s staged) error {
	dst := filepath.Join(o.Dir, newDir)
	if err := os.Mkdir(dst, 0o755); err != nil {
		return fmt.Errorf("could not write .ezc/ (%s). Nothing was changed.", reason(err))
	}
	for _, f := range s.files {
		if err := copyFile(filepath.Join(s.dir, f), filepath.Join(dst, f)); err != nil {
			os.RemoveAll(dst)
			return fmt.Errorf("could not write .ezc/ (%s). Nothing was changed.", reason(err))
		}
	}
	for _, f := range s.files {
		o.say("+ .ezc/%s", f)
	}
	return nil
}

// reason is the part of a file-system error that is not a path.
func reason(err error) string {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err.Error()
	}
	var le *os.LinkError
	if errors.As(err, &le) {
		return le.Err.Error()
	}
	return "it failed"
}
