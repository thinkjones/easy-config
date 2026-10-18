package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/thinkjones/easy-config/cli/internal/backend"
	"github.com/thinkjones/easy-config/cli/internal/manifest"
	"github.com/thinkjones/easy-config/cli/internal/proc"
	"github.com/thinkjones/easy-config/cli/internal/resolve"
)

// asyncEditors return before the user has finished editing unless told to wait.
var asyncEditors = []string{"code", "code-insiders", "cursor", "windsurf", "subl", "zed", "atom", "mate"}

// editorCommand is the user's editor split into words: $VISUAL, else $EDITOR. Quoting is not understood.
func editorCommand(sys System) ([]string, error) {
	for _, v := range []string{"VISUAL", "EDITOR"} {
		if words := strings.Fields(sys.Getenv(v)); len(words) > 0 {
			return words, nil
		}
	}
	return nil, errors.New("set EDITOR to an editor that waits, for example: export EDITOR=nano")
}

// waitFlag reports whether w makes an async editor wait: --wait, --wait=..., or a short flag cluster holding w or W.
func waitFlag(w string) bool {
	if strings.HasPrefix(w, "--") {
		return w == "--wait" || strings.HasPrefix(w, "--wait=")
	}
	return strings.HasPrefix(w, "-") && strings.ContainsAny(w, "wW")
}

// waits is an error when the editor would return before the user finishes.
func waits(editor []string) error {
	base := strings.TrimSuffix(strings.TrimSuffix(filepath.Base(editor[0]), ".exe"), ".cmd")
	if slices.Contains(asyncEditors, base) && !slices.ContainsFunc(editor[1:], waitFlag) {
		return fmt.Errorf("%s returns before you finish editing, so changes would be lost. Use `%s --wait`.", editor[0], editor[0])
	}
	return nil
}

// validValues is the problem with the plaintext values file at path, as a message with no value in it; "" when it is fine.
func validValues(path string) string {
	pairs, _, err := resolve.Plain(path)
	if err != nil {
		return err.Error()
	}
	for _, p := range pairs {
		switch {
		case slices.Contains(resolve.SelectorVars, p.Name):
			return fmt.Sprintf("%s names the environment and cannot be stored.", p.Name)
		case !manifest.ValidName(p.Name):
			return "a name in the file is not a valid variable name; use letters, digits and _, not starting with a digit."
		}
	}
	return ""
}

func newEditCmd(sys System) *cobra.Command {
	var (
		global, secrets bool
		env             string
	)
	cmd := &cobra.Command{
		Use:   "edit",
		Short: "open a values file in your editor",
		Long: `Open common.yaml, or env.ENV.yaml with --env, in $VISUAL or $EDITOR; with --secrets the encrypted file of the same name. With -g the store's files are used instead.

The editor must wait until you finish: for example nano, vim, or "code --wait". The command is split on spaces; quoting inside $EDITOR is not supported.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkEnvName("edit", env); err != nil {
				return err
			}
			if !sys.StdinIsTerminal() {
				return errors.New("ezc edit needs a terminal")
			}
			editor, err := editorCommand(sys)
			if err != nil {
				return err
			}
			if err := waits(editor); err != nil {
				return err
			}
			if _, err := sys.LookPath(editor[0]); err != nil {
				return fmt.Errorf("%s is not installed or not on PATH.", editor[0])
			}
			sc, err := scopeFor(sys, global)
			if err != nil {
				return err
			}
			file := valuesFile(valuesBase(env), secrets)
			path, err := absPath(sys, filepath.Join(sc.dir, file))
			if err != nil {
				return err
			}
			// The file itself may hold uncommitted changes: "edit <file>" is a true name for them too,
			// and a result that was saved but is not valid has to be opened again.
			st, err := sc.guard(sys)
			if err != nil {
				return err
			}
			if secrets {
				// Whoever the file is encrypted for reads what is saved in it.
				if err = sc.guardRecipients(sys, file); err == nil {
					err = editSecrets(sys, sc, env, path, editor)
				}
			} else {
				err = editPlain(sys, sc, file, path, editor)
			}
			if err != nil {
				return err
			}
			fmt.Fprintf(sys.Stdout, "edited %s\n", sc.show(file))
			return commitStore(sys, st, "edit "+file, file)
		},
	}
	cmd.Flags().BoolVarP(&global, "global", "g", false, "edit the global store's file")
	cmd.Flags().StringVar(&env, "env", "", "edit the environment's file, not the common one")
	cmd.Flags().BoolVar(&secrets, "secrets", false, "edit the encrypted file")
	return cmd
}

// editPlain runs the editor on a plaintext file, creating it first, and checks what is left.
func editPlain(sys System, sc scope, file, path string, editor []string) error {
	created := false
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			return err
		}
		created = true
	} else if err != nil {
		return err
	}
	_, _, runErr := sys.Exec(proc.Command{Name: editor[0], Args: append(slices.Clone(editor[1:]), path), Interactive: true})
	if info, err := os.Stat(path); created && err == nil && info.Size() == 0 {
		os.Remove(path)
	}
	if runErr != nil {
		fmt.Fprintf(sys.Stderr, "%s exited with an error; %s may not have been saved.\n", editor[0], sc.show(file))
		return &ExitError{1}
	}
	if msg := validValues(path); msg != "" {
		fmt.Fprintf(sys.Stderr, "%s\n%s was saved but is not valid; fix it with `ezc edit`.\n", msg, sc.show(file))
		return &ExitError{1}
	}
	return nil
}

// editSecrets has the backend edit the encrypted file, then checks that every name in it is encrypted.
func editSecrets(sys System, sc scope, env, path string, editor []string) error {
	be := backendFor(sys)
	if ok, err := be.HasPolicy(sc.dir); err != nil {
		return err
	} else if !ok {
		return noPolicy(sc)
	}
	show := sc.show(filepath.Base(path))
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		set := "ezc set"
		if sc.layer == "store" {
			set += " -g"
		}
		set += " <NAME>"
		if env != "" {
			set += " --env " + env
		}
		return fmt.Errorf("%s does not exist yet. Create it by setting a secret: `%s`.", show, set)
	} else if err != nil {
		return err
	}
	// Fail with the backend's own message before an editor opens; the values are dropped at once.
	// Any other failure is a file that needs repairing, which this command must still open.
	if _, err := be.Decrypt(path); errors.Is(err, backend.ErrNoKey) {
		return err
	}
	if err := be.Edit(path, strings.Join(editor, " ")); err != nil {
		return err
	}
	if _, err := os.Stat(path); err != nil {
		return nil // every name was removed
	}
	_, unencrypted, err := be.Inspect(path)
	if err != nil {
		return err
	}
	if len(unencrypted) > 0 {
		lines := make([]string, len(unencrypted))
		for i, n := range unencrypted {
			lines[i] = fmt.Sprintf("%s: %s is not encrypted; set it again with `ezc set %s`.", show, n, n)
		}
		return errors.New(strings.Join(lines, "\n"))
	}
	if _, err := be.Decrypt(path); err != nil {
		fmt.Fprintf(sys.Stderr, "%v\n%s was saved but is not valid; fix it with `ezc edit --secrets`.\n", err, show)
		return &ExitError{1}
	}
	return nil
}
