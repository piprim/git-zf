package review

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/internal/gittest"
	reviewpkg "github.com/piprim/git-zf/review"
	"github.com/piprim/git-zf/review/reviewtest"
	"github.com/piprim/git-zf/store"
)

// writeLegacyBlob points refs/zf/reviews/<slug> at a JSON blob, as git-zf did
// before reviews were commit chains.
func writeLegacyBlob(t *testing.T, dir, slug string) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", "-C", dir, "hash-object", "-w", "--stdin")
	cmd.Stdin = strings.NewReader(`{"status":"in_review","round":1,"feature_sha":"x"}`)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("hash-object: %v", err)
	}

	upd := exec.CommandContext(t.Context(), "git", "-C", dir,
		"update-ref", "refs/zf/reviews/"+slug, strings.TrimSpace(string(out)))
	if b, err := upd.CombinedOutput(); err != nil {
		t.Fatalf("update-ref: %v\n%s", err, b)
	}
}

func TestReviewRequest_ReplacesLegacyBlob(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	rig := newReviewE2ERig(t)
	writeLegacyBlob(t, rig.dir, "77")

	err := runReviewRequest(ctx, rig.deps(), "77")

	t.Run("request succeeds over a legacy blob", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runReviewRequest: %v", err)
		}
	})

	t.Run("the ref is now a chain in review at round 1", func(t *testing.T) {
		st, lErr := reviewpkg.Load(ctx, rig.client, "77")
		if lErr != nil || st == nil {
			t.Fatalf("Load = %v, %v", st, lErr)
		}
		if st.Status != reviewpkg.StatusInReview || st.Round != 1 {
			t.Errorf("state = %+v", st)
		}
	})
}

func TestReviewRequest_LegacyBlobKeepsReviewerCommits(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	rig := newReviewE2ERig(t)
	writeLegacyBlob(t, rig.dir, "77")

	// The reviewer left a commit on 77@review that the feature branch lacks.
	if err := rig.client.RunGitAt(ctx, rig.dir, "checkout", "-q", "-b", "77@review", "77@feat@my-feature"); err != nil {
		t.Fatalf("checkout 77@review: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rig.dir, "nit.txt"), []byte("nit\n"), 0o644); err != nil {
		t.Fatalf("write nit.txt: %v", err)
	}
	for _, args := range [][]string{
		{"add", "nit.txt"},
		{"commit", "-q", "-m", "fix: reviewer nit"},
		{"checkout", "-q", "main"},
	} {
		if err := rig.client.RunGitAt(ctx, rig.dir, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}

	err := runReviewRequest(ctx, rig.deps(), "77")

	t.Run("request is refused while reviewer commits are unincorporated", func(t *testing.T) {
		if err == nil || !strings.Contains(err.Error(), "unincorporated") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("the message says to merge by hand, not to run review sync", func(t *testing.T) {
		if err == nil {
			t.Fatal("no error")
		}
		msg := err.Error()
		if !strings.Contains(msg, "77@review") || !strings.Contains(msg, "by hand") || strings.Contains(msg, "git zf review sync") {
			t.Errorf("message:\n%s", msg)
		}
	})

	t.Run("the legacy blob and the review branch are left alone", func(t *testing.T) {
		if _, lErr := reviewpkg.Load(ctx, rig.client, "77"); !errors.Is(lErr, reviewpkg.ErrLegacyReview) {
			t.Errorf("Load err = %v, want ErrLegacyReview", lErr)
		}
		if exists, _ := rig.client.BranchExists("77@review"); !exists {
			t.Error("77@review was deleted")
		}
	})
}

func TestReviewList_HidesClosedReviews(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	rig := newReviewE2ERig(t)

	reviewtest.Seed(t, rig.client, "77", reviewpkg.StatusApproved, 1, "f1")
	reviewtest.Seed(t, rig.client, "78", reviewpkg.StatusInReview, 1, "f2")
	if err := reviewpkg.Append(ctx, rig.client, "77", &reviewpkg.Op{Type: reviewpkg.OpClose}, false); err != nil {
		t.Fatalf("append close: %v", err)
	}

	rig.stdout.Reset()
	err := runReviewList(ctx, rig.deps())
	out := rig.stdout.String()

	t.Run("list succeeds", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runReviewList: %v", err)
		}
	})
	t.Run("the open review is listed", func(t *testing.T) {
		if !strings.Contains(out, "78") {
			t.Errorf("output:\n%s", out)
		}
	})
	t.Run("the closed review is hidden", func(t *testing.T) {
		if strings.Contains(out, "77") {
			t.Errorf("output:\n%s", out)
		}
	})
}

func TestReviewRequest_AfterClose_StartsNextRound(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	rig := newReviewE2ERig(t)

	reviewtest.Seed(t, rig.client, "77", reviewpkg.StatusApproved, 1, "f1")
	if err := reviewpkg.Append(ctx, rig.client, "77", &reviewpkg.Op{Type: reviewpkg.OpClose}, false); err != nil {
		t.Fatalf("append close: %v", err)
	}

	err := runReviewRequest(ctx, rig.deps(), "77")

	t.Run("request on a closed review succeeds", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runReviewRequest: %v", err)
		}
	})
	t.Run("the review reopens at round 2", func(t *testing.T) {
		st, lErr := reviewpkg.Load(ctx, rig.client, "77")
		if lErr != nil || st == nil {
			t.Fatalf("Load = %v, %v", st, lErr)
		}
		if st.Closed || st.Round != 2 || st.Status != reviewpkg.StatusInReview {
			t.Errorf("state = %+v", st)
		}
	})
}

