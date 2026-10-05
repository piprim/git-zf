// Package gittest holds test helpers for packages that drive real
// repositories. It imports nothing from this module, so any package's tests
// can use it.
package gittest

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// SSHSigner makes the repository at dir able to sign commits with a fresh SSH
// key and to verify them: gpg.format, user.signingkey and
// gpg.ssh.allowedSignersFile are set in its local configuration.
// commit.gpgsign is left as it is. The test is skipped when ssh-keygen is not
// installed.
func SSHSigner(t testing.TB, dir string) {
	t.Helper()

	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not installed")
	}

	keyDir := t.TempDir()
	key := filepath.Join(keyDir, "key")
	run(t, keyDir, "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "signer@test.com", "-f", key)

	pub, err := os.ReadFile(key + ".pub")
	if err != nil {
		t.Fatalf("read public key: %v", err)
	}

	allowed := filepath.Join(keyDir, "allowed_signers")
	if err := os.WriteFile(allowed, append([]byte("signer@test.com "), pub...), 0o600); err != nil {
		t.Fatalf("write allowed signers: %v", err)
	}

	for _, kv := range [][2]string{
		{"gpg.format", "ssh"},
		{"user.signingkey", key + ".pub"},
		{"gpg.ssh.allowedSignersFile", allowed},
	} {
		run(t, dir, "git", "config", kv[0], kv[1])
	}
}

func run(t testing.TB, dir, name string, args ...string) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}
