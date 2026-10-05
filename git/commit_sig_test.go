package git

import (
	"testing"

	"github.com/piprim/git-zf/internal/gittest"
)

func TestCommitSignature(t *testing.T) {
	t.Parallel()

	client, dir := newDiskRepo(t)
	ctx := t.Context()
	mustGit(t, dir, "config", "commit.gpgsign", "false")
	gittest.SSHSigner(t, dir)

	unsigned, err := client.WriteChainRoot(ctx, []byte(`{}`), "unsigned", false)
	if err != nil {
		t.Fatalf("WriteChainRoot unsigned: %v", err)
	}
	signed, err := client.WriteChainRoot(ctx, []byte(`{}`), "signed", true)
	if err != nil {
		t.Fatalf("WriteChainRoot signed: %v", err)
	}

	t.Run("sign=true signs although commit.gpgsign is false", func(t *testing.T) {
		if ok, err := client.CommitSigned(ctx, signed); err != nil || !ok {
			t.Errorf("CommitSigned = %v, %v", ok, err)
		}
	})

	t.Run("sign=false with commit.gpgsign false leaves the commit unsigned", func(t *testing.T) {
		if ok, err := client.CommitSigned(ctx, unsigned); err != nil || ok {
			t.Errorf("CommitSigned = %v, %v", ok, err)
		}
	})

	t.Run("VerifyCommit accepts a signature from an allowed signer", func(t *testing.T) {
		if err := client.VerifyCommit(ctx, signed); err != nil {
			t.Errorf("VerifyCommit: %v", err)
		}
	})

	t.Run("VerifyCommit rejects an unsigned commit", func(t *testing.T) {
		if err := client.VerifyCommit(ctx, unsigned); err == nil {
			t.Error("VerifyCommit accepted an unsigned commit")
		}
	})

	t.Run("CommitSigner names the principal of a verified signature", func(t *testing.T) {
		if got, err := client.CommitSigner(ctx, signed); err != nil || got != "signer@test.com" {
			t.Errorf("CommitSigner = %q, %v", got, err)
		}
	})

	t.Run("CommitSigner is empty for an unsigned commit", func(t *testing.T) {
		if got, err := client.CommitSigner(ctx, unsigned); err != nil || got != "" {
			t.Errorf("CommitSigner = %q, %v", got, err)
		}
	})

	mustGit(t, dir, "config", "--unset", "gpg.ssh.allowedSignersFile")

	t.Run("a signature git cannot check is signed but not verified", func(t *testing.T) {
		if ok, err := client.CommitSigned(ctx, signed); err != nil || !ok {
			t.Errorf("CommitSigned = %v, %v", ok, err)
		}
		if err := client.VerifyCommit(ctx, signed); err == nil {
			t.Error("VerifyCommit accepted a signature with no allowed signers")
		}
	})

	mustGit(t, dir, "config", "user.signingkey", "/nonexistent/key.pub")

	t.Run("sign=true fails when the key cannot be loaded", func(t *testing.T) {
		if _, err := client.WriteChainRoot(ctx, []byte(`{}`), "x", true); err == nil {
			t.Error("WriteChainRoot signed without a usable key")
		}
	})
}