func TestReviewApprove_RequireSigned(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	rig := newReviewE2ERig(t)
	bringRigToInReview(t, rig)
	rig.cfg.Review.RequireSigned = true

	// No usable key yet: signing must fail.
	for _, kv := range [][2]string{{"gpg.format", "ssh"}, {"user.signingkey", "/nonexistent/key.pub"}} {
		if err := rig.client.RunGitAt(ctx, rig.dir, "config", kv[0], kv[1]); err != nil {
			t.Fatalf("git config %s: %v", kv[0], err)
		}
	}
	failErr := runReviewApprove(ctx, rig.deps(), "77")

	t.Run("approve fails when the op cannot be signed", func(t *testing.T) {
		if failErr == nil {
			t.Fatal("approve succeeded without a usable signing key")
		}
	})

	t.Run("no approve op is written on failure", func(t *testing.T) {
		st, err := reviewpkg.Load(ctx, rig.client, "77")
		if err != nil || st == nil {
			t.Fatalf("Load = %v, %v", st, err)
		}
		if st.Status != reviewpkg.StatusInReview || len(st.Approvals) != 0 {
			t.Errorf("state = %+v", st)
		}
	})

	gittest.SSHSigner(t, rig.dir)
	okErr := runReviewApprove(ctx, rig.deps(), "77")

	t.Run("approve succeeds with a signing key", func(t *testing.T) {
		if okErr != nil {
			t.Fatalf("runReviewApprove: %v", okErr)
		}
	})

	t.Run("the approve op is signed although commit.gpgsign is false", func(t *testing.T) {
		st, err := reviewpkg.Load(ctx, rig.client, "77")
		if err != nil || st == nil || len(st.Approvals) != 1 {
			t.Fatalf("Load = %+v, %v", st, err)
		}
		if got := reviewpkg.SignatureState(ctx, rig.client, st.Approvals[0].Commit); got != reviewpkg.SigVerified {
			t.Errorf("SignatureState = %q", got)
		}
	})
}

func TestReviewStatus_ShowsSignatureState(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	rig := newReviewE2ERig(t)
	bringRigToInReview(t, rig)
	if err := runReviewApprove(ctx, rig.deps(), "77"); err != nil {
		t.Fatalf("runReviewApprove: %v", err)
	}

	rig.stdout.Reset()
	err := runReviewStatus(ctx, rig.deps(), "77")
	out := rig.stdout.String()

	t.Run("status succeeds", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runReviewStatus: %v", err)
		}
	})
	t.Run("the approval is listed as unsigned", func(t *testing.T) {
		if !strings.Contains(out, "Round 1 approvals:") || !strings.Contains(out, reviewpkg.SigNone) {
			t.Errorf("output:\n%s", out)
		}
	})
}

