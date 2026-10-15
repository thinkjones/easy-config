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
	"time"

	"github.com/thinkjones/easy-config/cli/internal/backend"
	"github.com/thinkjones/easy-config/cli/internal/proc"
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
	after, err := o.afterStore(stage.dir)
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

// runRepo migrates a repo that loads its environment through the old tool.
func runRepo(o Options) error {
	return errors.New("migrating a repo is not built yet")
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
