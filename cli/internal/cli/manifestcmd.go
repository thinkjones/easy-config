package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/thinkjones/easy-config/cli/internal/gen"
	"github.com/thinkjones/easy-config/cli/internal/manifest"
)

// schemaID is the published URL of the manifest JSON Schema; change it with the file's location.
const schemaID = "https://raw.githubusercontent.com/thinkjones/easy-config/main/cli/schema/" + manifest.SchemaVersion + "/manifest.schema.json"

// writeOut writes b to path, creating its folder, and says "wrote <path>" on stderr; "" writes to stdout.
func writeOut(sys System, path string, b []byte) error {
	if path == "" {
		_, err := sys.Stdout.Write(b)
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return err
	}
	_, err := fmt.Fprintf(sys.Stderr, "wrote %s\n", path)
	return err
}

func newManifestCmd(sys System) *cobra.Command {
	cmd := verbs(&cobra.Command{
		Use:   "manifest",
		Short: "validate the manifest, or print its JSON Schema",
	}, "validate or schema")
	cmd.AddCommand(newManifestValidateCmd(sys), newManifestSchemaCmd(sys))
	return cmd
}

func newManifestValidateCmd(sys System) *cobra.Command {
	var manifestFlag string
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "check that the manifest is well formed",
		Args:  noArgs,
		RunE: func(*cobra.Command, []string) error {
			_, path, explicit, err := manifestTarget(sys, manifestFlag)
			if err != nil {
				return err
			}
			m, err := loadManifest(path, explicit)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(sys.Stdout, "%s ok — %d required, %d optional (schema %s)\n", path, len(m.Required), len(m.Optional), m.SchemaVersion)
			return err
		},
	}
	cmd.Flags().StringVarP(&manifestFlag, "manifest", "m", "", "the manifest to check (default: the repo's)")
	return cmd
}

func newManifestSchemaCmd(sys System) *cobra.Command {
	var out string
	cmd := &cobra.Command{
		Use:   "schema",
		Short: "print the manifest's JSON Schema",
		Args:  noArgs,
		RunE: func(*cobra.Command, []string) error {
			b, err := manifest.JSONSchema(schemaID)
			if err != nil {
				return err
			}
			return writeOut(sys, out, b)
		},
	}
	cmd.Flags().StringVarP(&out, "output", "o", "", "write to FILE instead of stdout")
	return cmd
}

func newCodegenCmd(sys System) *cobra.Command {
	cmd := verbs(&cobra.Command{
		Use:   "codegen",
		Short: "generate typed config code from the manifest",
	}, "python")
	cmd.AddCommand(newCodegenPythonCmd(sys))
	return cmd
}

func newCodegenPythonCmd(sys System) *cobra.Command {
	var manifestFlag, out string
	cmd := &cobra.Command{
		Use:   "python",
		Short: "generate a pydantic config module",
		Long:  "Validate the manifest, then generate a pydantic v2 module for it. An invalid manifest generates nothing.",
		Args:  noArgs,
		RunE: func(*cobra.Command, []string) error {
			_, path, explicit, err := manifestTarget(sys, manifestFlag)
			if err != nil {
				return err
			}
			m, err := loadManifest(path, explicit)
			if err != nil {
				return err
			}
			b, err := gen.Python(m, path)
			if err != nil {
				return err
			}
			return writeOut(sys, out, b)
		},
	}
	cmd.Flags().StringVarP(&manifestFlag, "manifest", "m", "", "the manifest to generate from (default: the repo's)")
	cmd.Flags().StringVarP(&out, "output", "o", "", "write to FILE instead of stdout")
	return cmd
}
