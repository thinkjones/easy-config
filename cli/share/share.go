// Package share holds the files ezc writes into other places.
package share

import _ "embed"

// EnvSh is the shim ezc repo init writes to a repo's .ezc/env.sh.
//
//go:embed env.sh
var EnvSh string

// ShimVersion is the number on the first line of EnvSh ("# ezc-shim: 1").
const ShimVersion = 1
