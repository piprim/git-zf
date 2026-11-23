# Rootless `make install` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `make install` installs git-zf as a plain user and reports any older binary that would run instead.

**Architecture:** The `install` target runs `go install` with the project's ldflags, then two independent shell checks (git's exec path, then `PATH`). No Go code changes.

**Tech Stack:** GNU make, POSIX sh (dash here), the Go toolchain.

**Spec:** `docs/superpowers/specs/2026-11-23-rootless-make-install-design.md`

## Global Constraints

- `git zf install` / `git zf uninstall` (Go code) are not touched.
- No `PREFIX` / `DESTDIR`.
- Files are compared with `[ a -ef b ]`, never as strings.
- Every shell path is double-quoted; output goes through `printf`, never `echo`.
- The checks warn on stderr and exit 0.
- No commit: the user commits.

## Review Focus

No Go test covers a Makefile; each line is checked by hand in Task 1.

1. An old binary in git's exec path → warning naming it, with `sudo rm "<path>"`.
2. Another `git-zf` earlier on `PATH` → warning naming it, in the same run as 1.
3. Go's bin directory missing from `PATH` → "not on PATH" warning, not the "comes first" one.
4. Nothing shadows → no warning at all.
5. `make uninstall` removes the installed file and nothing else.

---

### Task 1: Makefile

**Files:**
- Modify: `Makefile`

- [ ] **Step 1: Rewrite `install` and `uninstall`**

Remove `COPY`, `RM`, `GIT_EXEC_PATH` and the `ifeq ($(OS),Windows_NT)` block. `install` loses its `build` prerequisite. Add `uninstall` to `.PHONY`.

```make
install:
	CGO_ENABLED=0 go install $(LDFLAGS) .
	@dst="$$(go list -f '{{.Target}}' .)"; \
	printf 'Installed %s\n' "$$dst"; \
	exec_dir="$$(git --exec-path)"; \
	for f in "$$exec_dir/$(TARGET)" "$$exec_dir/$(TARGET).exe"; do \
		if [ -f "$$f" ] && ! [ "$$f" -ef "$$dst" ]; then \
			printf 'warning: git runs %s first; remove it with: sudo rm "%s"\n' "$$f" "$$f" >&2; \
		fi; \
	done; \
	found="$$(command -v $(TARGET) 2>/dev/null || true)"; \
	if [ -z "$$found" ]; then \
		printf 'warning: %s is not on PATH\n' "$$(dirname "$$dst")" >&2; \
	elif ! [ "$$found" -ef "$$dst" ]; then \
		printf 'warning: %s comes before %s on PATH\n' "$$found" "$$dst" >&2; \
	fi

uninstall:
	rm -f "$$(go list -f '{{.Target}}' .)"
```

- [ ] **Step 2: Verify on this machine**

Run: `mise exec -- make install`
Expected: `Installed /home/pi/bin/go/git-zf`, a warning for `/usr/lib/git-core/git-zf`, a warning that `/usr/local/bin/git-zf` comes first (Review Focus 1, 2), exit 0.

Run: `PATH="$(dirname "$(command -v go)"):/usr/bin:/bin" make install`
Expected: the exec-path warning and `/home/pi/bin/go is not on PATH` (Review Focus 3).

Run: `/home/pi/bin/go/git-zf version` → prints the version injected by ldflags.

Review Focus 4 cannot be reached without removing root-owned files: read the branch instead, and say so in the report.

Run: `mise exec -- make uninstall && ls /home/pi/bin/go/git-zf` → no such file (Review Focus 5). Then `mise exec -- make install` again to leave the binary installed.

### Task 2: Docs

**Files:**
- Modify: `README.md` (Install section)
- Modify: `website/docs/getting-started/install.md`
- Modify: `CLAUDE.md:9-10`

- [ ] **Step 1: Apply the spec's "Docs" section**, each bullet:

- From source: `make install` alone (drop the `make` line and `sudo`), where the binary lands (Go's bin directory, which must be on `PATH`), Windows from Git Bash, remove with `make uninstall`.
- `go install`: drop the `sudo git-zf install` line; remove by deleting the file from Go's bin directory.
- Release archive: keeps `sudo ./git-zf install`; the "omit `sudo` if the exec path is writable" note and `git zf uninstall` move under it.
- Website intro: no longer says each way copies into git's exec path.
- `CLAUDE.md`: `make install` comment says `go install`, no root; `./bin/git-zf install` comment says it copies to git's exec path, with `sudo`.

- [ ] **Step 2: Verify**

Run: `grep -n "sudo" README.md website/docs/getting-started/install.md`
Expected: only the release-archive lines and the unrelated bash-completion line.
