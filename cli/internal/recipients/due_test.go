package recipients

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDue(t *testing.T) {
	dir := t.TempDir()
	d, err := LoadDue(dir)
	if err != nil || len(d) != 0 {
		t.Fatalf("LoadDue missing = %v, %v", d, err)
	}
	d.Add("common.enc.yaml", "LLM_API_KEY", "sam", "2026-10-11")
	d.Add("common.enc.yaml", "LLM_API_KEY", "other", "2026-11-01")
	if e := d["common.enc.yaml/LLM_API_KEY"]; e.Removed != "sam" || e.On != "2026-10-11" {
		t.Errorf("Add replaced an entry: %+v", e)
	}
	if err := d.Save(dir); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, DueFile))
	if string(data) != "common.enc.yaml/LLM_API_KEY: {removed: sam, on: 2026-10-11}\n" {
		t.Errorf("file = %q", data)
	}
	back, err := LoadDue(dir)
	if err != nil || len(back) != 1 {
		t.Fatalf("reload = %v, %v", back, err)
	}
	if back.Clear("common.enc.yaml", "NOPE") {
		t.Error("Clear of a missing entry returned true")
	}
	if !back.Clear("common.enc.yaml", "LLM_API_KEY") {
		t.Error("Clear returned false")
	}
	if err := back.Save(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, DueFile)); !os.IsNotExist(err) {
		t.Errorf("empty Due left the file: %v", err)
	}
}

func TestDueRoundTripsAwkwardNames(t *testing.T) {
	dir := t.TempDir()
	d := Due{}
	d.Add("common.enc.yaml", "N", "null", "2026-10-11")
	if err := d.Save(dir); err != nil {
		t.Fatal(err)
	}
	back, err := LoadDue(dir)
	if err != nil || back["common.enc.yaml/N"].Removed != "null" {
		t.Errorf("round trip = %v, %v", back, err)
	}
}
