// Package migrate converts a store or a repo from the layout of the tool ezc replaces (fsconfig, whose folder
// is .fs-config/) to ezc's, and proves that the resolved environment is the same before and after.
//
// It is the one place under cli/ allowed to name that tool. Nothing is written until the proof passes.
package migrate

// OldTool is the command of the tool ezc replaces; the proof runs it to compose the environment as it stands.
const OldTool = "fsconfig"
