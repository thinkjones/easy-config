package setup

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"testing/fstest"
	"time"
)

func skillFS() fstest.MapFS {
	return fstest.MapFS{
		"skills/one/SKILL.md": {Data: []byte("---\nname: one\n---\nfirst\n")},
		"skills/two/SKILL.md": {Data: []byte("---\nname: two\n---\nsecond\n")},
	}
}

func statuses(rs []SkillResult) map[string]string {
	m := map[string]string{}
	for _, r := range rs {
		m[r.Name] = r.Status
	}
	return m
}

func TestInstallSkills(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".claude")
	skills := skillFS()
	rs, err := InstallSkills(dir, skills)
	if err != nil {
		t.Fatal(err)
	}
	if got := statuses(rs); !reflect.DeepEqual(got, map[string]string{"one": "installed", "two": "installed"}) {
		t.Fatalf("first run: %v", got)
	}
	if rs[0].Name != "one" || rs[1].Name != "two" {
		t.Errorf("order: %v", rs)
	}
	one := filepath.Join(dir, "skills", "one", "SKILL.md")
	if b, _ := os.ReadFile(one); string(b) != string(skills["skills/one/SKILL.md"].Data) {
		t.Errorf("one = %q", b)
	}
	old := time.Now().Add(-time.Hour)
	os.Chtimes(one, old, old)
	rs, _ = InstallSkills(dir, skills)
	if got := statuses(rs); !reflect.DeepEqual(got, map[string]string{"one": "kept", "two": "kept"}) {
		t.Fatalf("second run: %v", got)
	}
	if st, _ := os.Stat(one); !st.ModTime().Equal(old) {
		t.Error("an identical skill was written again")
	}
	os.WriteFile(one, []byte("edited\n"), 0o644)
	rs, _ = InstallSkills(dir, skills)
	if got := statuses(rs); !reflect.DeepEqual(got, map[string]string{"one": "updated", "two": "kept"}) {
		t.Fatalf("after an edit: %v", got)
	}
	if b, _ := os.ReadFile(one); string(b) != string(skills["skills/one/SKILL.md"].Data) {
		t.Error("the edited skill was not replaced")
	}
}

func TestInstallSkillsFailsEachWhenTheFolderIsAFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".claude")
	os.WriteFile(dir, []byte("x"), 0o644)
	rs, err := InstallSkills(dir, skillFS())
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rs {
		if r.Status != "failed" || r.Err == nil {
			t.Errorf("%s: %s %v", r.Name, r.Status, r.Err)
		}
	}
	if len(rs) != 2 {
		t.Errorf("results: %v", rs)
	}
}
