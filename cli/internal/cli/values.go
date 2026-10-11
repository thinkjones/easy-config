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

	"github.com/thinkjones/easy-config/cli/internal/manifest"
	"github.com/thinkjones/easy-config/cli/internal/resolve"
)

// manifestIfPresent is the manifest at path; found is false when there is no such file. An invalid manifest is an error.
func manifestIfPresent(path string) (m manifest.Manifest, found bool, err error) {
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return m, false, nil
	}
	m, err = loadManifest(path, true)
	return m, err == nil, err
}

// storeManifest is the store's manifest; found is false when it has none.
func storeManifest(sys System) (manifest.Manifest, bool, error) {
	p, err := storePaths(sys)
	if err != nil {
		return manifest.Manifest{}, false, err
	}
	return manifestIfPresent(filepath.Join(p.StoreEzc(), manifestName))
}

func newListCmd(sys System) *cobra.Command {
	var global bool
	cmd := &cobra.Command{
		Use:   "list [ENV]",
		Short: "list an environment's names, types and source files; never values",
		Long:  `List the names ENV (else $EZC_ENV, $APP_ENV, then local) holds: each name's type from the repo's manifest (else the store's), the file that supplies it, and the files it overrides. Then the manifest's names that have no value. Prints no values. With -g it lists the store alone, typed by the store's manifest.`,
		Args:  maxArgs(1, "at most one environment name"),
		RunE: func(_ *cobra.Command, args []string) error {
			var repo string
			var err error
			if !global {
				if repo, err = requireRepoDir(sys); err != nil {
					return err
				}
			}
			// Resolve first: what is wrong with the store is said once, by the resolver.
			res, err := load(sys, repo, envName(sys, firstArg(args)))
			if err != nil {
				return err
			}
			// ms[0] is the scope's own manifest; in a repo the store's follows, for the names the repo's does not declare.
			var ms []manifest.Manifest
			if !global {
				m, _, err := manifestIfPresent(filepath.Join(repo, manifestName))
				if err != nil {
					return err
				}
				ms = append(ms, m)
			}
			sm, _, err := storeManifest(sys)
			if err != nil {
				return err
			}
			ms = append(ms, sm)
			warn(sys, res, repo)
			_, err = fmt.Fprint(sys.Stdout, renderList(ms, res))
			return err
		},
	}
	cmd.Flags().BoolVarP(&global, "global", "g", false, "list the global store alone")
	return cmd
}

// typeOf is name's type in the first of ms that declares it, or "-".
func typeOf(ms []manifest.Manifest, name string) string {
	for _, m := range ms {
		if v, ok := m.Lookup(name); ok {
			return string(v.Type)
		}
	}
	return "-"
}

// renderList is the `ezc list` table: set names, typed by the first of ms that declares each, then the names ms[0] declares that have no value.
func renderList(ms []manifest.Manifest, res resolve.Result) string {
	var names []string
	for _, n := range res.Names() {
		if !slices.Contains(resolve.SelectorVars, n) {
			names = append(names, n)
		}
	}
	for _, n := range slices.Sorted(slices.Values(resolve.SelectorVars)) {
		if _, ok := res.Env[n]; ok {
			names = append(names, n)
		}
	}
	var rows [][]string
	for _, n := range names {
		var note string
		if over := res.Overrides(n); len(over) > 0 {
			bases := make([]string, len(over))
			for i, p := range over {
				bases[i] = filepath.Base(p)
			}
			note = "overrides " + strings.Join(bases, ", ")
		}
		rows = append(rows, []string{n, typeOf(ms, n), fileOf(res, n), note})
	}
	out := table(rows)
	var unset [][]string
	for _, e := range ms[0].All() {
		if _, ok := res.Env[e.Name]; !ok {
			unset = append(unset, []string{e.Name, string(e.Var.Type), "(not set)"})
		}
	}
	slices.SortFunc(unset, func(a, b []string) int { return strings.Compare(a[0], b[0]) })
	if len(unset) > 0 {
		out += "\n" + table(unset)
	}
	return out
}

// fileOf is where name's value came from: store/<base>, repo/<base> or shell.
func fileOf(res resolve.Result, name string) string {
	origin := res.Origin[name]
	for _, s := range res.Sources {
		if s.Path == origin {
			return s.Layer + "/" + filepath.Base(s.Path)
		}
	}
	return origin
}

// table lays rows out in columns padded to the widest cell, two spaces apart, with trailing spaces trimmed.
func table(rows [][]string) string {
	var widths []int
	for _, r := range rows {
		for i, c := range r {
			if i >= len(widths) {
				widths = append(widths, 0)
			}
			widths[i] = max(widths[i], len(c))
		}
	}
	var b strings.Builder
	for _, r := range rows {
		var line strings.Builder
		for i, c := range r {
			line.WriteString(c)
			line.WriteString(strings.Repeat(" ", widths[i]-len(c)+2))
		}
		b.WriteString(strings.TrimRight(line.String(), " "))
		b.WriteByte('\n')
	}
	return b.String()
}

func newGetCmd(sys System) *cobra.Command {
	var reveal bool
	cmd := &cobra.Command{
		Use:   "get NAME [ENV]",
		Short: "print one variable's value; secrets need --reveal",
		Long:  `Print NAME's value from ENV (else $EZC_ENV, $APP_ENV, then local) on stdout, and nothing else: the value verbatim followed by one newline, even when the value itself ends in one. In a repo it resolves the repo with the store; elsewhere the store alone. A secret (declared secret by the repo's manifest or the store's) is printed only with --reveal.`,
		Args: func(cmd *cobra.Command, args []string) error {
			if err := minArgs(1, "a variable name")(cmd, args); err != nil {
				return err
			}
			return maxArgs(2, "a name and at most one environment")(cmd, args)
		},
		RunE: func(_ *cobra.Command, args []string) error {
			name := args[0]
			repo, err := repoDir(sys)
			if err != nil {
				return err
			}
			// Resolve first: what is wrong with the store is said once, by the resolver.
			res, err := load(sys, repo, envName(sys, firstArg(args[1:])))
			if err != nil {
				return err
			}
			var ms []manifest.Manifest
			if repo != "" {
				m, found, err := manifestIfPresent(filepath.Join(repo, manifestName))
				if err != nil {
					return err
				}
				if found {
					ms = append(ms, m)
				}
			}
			sm, found, err := storeManifest(sys)
			if err != nil {
				return err
			}
			if found {
				ms = append(ms, sm)
			}
			value, ok := res.Env[name]
			if !ok {
				return fmt.Errorf("%s is not set in environment %s.", name, res.EnvLabel())
			}
			for _, m := range ms {
				if v, ok := m.Lookup(name); ok && v.IsSecret() && !reveal {
					return fmt.Errorf("%s is a secret — add --reveal to print it.", name)
				}
			}
			_, err = io.WriteString(sys.Stdout, value+"\n")
			return err
		},
	}
	cmd.Flags().BoolVar(&reveal, "reveal", false, "print a secret")
	return cmd
}
