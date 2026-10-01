# Roadmap

## Enhancement

- `git zf review reject` should permit to add a md file or a comment for explanation.
- Flags `-m <text>` / `-F <file.md>` should add a comment on the Tracker interface implemented for Forgejo and Redmine only.
- Creating an issue from a external issues tracker, add an entry to create a fresh issue to the tracker.
- `git zf init` installs hooks under the per-worktree git dir when run inside a linked worktree; git reads hooks from the common dir. Run `init` from the main checkout for now.
- AI assistant. See https://github.com/rshdhere/vibecheck
- Implement bug tracking into git repo and by-directionnal syncing with remote trackers like https://github.com/git-bug/git-bug does without the same feature, git-zf keeps his specific workflow.


## To be discuss

- Add the trackers for “Under development”, “To be reviewed/tested”, “Under review”, “Test/review” to the redimne conf to automate the code review workflow.
