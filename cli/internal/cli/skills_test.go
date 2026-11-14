package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Setup leaves Claude Code's folder alone: ezc's agent skills are installed by hand, when wanted.
func TestSetupInstallsNoSkills(t *testing.T) {
	f := newFake(t)
	if err := f.run("setup", "--new-local", "--name", "gene", "--yes"); err != nil {
		t.Fatalf("setup: %v\n%s%s", err, f.out.String(), f.errOut.String())
	}
	if out := f.out.String() + f.errOut.String(); strings.Contains(out, "skill") {
		t.Errorf("setup talks about skills:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(f.env["HOME"], ".claude")); !os.IsNotExist(err) {
		t.Errorf("setup wrote under ~/.claude: %v", err)
	}
}
