package migrate

import (
	"sort"

	"github.com/thinkjones/easy-config/cli/internal/resolve"
)

// storeLabel names the proof of the store alone.
const storeLabel = "(store)"

// afterStore composes the staged store alone with ezc's resolver.
func (o Options) afterStore(stage string) (map[string]string, error) {
	return o.after(resolve.Options{StoreDir: stage})
}

// after composes with ezc's resolver and keeps what ezc exports.
func (o Options) after(ro resolve.Options) (map[string]string, error) {
	ro.Home = o.Home
	ro.Decrypt = o.Backend.Decrypt
	res, err := resolve.Load(ro)
	if err != nil {
		return nil, err
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
