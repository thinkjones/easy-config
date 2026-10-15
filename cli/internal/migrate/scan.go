package migrate

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// oldName matches a mention of the old tool or its folder.
var oldName = regexp.MustCompile(`(?i)fs[-_]?config`)

// skipped are the folders the scan for the old tool's name does not enter.
var skipped = []string{".git", newDir, oldDir, "node_modules", ".venv", "venv", "vendor", "dist", "build"}

const (
	maxScanned  = 1 << 20 // bytes; a bigger file is not text anyone edits by hand
	maxLines    = 3       // line numbers shown per file
	maxFiles    = 20      // files listed before "and n more"
	binarySniff = 8 << 10 // a NUL in this much of a file makes it binary
)

// oldNames lists the text files under root that still name the old tool, one ! line each with the first line
// numbers. override holds the text a file will have (mise.toml after its rewrite), by path relative to root.
func oldNames(root string, override map[string]string) []string {
	var lines []string
	files := 0
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && slices.Contains(skipped, d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		text, ok := override[rel]
		if !ok {
			if text, ok = readText(p); !ok {
				return nil
			}
		}
		var at []string
		for i, l := range strings.Split(text, "\n") {
			if oldName.MatchString(l) && len(at) < maxLines {
				at = append(at, fmt.Sprint(i+1))
			}
		}
		if len(at) == 0 {
			return nil
		}
		files++
		if files <= maxFiles {
			lines = append(lines, fmt.Sprintf("! %s:%s names the old tool", rel, strings.Join(at, ",")))
		}
		return nil
	})
	if files > maxFiles {
		lines = append(lines, fmt.Sprintf("! and %d more files", files-maxFiles))
	}
	return lines
}

// readText is a file's text, or false when it is too big or binary.
func readText(path string) (string, bool) {
	st, err := os.Stat(path)
	if err != nil || st.Size() > maxScanned {
		return "", false
	}
	b, err := os.ReadFile(path)
	if err != nil || bytes.IndexByte(b[:min(len(b), binarySniff)], 0) >= 0 {
		return "", false
	}
	return string(b), true
}

// ciFindings lists the files under root/.github that pass keyVar, the variable the old tool's CI read its key
// from, with the command that gives the repo an ezc key: rotating the bot of bots (name to repo IDs) that lists
// this repo, matched by the repo folder's name, else adding one.
func ciFindings(root, keyVar string, bots map[string][]string) []string {
	if keyVar == "" {
		return nil
	}
	fix := "ezc bot add <name> --repo <owner/repo>"
	base := filepath.Base(root)
	var names []string
	for name := range bots {
		names = append(names, name)
	}
	sort.Strings(names)
found:
	for _, name := range names {
		for _, id := range bots[name] {
			if _, repo, ok := strings.Cut(id, "/"); (ok && repo == base) || id == base {
				fix = "ezc bot rotate " + name
				break found
			}
		}
	}
	var lines []string
	filepath.WalkDir(filepath.Join(root, ".github"), func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return nil
		}
		text, ok := readText(p)
		if !ok || !strings.Contains(text, keyVar) {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		lines = append(lines, fmt.Sprintf("! %s passes %s to CI; ezc reads EZC_AGE_KEY. Run %s, then see docs/ci.md.", filepath.ToSlash(rel), keyVar, fix))
		return nil
	})
	return lines
}
