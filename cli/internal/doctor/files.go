package doctor

import (
	"os"
	"regexp"
	"slices"
	"strings"
)

// Names recognised in any .ezc folder, and the extra ones a repo and a store have. Suggestions are made
// against these only, never against environment files.
var (
	commonNames = []string{"manifest.yaml", "common.yaml", "common.enc.yaml"}
	repoNames   = []string{"env.sh"}
	storeNames  = []string{"recipients.yaml", "repos.yaml", "rotation-due.yaml"}
)

// storeFolders are the folders a store keeps.
var storeFolders = []string{"requests"}

var envFile = regexp.MustCompile(`^env\.([^.]+)(\.enc)?\.yaml$`)

// maxDistance is how far a name may be from a recognised one and still be suggested.
const maxDistance = 2

// Unrecognised lists the files in a .ezc folder that ezc does not read, with a suggestion for each
// ("" when there is none). store says dir is the global store's, not a repo's. extra are further
// recognised names, such as the backend's policy file. A folder that does not exist has none.
func Unrecognised(dir string, store bool, extra []string) ([][2]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	fixed := slices.Clone(commonNames)
	if store {
		fixed = append(fixed, storeNames...)
	} else {
		fixed = append(fixed, repoNames...)
	}
	var out [][2]string
	for _, e := range entries {
		n := e.Name()
		if recognised(n, e.IsDir(), store, fixed, extra) {
			continue
		}
		out = append(out, [2]string{n, suggest(n, fixed)})
	}
	return out, nil
}

func recognised(name string, isDir, store bool, fixed, extra []string) bool {
	switch {
	case name == ".gitignore", name == ".DS_Store", slices.Contains(extra, name):
		return true
	case isDir:
		return store && slices.Contains(storeFolders, name)
	case slices.Contains(fixed, name):
		return true
	}
	m := envFile.FindStringSubmatch(name)
	return m != nil && validEnv(m[1])
}

// suggest is the recognised name that name was probably meant to be, or "".
func suggest(name string, fixed []string) string {
	switch {
	case strings.HasSuffix(name, ".yml"):
		return strings.TrimSuffix(name, ".yml") + ".yaml"
	case name == "env.enc.yaml":
		return "env.<name>.enc.yaml"
	}
	best, bestD := "", maxDistance+1
	for _, f := range fixed {
		if d := distance(name, f); d < bestD {
			best, bestD = f, d
		}
	}
	return best
}

// distance is the Levenshtein distance between a and b.
func distance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur := make([]int, len(rb)+1)
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(rb)]
}
