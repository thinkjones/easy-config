package manifest

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Load reads and parses a manifest.yaml. KnownFields makes an unrecognised key
// an error rather than a silent no-op — the same failure mode as a mistyped
// mise _.file entry, and just as invisible without it.
func Load(path string) (Manifest, error) {
	var m Manifest
	f, err := os.Open(path)
	if err != nil {
		return m, fmt.Errorf("%s: %w", path, err)
	}
	defer f.Close()

	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil {
		return m, fmt.Errorf("%s: %w", path, err)
	}
	return m, nil
}