func TestReviewApprove_RecordsApprovedSHA(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	t.Run("without a review branch the approval covers the submitted commit", func(t *testing.T) {
		rig := newReviewE2ERig(t)
		bringRigToInReview(t, rig)

		if err := runReviewApprove(ctx, rig.deps(), "77"); err != nil {
			t.Fatalf("runReviewApprove: %v", err)
		}
		st, err := reviewpkg.Load(ctx, rig.client, "77")
		if err != nil || st == nil || len(st.Approvals) != 1 {
			t.Fatalf("Load = %+v, %v", st, err)
		}
		if st.Approvals[0].ApprovedSHA != st.FeatureSHA || st.Approvals[0].HasCommits {
			t.Errorf("approval = %+v, feature_sha = %s", st.Approvals[0], st.FeatureSHA)
		}
	})

	t.Run("with reviewer commits the approval covers the review branch tip", func(t *testing.T) {
		rig := newReviewE2ERig(t)
		bringRigToInReview(t, rig)

		if err := rig.client.RunGitAt(ctx, rig.dir, "checkout", "-q", "-b", "77@review", "77@feat@my-feature"); err != nil {
			t.Fatalf("checkout 77@review: %v", err)
		}
		if err := os.WriteFile(filepath.Join(rig.dir, "nit.txt"), []byte("nit\n"), 0o644); err != nil {
			t.Fatalf("write nit.txt: %v", err)
		}
		for _, args := range [][]string{
			{"add", "nit.txt"},
			{"commit", "-q", "-m", "fix: reviewer nit"},
			{"checkout", "-q", "main"},
		} {
			if err := rig.client.RunGitAt(ctx, rig.dir, args...); err != nil {
				t.Fatalf("git %v: %v", args, err)
			}
		}
		tip, err := rig.client.ResolveRef("refs/heads/77@review")
		if err != nil {
			t.Fatalf("resolve 77@review: %v", err)
		}

		if err := runReviewApprove(ctx, rig.deps(), "77"); err != nil {
			t.Fatalf("runReviewApprove: %v", err)
		}
		st, err := reviewpkg.Load(ctx, rig.client, "77")
		if err != nil || st == nil || len(st.Approvals) != 1 {
			t.Fatalf("Load = %+v, %v", st, err)
		}
		if st.Approvals[0].ApprovedSHA != tip.String() || !st.Approvals[0].HasCommits {
			t.Errorf("approval = %+v, review tip = %s", st.Approvals[0], tip)
		}
	})
}

// tipOp decodes op.json of the tip of refs/zf/reviews/<slug> in dir.
func tipOp(t *testing.T, dir, slug string) reviewpkg.Op {
	t.Helper()

	out, err := exec.CommandContext(t.Context(), "git", "-C", dir,
		"cat-file", "blob", "refs/zf/reviews/"+slug+":op.json").Output()
	if err != nil {
		t.Fatalf("cat-file op.json: %v", err)
	}
	op, ok := reviewpkg.DecodeOp("tip", nil, out)
	if !ok {
		t.Fatalf("malformed tip op: %s", out)
	}

	return op
}

func TestReviewApprove_OpCarriesRound(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	rig := newReviewE2ERig(t)
	bringRigToInReview(t, rig)

	err := runReviewApprove(ctx, rig.deps(), "77")

	t.Run("approve succeeds", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runReviewApprove: %v", err)
		}
	})

	t.Run("the approve op records round 1", func(t *testing.T) {
		raw, cErr := exec.CommandContext(ctx, "git", "-C", rig.dir,
			"cat-file", "blob", "refs/zf/reviews/77:op.json").Output()
		if cErr != nil {
			t.Fatalf("cat-file op.json: %v", cErr)
		}
		if !strings.Contains(string(raw), `"type":"approve"`) || !strings.Contains(string(raw), `"round":1`) {
			t.Errorf("op.json = %s", raw)
		}
	})
}

func TestReviewStart_RecordsReviewer(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	rig := newReviewE2ERig(t)
	bringRigToInReview(t, rig)

	err := runReviewStart(ctx, rig.deps(), "77")

	t.Run("start succeeds", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runReviewStart: %v", err)
		}
	})

	t.Run("the chain names the rig's identity as reviewer", func(t *testing.T) {
		st, lErr := reviewpkg.Load(ctx, rig.client, "77")
		if lErr != nil || st == nil {
			t.Fatalf("Load = %v, %v", st, lErr)
		}
		if st.Reviewer != "Test User <test@test.com>" {
			t.Errorf("Reviewer = %q", st.Reviewer)
		}
	})

	t.Run("the start op records round 1", func(t *testing.T) {
		if op := tipOp(t, rig.dir, "77"); op.Type != reviewpkg.OpStart || op.Round != 1 {
			t.Errorf("tip op = %+v", op)
		}
	})
}

