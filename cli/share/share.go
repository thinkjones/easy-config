// Package share holds the files ezc writes into other places.
package share

import "embed"

// Skills holds the agent skills ezc setup installs: skills/<name>/SKILL.md.
//
//go:embed skills
var Skills embed.FS
