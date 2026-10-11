package cli

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/thinkjones/easy-config/cli/internal/backend"
	"github.com/thinkjones/easy-config/cli/internal/check"
	"github.com/thinkjones/easy-config/cli/internal/manifest"
	"github.com/thinkjones/easy-config/cli/internal/resolve"
	"github.com/thinkjones/easy-config/cli/internal/valuefile"
)

// scope is the .ezc folder a command writes to: the repo's, or with -g the store's.
type scope struct {
	layer string // "repo" or "store"
	dir   string
}

func (s scope) manifest() string        { return filepath.Join(s.dir, manifestName) }
func (s scope) show(file string) string { return s.layer + "/" + file }

// scopeFor is the repo's .ezc folder, or with global the store's, which must exist.
func scopeFor(sys System, global bool) (scope, error) {
	if !global {
		dir, err := requireRepoDir(sys)
		return scope{"repo", dir}, err
	}
	p, err := storePaths(sys)
	if err != nil {
		return scope{}, err
	}
	if info, err := os.Stat(p.StoreEzc()); err != nil || !info.IsDir() {
		return scope{}, fmt.Errorf("no store at %s. `ezc setup` creates one.", p.Store())
	}
	return scope{"store", p.StoreEzc()}, nil
}

// valuesBase is "common", or "env.<env>" when an environment is named.
func valuesBase(env string) string {
	if env == "" {
		return "common"
	}
	return "env." + env
}

// valuesFile is the file for a base: encrypted for a secret, plaintext otherwise.
func valuesFile(base string, encrypted bool) string {
	if encrypted {
		return base + ".enc.yaml"
	}
	return base + ".yaml"
}

// checkEnvName is a usage error when env is given and cannot name an environment.
func checkEnvName(cmd string, env string) error {
	switch {
	case env == "" || resolve.ValidEnvName(env):
		return nil
	case env == resolve.ReservedEnv:
		return usagef("ezc %s: %s is a reserved name; it marks encrypted files (env.<name>.enc.yaml).", cmd, env)
	}
	return usagef("ezc %s: %q is not a valid environment name; use letters, digits, - and _.", cmd, env)
}

// holds reports whether the values file at path has name. A file that does not exist holds nothing.
func holds(be backend.Backend, path, name string) (bool, error) {
	if resolve.IsEncrypted(path) {
		if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		names, _, err := be.Inspect(path)
		return slices.Contains(names, name), err
	}
	pairs, _, err := resolve.Plain(path)
	return slices.ContainsFunc(pairs, func(p backend.Pair) bool { return p.Name == name }), err
}

// declaration is the manifest in dir's scope; it must exist.
func scopeManifest(s scope) (manifest.Manifest, error) {
	m, found, err := manifestIfPresent(s.manifest())
	if err == nil && !found {
		hint := ""
		if s.layer == "repo" {
			hint = " Run `ezc repo init` to create one."
		}
		err = fmt.Errorf("%s has no %s.%s", s.dir, manifestName, hint)
	}
	return m, err
}