// approvedRig is a rig whose review of 77 is approved (unsigned) and open.
func approvedRig(t *testing.T) (*reviewE2ERig, *scriptedReviewPrompter) {
	t.Helper()

	rig := newReviewE2ERig(t)
	feature, err := rig.client.ResolveRef("refs/heads/77@feat@my-feature")
	if err != nil {
		t.Fatalf("resolve feature: %v", err)
	}
	reviewtest.Seed(t, rig.client, "77", reviewpkg.StatusApproved, 1, feature.String())

	return rig, &scriptedReviewPrompter{Branch: &store.BranchRow{IssueSlug: "77", BranchName: "77@feat@my-feature"}}
}

func TestReviewRequest_ApprovedReviewWithRequireSigned(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	t.Run("flag on: the approved review is offered and a request starts round 2", func(t *testing.T) {
		rig, p := approvedRig(t)
		rig.cfg.Review.RequireSigned = true

		if err := runReviewRequestInteractive(ctx, rig.deps(), p); err != nil {
			t.Fatalf("runReviewRequestInteractive: %v", err)
		}
		if strings.Contains(rig.stdout.String(), "No in-progress branches") {
			t.Errorf("the approved review was not offered:\n%s", rig.stdout)
		}
		st, err := reviewpkg.Load(ctx, rig.client, "77")
		if err != nil || st == nil {
			t.Fatalf("Load = %v, %v", st, err)
		}
		if st.Status != reviewpkg.StatusInReview || st.Round != 2 {
			t.Errorf("state = %+v", st)
		}
	})

	t.Run("flag off: the approved review is not offered", func(t *testing.T) {
		rig, p := approvedRig(t)

		if err := runReviewRequestInteractive(ctx, rig.deps(), p); err != nil {
			t.Fatalf("runReviewRequestInteractive: %v", err)
		}
		if !strings.Contains(rig.stdout.String(), "No in-progress branches") {
			t.Errorf("output:\n%s", rig.stdout)
		}
		st, err := reviewpkg.Load(ctx, rig.client, "77")
		if err != nil || st == nil {
			t.Fatalf("Load = %v, %v", st, err)
		}
		if st.Status != reviewpkg.StatusApproved || st.Round != 1 {
			t.Errorf("state = %+v", st)
		}
	})
}

func TestReviewReject_RequireSignedDoesNotSign(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	rig := newReviewE2ERig(t)
	bringRigToInReview(t, rig)
	rig.cfg.Review.RequireSigned = true

	// A signing key that cannot be used: a signed write would fail.
	for _, kv := range [][2]string{{"gpg.format", "ssh"}, {"user.signingkey", "/nonexistent/key.pub"}} {
		if err := rig.client.RunGitAt(ctx, rig.dir, "config", kv[0], kv[1]); err != nil {
			t.Fatalf("git config %s: %v", kv[0], err)
		}
	}

	_, err := runReviewReject(ctx, rig.deps(), "77", "needs work")

	t.Run("reject succeeds", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runReviewReject: %v", err)
		}
	})

	t.Run("an unsigned reject op is written", func(t *testing.T) {
		op := tipOp(t, rig.dir, "77")
		if op.Type != reviewpkg.OpReject {
			t.Fatalf("tip op = %+v", op)
		}
		tip, rErr := rig.client.ResolveRef("refs/zf/reviews/77")
		if rErr != nil {
			t.Fatalf("resolve tip: %v", rErr)
		}
		if got := reviewpkg.SignatureState(ctx, rig.client, tip.String()); got != reviewpkg.SigNone {
			t.Errorf("SignatureState = %q", got)
		}
	})
}

func TestReviewRequest_ReplacesLegacyBlobOnRemote(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	rig := newReviewE2ERigWithOrigin(t)
	originOut, err := exec.CommandContext(ctx, "git", "-C", rig.dir, "remote", "get-url", "origin").Output()
	if err != nil {
		t.Fatalf("get origin url: %v", err)
	}
	originDir := strings.TrimSpace(string(originOut))

	writeLegacyBlob(t, rig.dir, "77")
	if err := rig.client.RunGitAt(ctx, rig.dir, "push", "-q", "origin", "refs/zf/reviews/77"); err != nil {
		t.Fatalf("push legacy blob: %v", err)
	}

	reqErr := runReviewRequest(ctx, rig.deps(), "77")

	t.Run("request succeeds", func(t *testing.T) {
		if reqErr != nil {
			t.Fatalf("runReviewRequest: %v", reqErr)
		}
	})

	t.Run("the origin's ref is a commit chain in review", func(t *testing.T) {
		kind, kErr := exec.CommandContext(ctx, "git", "-C", originDir, "cat-file", "-t", "refs/zf/reviews/77").Output()
		if kErr != nil || strings.TrimSpace(string(kind)) != "commit" {
			t.Fatalf("origin ref type = %q, %v", kind, kErr)
		}
		st := readRemoteReviewRef(t, originDir, "77")
		if st == nil || st.Status != reviewpkg.StatusInReview || st.Round != 1 {
			t.Errorf("origin state = %+v", st)
		}
	})
}

