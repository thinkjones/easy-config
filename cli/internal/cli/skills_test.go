package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thinkjones/easy-config/cli/share"
)

func TestSetupInstallsSkills(t *testing.T) {
	f := newFake(t)
	if err := f.run("setup", "--new-local", "--name", "gene", "--yes"); err != nil {
		t.Fatalf("setup: %v\n%s%s", err, f.out.String(), f.errOut.String())
	}
	out := f.out.String()
	for _, name := range []string{"ezc-convert", "ezc-migrate"} {
		line := "+ installed the skill " + name + "\n"
		i, next := strings.Index(out, line), strings.Index(out, "Next:")
		if i < 0 || next < 0 || i > next {
			t.Errorf("output lacks %q before Next:\n%s", line, out)
		}
		want, _ := share.Skills.ReadFile("skills/" + name + "/SKILL.md")
		got, err := os.ReadFile(filepath.Join(f.env["HOME"], ".claude", "skills", name, "SKILL.md"))
		if err != nil || string(got) != string(want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	f.out.Reset()
	if err := f.run("setup", "--name", "gene", "--yes"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.out.String(), "✓ the skill ezc-convert\n✓ the skill ezc-migrate\n") {
		t.Errorf("second run:\n%s", f.out.String())
	}
}

func TestSetupInstallsSkillsInClaudeConfigDir(t *testing.T) {
	f := newFake(t)
	f.env["CLAUDE_CONFIG_DIR"] = filepath.Join(t.TempDir(), "claude")
	if err := f.run("setup", "--new-local", "--name", "gene", "--yes"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(f.env["CLAUDE_CONFIG_DIR"], "skills", "ezc-convert", "SKILL.md")); err != nil {
		t.Error(err)
	}
	if _, err := os.Stat(filepath.Join(f.env["HOME"], ".claude")); err == nil {
		t.Error("written under HOME too")
	}
}

func TestSetupSaysWhenASkillCannotBeInstalled(t *testing.T) {
	f := newFake(t)
	os.WriteFile(filepath.Join(f.env["HOME"], ".claude"), []byte("x"), 0o644)
	if err := f.run("setup", "--new-local", "--name", "gene", "--yes"); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	if !strings.Contains(f.out.String(), "! could not install the skill ezc-convert (") {
		t.Errorf("output:\n%s", f.out.String())
	}
}
