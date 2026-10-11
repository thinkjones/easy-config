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
	"unicode/utf8"

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
	return usagef("ezc %s: that is not a valid environment name; use letters, digits, - and _.", cmd)
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

// scopeManifest is the manifest in s; it must exist.
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
		expand           bool
		env, typ, desc   string
		allowed          []string
		lo, hi           int
	)
	cmd := &cobra.Command{
		Use:   "set NAME [VALUE]",
		Short: "set a variable's value, declaring it when it is new",
		Long: `Set NAME in the repo's common.yaml, or env.ENV.yaml with --env. With -g the store's files are used instead.

A secret is stored encrypted, in common.enc.yaml or env.ENV.enc.yaml, and is never given on the command line: run ` + "`ezc set NAME`" + ` and type it, or pipe it on stdin. The same goes for any variable when VALUE is left out.

The value is stored literally: what you give is what loads, so a $ is written as $$ in the file. With --expand it is written as given, so that $NAME, ${NAME} and a leading ~ keep their meaning when the file is loaded; the environment is then resolved once, and a reference to a name that is not defined puts the file back as it was. A literal value cannot start with ~ or ~/, which is always expanded.

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
				return usagef("ezc set: %s", badName(name))
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
					return fmt.Errorf("%s is declared %s in %s; a different --type contradicts it.", name, v.Type, sc.show(manifestName))
				}
			} else {
				if typ == "" || desc == "" {
					return usagef("ezc set: %s is not declared. Add --type and --desc to declare it.", name)
				}
				if !slices.Contains(manifest.AllTypes(), manifest.VarType(typ)) {
					return usagef("ezc set: unknown --type; use %s.", typeList())
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

			if expand && v.IsSecret() {
				return usagef("ezc set: --expand does not apply to a secret; secrets are stored exactly as given.")
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
			if v.IsSecret() {
				if ok, err := be.HasPolicy(sc.dir); err != nil {
					return err
				} else if !ok {
					return fmt.Errorf("%s: %w. See \"Encryption\" in the README.", sc.dir, backend.ErrNoPolicy)
				}
			}

			value, err := valueToSet(sys, name, v.IsSecret(), args[1:])
			if err != nil {
				return err
			}
			if strings.ContainsRune(value, 0) {
				return fmt.Errorf("%s: contains a NUL byte, which an environment variable cannot hold", name)
			}
			if !utf8.ValidString(value) {
				return fmt.Errorf("%s: not valid UTF-8", name)
			}
			switch {
			case strings.TrimSpace(value) == "":
				return fmt.Errorf("%s: empty", name)
			case check.IsPlaceholder(value):
				return fmt.Errorf("%s: placeholder, not a value", name)
			}
			if _, ok := resolve.Escape(value); !ok && !v.IsSecret() && !expand {
				return fmt.Errorf("%s: a value that starts with ~ is expanded to your home folder when loaded. Pass --expand to keep that meaning, or use `ezc edit`.", name)
			}
			// A reference's final text is not known here, as in the store audit.
			if !expand || !strings.ContainsRune(value, '$') {
				if why := check.ValidateValue(v, value); why != "" {
					return fmt.Errorf("%s: %s", name, why)
				}
			}

			// A new name is declared first, so a failure to declare leaves nothing behind; if the value then
			// cannot be written, the manifest is put back byte for byte.
			var saved []byte
			if !declared {
				if saved, err = os.ReadFile(sc.manifest()); err != nil {
					return err
				}
				if err := valuefile.Declare(sc.manifest(), name, v, optional); err != nil {
					return err
				}
			}
			undeclare := func(err error) error {
				if !declared {
					if rerr := valuefile.Replace(sc.manifest(), saved); rerr != nil {
						return fmt.Errorf("%w. %s declares %s, which has no value; remove the declaration by hand", err, sc.manifest(), name)
					}
				}
				return err
			}
			path := filepath.Join(sc.dir, file)
			var previous []byte
			existed := false
			if expand {
				if previous, err = os.ReadFile(path); err == nil {
					existed = true
				} else if !errors.Is(err, fs.ErrNotExist) {
					return undeclare(err)
				}
			}
			if err := writeValue(be, path, name, value, v.IsSecret(), expand); err != nil {
				return undeclare(err)
			}
			if expand {
				if err := unresolved(sys, sc, env, path, name); err != nil {
					rerr := os.Remove(path)
					if existed {
						rerr = valuefile.Replace(path, previous)
					}
					if rerr != nil {
						return fmt.Errorf("%w\n       %s could not be put back as it was; fix %s there with `ezc edit`", err, path, name)
					}
					return undeclare(fmt.Errorf("%w\n       Nothing was changed", err))
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
	f.BoolVar(&expand, "expand", false, "keep $NAME, ${NAME}, $$ and a leading ~ meaningful; not for a secret")
	f.StringVar(&typ, "type", "", "the type to declare a new name with: "+typeList())
	f.StringVar(&desc, "desc", "", "the description to declare a new name with")
	f.BoolVar(&optional, "optional", false, "declare a new name optional, not required")
	f.StringSliceVar(&allowed, "values", nil, "the allowed values of a new enum, comma-separated")
	f.IntVar(&lo, "min", 0, "the smallest value of a new int")
	f.IntVar(&hi, "max", 0, "the largest value of a new int")
	noEcho(cmd, " A value that starts with - must be piped on stdin.")
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

// writeValue stores the value, plaintext or through the backend. A plaintext value is written so that it
// loads exactly as given, unless expand asks for it to be written as given.
func writeValue(be backend.Backend, path, name, value string, secret, expand bool) error {
	switch {
	case !secret && expand:
		return valuefile.Set(path, name, value)
	case !secret:
		return valuefile.SetLiteral(path, name, value)
	}
	err := be.Set(path, name, value)
	if errors.Is(err, backend.ErrNoPolicy) {
		err = fmt.Errorf("%w See \"Encryption\" in the README.", err)
	}
	return err
}

// unresolved is the resolver's error when name, as now written in the file at path, cannot be expanded.
// It resolves the environment the write targets, else the one a command would pick; a failure that is
// not this name's is not this command's to report.
func unresolved(sys System, sc scope, env, path, name string) error {
	repo := ""
	if sc.layer == "repo" {
		repo = sc.dir
	}
	_, err := load(sys, repo, envName(sys, env))
	var ref *resolve.RefError
	if !errors.As(err, &ref) || ref.Name != name {
		return nil
	}
	a, errA := os.Stat(ref.Path)
	b, errB := os.Stat(path)
	if errA != nil || errB != nil || !os.SameFile(a, b) {
		return nil
	}
	return err
}

// badName is the reason a name is refused; it never repeats the argument, which may be NAME=value.
func badName(name string) string {
	msg := "that is not a valid variable name; use letters, digits and _, not starting with a digit."
	if strings.Contains(name, "=") {
		msg += " NAME and VALUE are separate arguments; a secret is typed or piped."
	}
	return msg
}

// noEcho makes a flag error say nothing of the arguments, which may hold a value.
func noEcho(cmd *cobra.Command, extra string) {
	cmd.SetFlagErrorFunc(func(c *cobra.Command, _ error) error {
		return usagef("%s: invalid or unrecognised flag.%s Try `%s`.", c.CommandPath(), extra, helpFor(c))
	})
}

func newUnsetCmd(sys System) *cobra.Command {
	var (
		global, undeclare bool
		env               string
	)
	cmd := &cobra.Command{
		Use:   "unset NAME",
		Short: "remove a variable's value from the plaintext and encrypted file",
		Long:  `Remove NAME from the repo's common.yaml and common.enc.yaml, or env.ENV.yaml and env.ENV.enc.yaml with --env, whichever hold it. With -g the store's files are used instead. The declaration in the scope's manifest stays unless --undeclare is given; that is refused while any other values file in the scope still holds NAME.`,
		Args:  exactArgs(1, "a variable name"),
		RunE: func(_ *cobra.Command, args []string) error {
			name := args[0]
			if !manifest.ValidName(name) {
				return usagef("ezc unset: %s", badName(name))
			}
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
			_, declared := m.Lookup(name)
			be := backendFor(sys)
			base := valuesBase(env)
			files := []string{valuesFile(base, false), valuesFile(base, true)}

			if undeclare {
				if other, err := heldInScope(be, sc.dir, name, files); err != nil {
					return err
				} else if other != "" {
					return fmt.Errorf("%s is still set in %s; unset it there first, or leave the declaration.", name, sc.show(other))
				}
			}
			removed := 0
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
				removed++
				fmt.Fprintf(sys.Stdout, "unset %s from %s\n", name, sc.show(file))
			}
			if removed == 0 && !(undeclare && declared) {
				return fmt.Errorf("%s is not set in %s or %s.", name, sc.show(files[0]), sc.show(files[1]))
			}
			if undeclare && declared {
				if gone, err := valuefile.Undeclare(sc.manifest(), name); err != nil {
					return err
				} else if gone {
					fmt.Fprintf(sys.Stdout, "removed %s from %s\n", name, sc.show(manifestName))
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&global, "global", "g", false, "remove from the global store")
	cmd.Flags().StringVar(&env, "env", "", "remove from the environment's files, not the common ones")
	cmd.Flags().BoolVar(&undeclare, "undeclare", false, "also remove the name's declaration from the manifest")
	noEcho(cmd, "")
	return cmd
}

// heldInScope is the first values file in dir (common and every environment, plain or encrypted) other than skip
// that has name; "" when none does. The files are the ones the resolver reads.
func heldInScope(be backend.Backend, dir, name string, skip []string) (string, error) {
	envs, err := resolve.Envs(dir)
	if err != nil {
		return "", err
	}
	bases := []string{"common"}
	for _, e := range envs {
		bases = append(bases, valuesBase(e))
	}
	for _, base := range bases {
		for _, n := range []string{valuesFile(base, false), valuesFile(base, true)} {
			if slices.Contains(skip, n) {
				continue
			}
			if in, err := holds(be, filepath.Join(dir, n), name); err != nil || in {
				return n, err
			}
		}
	}
	return "", nil
}