// inReviewWithMalformedOp brings 77 in review and appends a commit whose
// op.json is not JSON.
func inReviewWithMalformedOp(t *testing.T) *reviewE2ERig {
	t.Helper()

	rig := newReviewE2ERig(t)
	bringRigToInReview(t, rig)
	if _, err := rig.client.AppendChainCommit(t.Context(), git.ReviewRefs, "77", []byte("not json"), "junk", false); err != nil {
		t.Fatalf("AppendChainCommit: %v", err)
	}
	rig.stderr.Reset()

	return rig
}

func TestReviewCommands_PrintMalformedOpWarnings(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	t.Run("status prints the warning", func(t *testing.T) {
		rig := inReviewWithMalformedOp(t)
		if err := runReviewStatus(ctx, rig.deps(), "77"); err != nil {
			t.Fatalf("runReviewStatus: %v", err)
		}
		if !strings.Contains(rig.stderr.String(), "malformed op") {
			t.Errorf("stderr:\n%s", rig.stderr)
		}
	})

	t.Run("approve prints the warning", func(t *testing.T) {
		rig := inReviewWithMalformedOp(t)
		if err := runReviewApprove(ctx, rig.deps(), "77"); err != nil {
			t.Fatalf("runReviewApprove: %v", err)
		}
		if !strings.Contains(rig.stderr.String(), "malformed op") {
			t.Errorf("stderr:\n%s", rig.stderr)
		}
	})
}

func TestReviewRequest_LegacyRemoteDeleteRefusedHint(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	rig := newReviewE2ERigWithOrigin(t)
	originOut, err := exec.CommandContext(ctx, "git", "-C", rig.dir, "remote", "get-url", "origin").Output()
	if err != nil {
		t.Fatalf("get origin url: %v", err)
	}
	originDir := strings.TrimSpace(string(originOut))

	writeLegacyBlob(t, rig.dir, "77")
	if err := rig.client.RunGitAt(ctx, rig.dir, "push", "-q", "origin", "refs/zf/reviews/77"); err != nil {
		t.Fatalf("push legacy blob: %v", err)
	}

	// The origin refuses every ref deletion: the old blob stays there.
	hook := "#!/bin/sh\nwhile read old new ref; do\n" +
		"  case $new in 0000000000000000000000000000000000000000) exit 1;; esac\ndone\n"
	if err := os.WriteFile(filepath.Join(originDir, "hooks", "pre-receive"), []byte(hook), 0o755); err != nil {
		t.Fatalf("write hook: %v", err)
	}

	reqErr := runReviewRequest(ctx, rig.deps(), "77")

	t.Run("request succeeds locally", func(t *testing.T) {
		if reqErr != nil {
			t.Fatalf("runReviewRequest: %v", reqErr)
		}
	})

	t.Run("the push warning says how to delete the old remote ref", func(t *testing.T) {
		want := "git push origin --delete refs/zf/reviews/77"
		if !strings.Contains(rig.stderr.String(), want) {
			t.Errorf("stderr lacks %q:\n%s", want, rig.stderr)
		}
	})
}

func TestReviewStatus_ShowsSignerOfVerifiedApproval(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	rig := newReviewE2ERig(t)
	bringRigToInReview(t, rig)
	gittest.SSHSigner(t, rig.dir)
	rig.cfg.Review.RequireSigned = true
	if err := runReviewApprove(ctx, rig.deps(), "77"); err != nil {
		t.Fatalf("runReviewApprove: %v", err)
	}

	rig.stdout.Reset()
	err := runReviewStatus(ctx, rig.deps(), "77")
	out := rig.stdout.String()

	t.Run("status succeeds", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runReviewStatus: %v", err)
		}
	})
	t.Run("the verified approval names its signer", func(t *testing.T) {
		if !strings.Contains(out, reviewpkg.SigVerified) || !strings.Contains(out, "signed by signer@test.com") {
			t.Errorf("output:\n%s", out)
		}
	})
}

