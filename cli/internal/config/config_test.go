package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func sample() Config {
	return Config{
		User:    "gene",
		Backend: "sops",
		KeyFile: "/home/gene/.config/sops/age/keys.txt",
		Store:   Store{Remote: "git@github.com:acme/config.git"},
		Checkouts: map[string]string{
			"acme/app": "/home/gene/dev/app",
		},
	}
}

func TestRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	want := sample()
	if err := Save(path, want); err != nil {
		t.Fatal(err)
	}
	got, found, err := Load(path)
	if err != nil || !found {
		t.Fatalf("Load: found=%v err=%v", found, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestLoadAbsent(t *testing.T) {
	_, found, err := Load(filepath.Join(t.TempDir(), "config.toml"))
	if found || err != nil {
		t.Fatalf("found=%v err=%v", found, err)
	}
}

func TestLoadInvalid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("user = \n[["), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("err = %v, want one naming %s", err, path)
	}
}

func TestLoadRelativeKeyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("key_file = \"keys.txt\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "key_file") {
		t.Fatalf("err = %v, want one naming the path and key_file", err)
	}
}

func TestLoadIgnoresUnknownKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("user = \"gene\"\nfuture = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, found, err := Load(path)
	if err != nil || !found || c.User != "gene" {
		t.Fatalf("c=%+v found=%v err=%v", c, found, err)
	}
}

func TestSaveLocalOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	c := sample()
	c.Store.Remote = ""
	if err := Save(path, c); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "remote") {
		t.Fatalf("file has a remote line:\n%s", data)
	}
}

func TestSaveCreatesFolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a", "b", "config.toml")
	if err := Save(path, sample()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

func TestSaveLeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	if err := Save(filepath.Join(dir, "config.toml"), sample()); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != "config.toml" {
		t.Fatalf("folder holds %v", entries)
	}
}
