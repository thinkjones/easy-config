package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/thinkjones/easy-config/cli/internal/dotenv"
	"github.com/thinkjones/easy-config/cli/internal/recipients"
	"github.com/thinkjones/easy-config/cli/internal/resolve"
)

func newImportCmd(sys System) *cobra.Command {
	var global bool
	cmd := &cobra.Command{
		Use:   "import ENV FILE",
		Short: "set an environment's values from a .env file",
		Long: `Set every name in FILE, a .env file, in the repo's env.ENV.yaml, or env.ENV.enc.yaml for a name the manifest declares a secret. ENV common means common.yaml and common.enc.yaml, which every environment loads. With -g the store's files are used instead.

Every name must already be declared in the scope's manifest, and every value is checked as ` + "`ezc set`" + ` checks it, before anything is written: one problem and nothing is imported. Values are stored literally, as ` + "`ezc set`" + ` stores them without --expand. EZC_ENV and APP_ENV name the environment and are left out, and so is a name with an empty value (NAME=), which a .env file uses for not set.

FILE takes NAME=VALUE lines, # comments, an optional export, 'literal' and "double-quoted" values (\n, \t, \", \\ and \$; they may span lines). A name given twice takes its last value. Values are never printed; an error names the line.`,
		Args: exactArgs(2, "an environment name and a file"),
		RunE: func(_ *cobra.Command, args []string) error {
			return importEnv(sys, global, args[0], args[1])
		},
	}
	cmd.Flags().BoolVarP(&global, "global", "g", false, "write to the global store")
	return cmd
}

// importEnv is `ezc env import`: everything is checked, then every value is written, then the store commits once.
func importEnv(sys System, global bool, env, file string) error {
	if err := checkEnvName("env import", env); err != nil {
		return err
	}
	if env == "" {
		return usagef("ezc env import: give an environment name, or common.")
	}
	if env == "common" {
		env = "" // the common files, not env.common.yaml
	}
	sc, err := scopeFor(sys, global)
	if err != nil {
		return err
	}
	m, err := scopeManifest(sc)
	if err != nil {
		return err
	}
	src, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	entries, err := dotenv.Parse(src)
	if err != nil {
		return nothingImported(fmt.Errorf("the file could not be read as a .env file (%v)", err))
	}

	var kept []dotenv.Entry
	var undeclared, empty []string
	for _, e := range entries {
		switch _, ok := m.Lookup(e.Name); {
		case slices.Contains(resolve.SelectorVars, e.Name):
			fmt.Fprintf(sys.Stderr, "! %s names the environment and was not imported.\n", e.Name)
		case e.Value == "": // NAME= in a .env file means not set
			empty = append(empty, e.Name)
		case !ok:
			undeclared = append(undeclared, e.Name)
		default:
			kept = append(kept, e)
		}
	}
	switch len(empty) {
	case 0:
	case 1:
		fmt.Fprintf(sys.Stderr, "! %s is empty in the file and was not imported.\n", empty[0])
	default:
		fmt.Fprintf(sys.Stderr, "! %s and %s are empty in the file and were not imported.\n", strings.Join(empty[:len(empty)-1], ", "), empty[len(empty)-1])
	}
	if len(undeclared) > 0 {
		slices.Sort(undeclared)
		return nothingImported(fmt.Errorf("the file has names %s does not declare: %s. Declare them (ezc set NAME --type T --desc D, or edit the manifest) or take them out of the file.", sc.show(manifestName), strings.Join(undeclared, ", ")))
	}
	if len(kept) == 0 {
		return nothingImported(fmt.Errorf("the file has no values to import."))
	}

	be := backendFor(sys)
	base := valuesBase(env)
	secret := valuesFile(base, true)
	var watched []string
	for _, e := range kept {
		v, _ := m.Lookup(e.Name)
		if err := checkValue(e.Name, v, e.Value, false, "Take it out of the file and run ezc set --expand."); err != nil {
			return nothingImported(err)
		}
		file, other := valuesFile(base, v.IsSecret()), valuesFile(base, !v.IsSecret())
		if in, err := holds(be, filepath.Join(sc.dir, other), e.Name); err != nil {
			return nothingImported(err)
		} else if in {
			return nothingImported(fmt.Errorf("%s is already in %s. Moving a value across the secret boundary means it was (or will be) stored in plaintext: remove it with `ezc unset`, and give a secret a new value.", e.Name, sc.show(other)))
		}
		if !slices.Contains(watched, file) {
			watched = append(watched, file)
		}
	}
	slices.Sort(watched) // env.ENV.enc.yaml before env.ENV.yaml, as the commit names them
	hasSecret := slices.Contains(watched, secret)
	if hasSecret {
		if ok, err := be.HasPolicy(sc.dir); err != nil {
			return nothingImported(err)
		} else if !ok {
			return nothingImported(noPolicy(sc))
		}
	}
	// A secret that is due for a new value stops being due when it gets one, in the same commit.
	var due recipients.Due
	var clear []string
	if hasSecret && sc.store {
		if due, err = recipients.LoadDue(sc.dir); err != nil {
			return nothingImported(err)
		}
		for _, e := range kept {
			if _, found := due[secret+"/"+e.Name]; found {
				clear = append(clear, e.Name)
			}
		}
		if len(clear) > 0 {
			watched = append(watched, recipients.DueFile)
		}
	}
	st, err := sc.guard(sys, watched...)
	if err != nil {
		return err
	}
	if hasSecret {
		if err := sc.guardNewFile(sys, secret); err != nil {
			return err
		}
	}

	before := fileStates(sc.dir, watched)
	undo := func(err error) error {
		if rerr := putBack(sc.dir, watched, before); rerr != nil {
			return fmt.Errorf("%w\n       %s could not be put back as it was; look at %s", err, sc.dir, strings.Join(changedFiles(sc.dir, watched, before), ", "))
		}
		return nothingImported(err)
	}
	var lines []string
	for _, e := range kept {
		v, _ := m.Lookup(e.Name)
		file := valuesFile(base, v.IsSecret())
		if err := writeValue(be, sc, filepath.Join(sc.dir, file), e.Name, e.Value, v.IsSecret(), false); err != nil {
			return undo(err)
		}
		lines = append(lines, fmt.Sprintf("set %s in %s\n", e.Name, sc.show(file)))
	}
	if len(clear) > 0 {
		for _, name := range clear {
			due.Clear(secret, name)
		}
		if err := due.Save(sc.dir); err != nil {
			return undo(err)
		}
	}
	for _, l := range lines {
		fmt.Fprint(sys.Stdout, l)
	}
	return sc.commit(sys, st, "import "+base, watched, before)
}

// nothingImported ends err with the sentence that says no file was changed.
func nothingImported(err error) error {
	if strings.HasSuffix(err.Error(), ".") {
		return fmt.Errorf("%w Nothing was imported.", err)
	}
	return fmt.Errorf("%w. Nothing was imported.", err)
}