func newSetCmd(sys System) *cobra.Command {
	var (
		global, optional bool
		env, typ, desc   string
		allowed          []string
		lo, hi           int
	)
	cmd := &cobra.Command{
		Use:   "set NAME [VALUE]",
		Short: "set a variable's value, declaring it when it is new",
		Long: `Set NAME in the repo's common.yaml, or env.ENV.yaml with --env. With -g the store's files are used instead.

A secret is stored encrypted, in common.enc.yaml or env.ENV.enc.yaml, and is never given on the command line: run ` + "`ezc set NAME`" + ` and type it, or pipe it on stdin. The same goes for any variable when VALUE is left out.

A name the scope's manifest does not declare is declared by this command, as required unless --optional is given: --type and --desc are then needed, and --values, --min and --max when the type calls for them.`,
		Args: func(cmd *cobra.Command, args []string) error {
			if err := minArgs(1, "a variable name")(cmd, args); err != nil {
				return err
			}
			return maxArgs(2, "a name and at most one value")(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			flags := cmd.Flags()
			name := args[0]
			if !manifest.ValidName(name) {
				return usagef("ezc set: %q is not a valid variable name; use letters, digits and underscores, and do not start with a digit.", name)
			}
			if slices.Contains(resolve.SelectorVars, name) {
				return usagef("ezc set: %s names the environment and cannot be stored.", name)
			}
			if err := checkEnvName("set", env); err != nil {
				return err
			}
			sc, err := scopeFor(sys, global)
			if err != nil {
				return err
			}
			m, err := scopeManifest(sc)
			if err != nil {
				return err
			}

			v, declared := m.Lookup(name)
			if declared {
				for _, f := range []string{"desc", "optional", "values", "min", "max"} {
					if flags.Changed(f) {
						return usagef("ezc set: %s is already declared; edit %s to change its declaration.", name, sc.manifest())
					}
				}
				if typ != "" && typ != string(v.Type) {
					return fmt.Errorf("%s is declared %s in %s; --type %s contradicts it.", name, v.Type, sc.show(manifestName), typ)
				}
			} else {
				if typ == "" || desc == "" {
					return usagef("ezc set: %s is not declared. Add --type and --desc to declare it.", name)
				}
				v = manifest.Var{Type: manifest.VarType(typ), Desc: desc, Values: allowed}
				if flags.Changed("min") {
					v.Min = &lo
				}
				if flags.Changed("max") {
					v.Max = &hi
				}
				if err := validDeclaration(m, name, v, optional); err != nil {
					return usagef("ezc set: %v", err)
				}
			}

			value, err := valueToSet(sys, name, v.IsSecret(), args[1:])
			if err != nil {
				return err
			}
			if strings.ContainsRune(value, 0) {
				return fmt.Errorf("%s: contains a NUL byte, which an environment variable cannot hold", name)
			}
			switch {
			case strings.TrimSpace(value) == "":
				return fmt.Errorf("%s: empty", name)
			case check.IsPlaceholder(value):
				return fmt.Errorf("%s: placeholder, not a value", name)
			}
			if why := check.ValidateValue(v, value); why != "" {
				return fmt.Errorf("%s: %s", name, why)
			}

			be := backendFor(sys)
			base := valuesBase(env)
			file := valuesFile(base, v.IsSecret())
			other := valuesFile(base, !v.IsSecret())
			if in, err := holds(be, filepath.Join(sc.dir, other), name); err != nil {
				return err
			} else if in {
				return fmt.Errorf("%s is already in %s. Moving a value across the secret boundary means it was (or will be) stored in plaintext: remove it with `ezc unset`, and give a secret a new value.", name, sc.show(other))
			}

			path := filepath.Join(sc.dir, file)
			undo, err := writeValue(be, path, name, value, v.IsSecret())
			if err != nil {
				return err
			}
			if !declared {
				if err := valuefile.Declare(sc.manifest(), name, v, optional); err != nil {
					if uerr := undo(); uerr != nil {
						return fmt.Errorf("%v. The value is still in %s; remove it with `ezc unset`, and declare %s in %s", err, path, name, sc.manifest())
					}
					return err
				}
			}
			fmt.Fprintf(sys.Stdout, "set %s in %s\n", name, sc.show(file))
			if !declared {
				fmt.Fprintf(sys.Stdout, "declared %s (%s) in %s\n", name, v.Type, sc.show(manifestName))
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.BoolVarP(&global, "global", "g", false, "write to the global store")
	f.StringVar(&env, "env", "", "write the environment's file, not the common one")
	f.StringVar(&typ, "type", "", "the type to declare a new name with: "+typeList())
	f.StringVar(&desc, "desc", "", "the description to declare a new name with")
	f.BoolVar(&optional, "optional", false, "declare a new name optional, not required")
	f.StringSliceVar(&allowed, "values", nil, "the allowed values of a new enum, comma-separated")
	f.IntVar(&lo, "min", 0, "the smallest value of a new int")
	f.IntVar(&hi, "max", 0, "the largest value of a new int")
	return cmd
}

// typeList is the manifest's types, comma-separated.
func typeList() string {
	var names []string
	for _, t := range manifest.AllTypes() {
		names = append(names, string(t))
	}
	return strings.Join(names, ", ")
}

// validDeclaration is an error when m with name declared as v would not validate.
func validDeclaration(m manifest.Manifest, name string, v manifest.Var, optional bool) error {
	next := manifest.Manifest{SchemaVersion: m.SchemaVersion, Required: map[string]manifest.Var{}, Optional: map[string]manifest.Var{}}
	for k, x := range m.Required {
		next.Required[k] = x
	}
	for k, x := range m.Optional {
		next.Optional[k] = x
	}
	if optional {
		next.Optional[name] = v
	} else {
		next.Required[name] = v
	}
	if errs := next.Validate(); len(errs) > 0 {
		return errs[0]
	}
	return nil
}

// valueToSet is the value on the command line, else what the user types or pipes in.
// A secret is never taken from the command line.
func valueToSet(sys System, name string, secret bool, args []string) (string, error) {
	if len(args) == 1 {
		if secret {
			return "", usagef("ezc set: a secret is never given on the command line. Run `ezc set %s` and type it, or pipe it on stdin.", name)
		}
		return args[0], nil
	}
	if sys.StdinIsTerminal() {
		return sys.ReadSecret("Value for " + name + ": ")
	}
	b, err := io.ReadAll(sys.Stdin)
	if err != nil {
		return "", fmt.Errorf("reading stdin: %w", err)
	}
	value := string(b)
	if rest, ok := strings.CutSuffix(value, "\n"); ok {
		value = strings.TrimSuffix(rest, "\r")
	}
	return value, nil
}

// writeValue stores the value, plaintext or through the backend, and returns a function that puts the file back
// as it was before.
func writeValue(be backend.Backend, path, name, value string, secret bool) (undo func() error, err error) {
	had, err := holds(be, path, name)
	if err != nil {
		return nil, err
	}
	if secret {
		if err := be.Set(path, name, value); err != nil {
			if errors.Is(err, backend.ErrNoPolicy) {
				err = fmt.Errorf("%w See \"Encryption\" in the README.", err)
			}
			return nil, err
		}
		return func() error {
			if had {
				return fmt.Errorf("%s held %s before", path, name)
			}
			return be.Unset(path, name)
		}, nil
	}
	var prev string
	if pairs, _, err := resolve.Plain(path); err == nil {
		for _, p := range pairs {
			if p.Name == name {
				prev = p.Value
			}
		}
	}
	if err := valuefile.Set(path, name, value); err != nil {
		return nil, err
	}
	return func() error {
		if had {
			return valuefile.Set(path, name, prev)
		}
		_, err := valuefile.Unset(path, name)
		return err
	}, nil
}

func newUnsetCmd(sys System) *cobra.Command {
	var (
		global bool
		env    string
	)
	cmd := &cobra.Command{
		Use:   "unset NAME",
		Short: "remove a variable's value, and its declaration when no file holds it any more",
		Long:  `Remove NAME from the repo's common.yaml, or env.ENV.yaml with --env; a secret is removed from the encrypted file. With -g the store's files are used instead. When no values file in the scope holds NAME any more, its declaration in the scope's manifest is removed too.`,
		Args:  exactArgs(1, "a variable name"),
		RunE: func(_ *cobra.Command, args []string) error {
			name := args[0]
			if err := checkEnvName("unset", env); err != nil {
				return err
			}
			sc, err := scopeFor(sys, global)
			if err != nil {
				return err
			}
			m, _, err := manifestIfPresent(sc.manifest())
			if err != nil {
				return err
			}
			be := backendFor(sys)
			base := valuesBase(env)
			v, declared := m.Lookup(name)
			files := []string{valuesFile(base, false), valuesFile(base, true)}
			if declared {
				files = []string{valuesFile(base, v.IsSecret())}
			}

			var removedFrom []string
			for _, file := range files {
				path := filepath.Join(sc.dir, file)
				in, err := holds(be, path, name)
				if err != nil {
					return err
				}
				if !in {
					continue
				}
				if resolve.IsEncrypted(file) {
					err = be.Unset(path, name)
				} else {
					_, err = valuefile.Unset(path, name)
				}
				if err != nil {
					return err
				}
				removedFrom = append(removedFrom, file)
			}
			if len(removedFrom) == 0 {
				shown := make([]string, len(files))
				for i, f := range files {
					shown[i] = sc.show(f)
				}
				return fmt.Errorf("%s is not set in %s.", name, strings.Join(shown, " or "))
			}
			for _, file := range removedFrom {
				fmt.Fprintf(sys.Stdout, "unset %s from %s\n", name, sc.show(file))
			}
			if !declared {
				return nil
			}
			still, err := heldInScope(be, sc.dir, name)
			if err != nil || still {
				return err
			}
			if gone, err := valuefile.Undeclare(sc.manifest(), name); err != nil {
				return err
			} else if gone {
				fmt.Fprintf(sys.Stdout, "removed %s from %s\n", name, sc.show(manifestName))
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&global, "global", "g", false, "remove from the global store")
	cmd.Flags().StringVar(&env, "env", "", "remove from the environment's file, not the common one")
	return cmd
}

// heldInScope reports whether any values file in dir (common and every environment, plain or encrypted) has name.
func heldInScope(be backend.Backend, dir, name string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		n := e.Name()
		isValues := n == "common.yaml" || n == "common.enc.yaml" ||
			(strings.HasPrefix(n, "env.") && strings.HasSuffix(n, ".yaml") && n != "env."+resolve.ReservedEnv+".yaml")
		if !isValues || e.IsDir() {
			continue
		}
		if in, err := holds(be, filepath.Join(dir, n), name); err != nil || in {
			return in, err
		}
	}
	return false, nil
}
