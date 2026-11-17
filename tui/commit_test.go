package tui

import (
	"strings"
	"testing"

	"github.com/piprim/git-zf/git"
)

func mergePageView(t *testing.T, opt *CommitOption) string {
	t.Helper()

	f := NewForm(MergeOptionsGroup(opt))
	f.Init()

	return f.View()
}

func TestMergeOptionsGroup(t *testing.T) {
	t.Parallel()

	authors := []string{"Ann <a@x>", "Bob <b@x>"}

	t.Run("shows author, no-verify, signoff in the commit order", func(t *testing.T) {
		t.Parallel()

		view := mergePageView(t, &CommitOption{Authors: authors})

		author := strings.Index(view, "Author:")
		noVerify := strings.Index(view, "--no-verify")
		signoff := strings.Index(view, "--signoff")
		if author < 0 || noVerify < 0 || signoff < 0 {
			t.Fatalf("missing field (author=%d no-verify=%d signoff=%d) in:\n%s", author, noVerify, signoff, view)
		}
		if author >= noVerify || noVerify >= signoff {
			t.Errorf("order author=%d no-verify=%d signoff=%d, want ascending", author, noVerify, signoff)
		}
	})

	t.Run("hides the staging and amend options", func(t *testing.T) {
		t.Parallel()

		view := mergePageView(t, &CommitOption{Authors: authors})

		for _, flag := range []string{"--all", "--include-untracked", "--amend", "--allow-empty"} {
			if strings.Contains(view, flag) {
				t.Errorf("merge page shows %s:\n%s", flag, view)
			}
		}
	})

	t.Run("each confirm is bound to its own field", func(t *testing.T) {
		t.Parallel()

		// The rendered page is the same either way without colors, so read
		// the value each confirm is bound to.
		opt := CommitOption{Authors: authors, CommitOptions: git.CommitOptions{NoVerify: true}}

		if got := noVerifyConfirm(&opt).GetValue(); got != true {
			t.Errorf("no-verify confirm = %v, want true", got)
		}
		if got := signoffConfirm(&opt).GetValue(); got != false {
			t.Errorf("signoff confirm = %v, want false", got)
		}
	})
}

func TestAuthorSelect(t *testing.T) {
	t.Parallel()

	t.Run("defaults to the first author", func(t *testing.T) {
		t.Parallel()

		opt := CommitOption{Authors: []string{"Ann <a@x>", "Bob <b@x>"}}
		authorSelect(&opt)

		if opt.Author != "Ann <a@x>" {
			t.Errorf("Author = %q, want Ann <a@x>", opt.Author)
		}
	})

	t.Run("keeps a preset author", func(t *testing.T) {
		t.Parallel()

		opt := CommitOption{Authors: []string{"Ann <a@x>", "Bob <b@x>"}}
		opt.Author = "Bob <b@x>"
		authorSelect(&opt)

		if opt.Author != "Bob <b@x>" {
			t.Errorf("Author = %q, want Bob <b@x>", opt.Author)
		}
	})

	t.Run("no authors leaves the author empty", func(t *testing.T) {
		t.Parallel()

		opt := CommitOption{}
		authorSelect(&opt)

		if opt.Author != "" {
			t.Errorf("Author = %q, want empty", opt.Author)
		}
	})

	t.Run("no authors shows a placeholder option", func(t *testing.T) {
		t.Parallel()

		if view := mergePageView(t, &CommitOption{}); !strings.Contains(view, "(no authors found)") {
			t.Errorf("no placeholder option in:\n%s", view)
		}
	})
}