func TestReviewStart_WarnsWhenReviewerCannotBeRecorded(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	rig := newReviewE2ERig(t)
	bringRigToInReview(t, rig)

	// commit.gpgsign with a key that cannot be loaded: writing the start op fails.
	for _, kv := range [][2]string{{"commit.gpgsign", "true"}, {"gpg.format", "ssh"}, {"user.signingkey", "/nonexistent/key.pub"}} {
		if err := rig.client.RunGitAt(ctx, rig.dir, "config", kv[0], kv[1]); err != nil {
			t.Fatalf("git config %s: %v", kv[0], err)
		}
	}
	rig.stderr.Reset()

	err := runReviewStart(ctx, rig.deps(), "77")

	t.Run("start still succeeds", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runReviewStart: %v", err)
		}
	})
	t.Run("a warning says the reviewer was not recorded", func(t *testing.T) {
		if !strings.Contains(rig.stderr.String(), "warning: record reviewer:") {
			t.Errorf("stderr:\n%s", rig.stderr)
		}
	})
}

func TestTrack_Reviewer_ClosedReviewIsNoOpenReview(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	rig := newReviewE2ERig(t)
	reviewtest.Seed(t, rig.client, "77", reviewpkg.StatusApproved, 1, "f1")
	if err := reviewpkg.Append(ctx, rig.client, "77", &reviewpkg.Op{Type: reviewpkg.OpClose}, false); err != nil {
		t.Fatalf("append close: %v", err)
	}
	if err := rig.client.RunGitAt(ctx, rig.dir, "checkout", "-q", "-b", "77@review"); err != nil {
		t.Fatalf("checkout review branch: %v", err)
	}

	err := runTrack(ctx, rig.deps())

	t.Run("track reports no open review", func(t *testing.T) {
		if err == nil || !strings.Contains(err.Error(), "no open review") {
			t.Errorf("err = %v", err)
		}
	})
}

// roundLine returns the "Round <n>" line of a `review status` history.
func roundLine(out string, round string) string {
	for line := range strings.SplitSeq(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "Round "+round+" ") {
			return line
		}
	}

	return ""
}

func TestReviewStatus_ReconcilesTheNewestRound(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	rig := newReviewE2ERig(t)

	// Round 1 is rejected, round 2 is requested: two rows in the store.
	bringRigToInReview(t, rig)
	if _, err := runReviewReject(ctx, rig.deps(), "77", "fix the tests"); err != nil {
		t.Fatalf("runReviewReject: %v", err)
	}
	if err := runReviewRequest(ctx, rig.deps(), "77"); err != nil {
		t.Fatalf("runReviewRequest round 2: %v", err)
	}

	// Round 2 is approved on the reviewer's machine: the chain knows, this
	// clone's store still says in_review.
	approve := &reviewpkg.Op{Type: reviewpkg.OpApprove, ApprovedSHA: "f2", Round: 2}
	if err := reviewpkg.Append(ctx, rig.client, "77", approve, false); err != nil {
		t.Fatalf("append approve: %v", err)
	}

	rig.stdout.Reset()
	if err := runReviewStatus(ctx, rig.deps(), "77"); err != nil {
		t.Fatalf("runReviewStatus: %v", err)
	}
	out := rig.stdout.String()

	rows, err := rig.store.ListReviews(ctx, "77")
	if err != nil || len(rows) != 2 {
		t.Fatalf("ListReviews = %d rows, %v", len(rows), err)
	}
	round2, round1 := rows[0], rows[1] // newest first

	t.Run("the store row of round 2 takes the chain's status", func(t *testing.T) {
		if round2.Round != 2 || round2.Status != store.ReviewStatusApproved {
			t.Errorf("round 2 row = %+v", round2)
		}
	})

	t.Run("the store row of round 1 keeps changes_requested", func(t *testing.T) {
		if round1.Round != 1 || round1.Status != store.ReviewStatusChangesRequested {
			t.Errorf("round 1 row = %+v", round1)
		}
	})

	t.Run("the history prints round 2 as approved", func(t *testing.T) {
		if line := roundLine(out, "2"); !strings.Contains(line, string(store.ReviewStatusApproved)) {
			t.Errorf("round 2 line = %q\n%s", line, out)
		}
	})

	t.Run("the history prints round 1 as changes_requested", func(t *testing.T) {
		if line := roundLine(out, "1"); !strings.Contains(line, string(store.ReviewStatusChangesRequested)) {
			t.Errorf("round 1 line = %q\n%s", line, out)
		}
	})
}
