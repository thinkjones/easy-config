package setup

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
)

// SkillResult is what InstallSkills did with one skill: installed, kept, updated or failed (with Err).
type SkillResult struct {
	Name, Status string
	Err          error
}

// InstallSkills writes each skills/<name>/SKILL.md of skills to <claudeDir>/skills/<name>/SKILL.md: a missing one
// is installed, an identical one kept, and a different one updated, since the skills are ezc's own files. Only an
// fs that cannot be read is an error; a skill that cannot be written is a failed result.
func InstallSkills(claudeDir string, skills fs.FS) ([]SkillResult, error) {
	entries, err := fs.ReadDir(skills, "skills")
	if err != nil {
		return nil, err
	}
	var results []SkillResult
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		want, err := fs.ReadFile(skills, path.Join("skills", e.Name(), "SKILL.md"))
		if err != nil {
			return nil, err
		}
		results = append(results, installSkill(filepath.Join(claudeDir, "skills", e.Name()), e.Name(), want))
	}
	return results, nil
}

func installSkill(dir, name string, want []byte) SkillResult {
	file := filepath.Join(dir, "SKILL.md")
	have, err := os.ReadFile(file)
	status := "updated"
	switch {
	case err == nil && bytes.Equal(have, want):
		return SkillResult{Name: name, Status: "kept"}
	case errors.Is(err, fs.ErrNotExist):
		status = "installed"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return SkillResult{Name: name, Status: "failed", Err: err}
	}
	if err := os.WriteFile(file, want, 0o644); err != nil {
		return SkillResult{Name: name, Status: "failed", Err: err}
	}
	return SkillResult{Name: name, Status: status}
}
