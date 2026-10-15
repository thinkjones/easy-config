package migrate

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"filippo.io/age"

	"github.com/thinkjones/easy-config/cli/internal/backend"
	"github.com/thinkjones/easy-config/cli/internal/backend/sops"
	"github.com/thinkjones/easy-config/cli/internal/proc"
)

func realRunner(c proc.Command) ([]byte, []byte, error) {
	cmd := exec.Command(c.Name, c.Args...)
	cmd.Dir, cmd.Env = c.Dir, c.Env
	cmd.Stdin = bytes.NewReader(c.Stdin)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	return out.Bytes(), errb.Bytes(), err
}

// TestRealSopsStoreMigrates migrates a store whose secrets real sops encrypted, with a fake old tool.
func TestRealSopsStoreMigrates(t *testing.T) {
	if _, err := exec.LookPath("sops"); err != nil {
		if os.Getenv("EZC_REQUIRE_SOPS") == "1" {
			t.Fatal("sops is required (EZC_REQUIRE_SOPS=1) but is not on PATH")
		}
		t.Skip("sops is not installed")
	}
	root := t.TempDir()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(root, "keys.txt")
	if err := os.WriteFile(keyFile, []byte(id.String()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	pub := id.Recipient().String()
	b := sops.New(sops.Options{Run: realRunner, LookPath: exec.LookPath, Environ: []string{"HOME=" + filepath.Join(root, "home")}, KeyFile: keyFile})

	dir := filepath.Join(root, "store")
	w := newWorld(t, dir)
	w.o.Backend = b
	old := filepath.Join(dir, ".fs-config")
	write(t, filepath.Join(old, "global.yaml"), "A: one\n")
	write(t, filepath.Join(old, "recipients.yaml"), "recipients:\n  gene:\n    key: "+pub+"\n    purpose: human\n")
	if err := b.WritePolicy(old, []string{pub}); err != nil {
		t.Fatal(err)
	}
	if err := b.Set(filepath.Join(old, "global.enc.yaml"), "S", "it's a $ecret\nline two"); err != nil {
		t.Fatal(err)
	}
	w.tool.out[""] = map[string]string{"A": "one", "S": "it's a $ecret\nline two"}
	if err := Run(w.o); err != nil {
		t.Fatalf("Run: %v\n%s", err, w.out)
	}
	enc := filepath.Join(dir, ".ezc", "common.enc.yaml")
	got, err := b.Decrypt(enc)
	if err != nil || !reflect.DeepEqual(got, []backend.Pair{{Name: "S", Value: "it's a $ecret\nline two"}}) {
		t.Errorf("decrypt: %v, %v", got, err)
	}
	policy, err := b.PolicyRecipients(enc)
	if err != nil || !reflect.DeepEqual(policy, []string{pub}) {
		t.Errorf("policy %v, %v", policy, err)
	}
}
