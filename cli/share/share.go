// Package share holds the files ezc writes into other places.
package share

import (
	"embed"
	"regexp"
	"strconv"
	"strings"
)

// EnvSh is the shim ezc repo init writes to a repo's .ezc/env.sh.
//
//go:embed env.sh
var EnvSh string

// Skills holds the agent skills ezc setup installs: skills/<name>/SKILL.md.
//
//go:embed skills
var Skills embed.FS

// ShimVersion is the number on the first line of EnvSh ("# ezc-shim: 1").
const ShimVersion = 2

var shimMarker = regexp.MustCompile(`^#\s*ezc-shim:\s*(\d+)\s*$`)

// VersionOf is the number on the first line of a shim ("# ezc-shim: 1"); ok is false when there is none.
func VersionOf(shim string) (n int, ok bool) {
	first, _, _ := strings.Cut(shim, "\n")
	m := shimMarker.FindStringSubmatch(strings.TrimRight(first, "\r"))
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	return n, err == nil
}
