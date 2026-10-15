package migrate

import (
	"errors"
	"sort"
	"strings"

	"github.com/thinkjones/easy-config/cli/internal/resolve"
)

// storeLabel names the proof of the store alone.
const storeLabel = "(store)"

// after composes with ezc's resolver and keeps what ezc exports. An error names the staged folder as the .ezc it
// will become, not the temporary folder it is in.
func (o Options) after(ro resolve.Options, stage string) (map[string]string, error) {
	ro.Home = o.Home
	ro.Decrypt = o.Backend.Decrypt
	res, err := resolve.Load(ro)
	if err != nil {
		return nil, errors.New(strings.ReplaceAll(err.Error(), stage, newDir))
	}
	env := map[string]string{}
	for _, n := range res.Exported() {
		env[n] = res.Env[n]
	}
	return env, nil
}

// prove compares the environment before and after, by name and by value, and prints one line per difference or
// one ✓ line. It never prints a value.
func (o Options) prove(label string, before, after map[string]string) bool {
	var lines []string
	for n, v := range before {
		a, ok := after[n]
		switch {
		case !ok:
			lines = append(lines, "✗ "+label+": "+n+" only before")
		case a != v:
			lines = append(lines, "✗ "+label+": "+n+" differs")
		}
	}
	for n := range after {
		if _, ok := before[n]; !ok {
			lines = append(lines, "✗ "+label+": "+n+" only after")
		}
	}
	sort.Strings(lines)
	for _, l := range lines {
		o.say("%s", l)
	}
	if len(lines) == 0 {
		o.say("✓ %s: %d names, identical", label, len(after))
	}
	return len(lines) == 0
}
