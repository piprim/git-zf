package convert

import (
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/tui"
)

// CommitOptionsFromTUI returns the [git.CommitOptions] from [tui.CommitOption].
func CommitOptionsFromTUI(opts tui.CommitOption) git.CommitOptions {
	return git.CommitOptions{
		All: opts.All, Amend: opts.Amend, NoVerify: opts.NoVerify,
		Signoff: opts.Signoff, AllowEmpty: opts.AllowEmpty,
		IncludeUntracked: opts.IncludeUntracked,
		Author:           opts.Author,
	}
}
