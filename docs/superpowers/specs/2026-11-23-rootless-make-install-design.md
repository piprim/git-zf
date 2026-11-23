# `make install` without root

## Intent

Installing git-zf from source must not need root. Today `sudo make install`
runs `install: build` as root: the Go build itself runs as root (root-owned
`./bin`, root's `PATH` may lack the Go toolchain), only because the copy
targets `$(git --exec-path)`, a root-owned directory.

git runs any `git-zf` it finds on `PATH`, so the binary does not have to live
in the exec path.

## Goals

- `make install` works as a plain user on Linux, macOS and Windows (Git Bash).
- After installing, `make install` says whether an older binary will run
  instead of the new one.

## Non-goals

- `git zf install` / `git zf uninstall` (the commands) are unchanged: they
  keep copying to git's exec path, for release archives. Their missing `.exe`
  on Windows is a known flaw, left for later.
- No `PREFIX` / `DESTDIR` support.

## Approaches

1. **Keep the exec path, split build from copy** so `sudo` only copies.
   Still needs `sudo`; rejected, it is not what was asked.
2. **`PREFIX ?= ~/.local`, copy to `$(PREFIX)/bin`.** Conventional on Unix,
   but that directory is not on `PATH` on Windows and the Makefile would have
   to add `.exe` itself.
3. **`go install` with the project's ldflags** (picked). Go's bin directory
   (`GOBIN`, else `GOPATH/bin`) is user-owned everywhere, Go adds `.exe` on
   Windows, and the Go installer puts that directory on `PATH`. Trade-off: it
   relies on Go's bin directory being on `PATH`, which the check below reports.

## Design

`Makefile`:

- `install` no longer depends on `build`. It runs
  `CGO_ENABLED=0 go install $(LDFLAGS) .`, then prints the destination, read
  with `go list -f '{{.Target}}' .` (the full path, `.exe` included).
- After installing, two independent checks, so one run reports everything
  that stands between the user and the new binary:
  1. **The exec path.** `$(git --exec-path)/git-zf` or `git-zf.exe` exists and
     is not the destination: git runs that one before anything on `PATH`.
     Warn, with the command that removes it (`sudo rm "<path>"`; printed as
     is on Windows too, where there is no `sudo`).
  2. **`PATH`.** `command -v git-zf` finds nothing: warn that the
     destination's directory is not on `PATH`. Else it finds a file that is
     not the destination: warn that this one comes first, with its path.
     The empty case is tested first.
- Two files are compared by identity, `[ "$$a" -ef "$$b" ]`, never as
  strings: on Windows `go list` prints `C:\...\git-zf.exe` and `command -v`
  an MSYS path. `-ef` is checked to work in dash, this machine's `/bin/sh`.
- Every path is double-quoted (the exec path holds a space on Windows) and
  printed with `printf '%s\n'`, never `echo` (dash's `echo` reads
  backslashes as escapes).
- `uninstall` removes the file `go list -f '{{.Target}}' .` names.
- The `COPY` / `RM` / `GIT_EXEC_PATH` variables and the `ifeq ($(OS),Windows_NT)`
  branch are removed; `uninstall` is added to `.PHONY`.
- `build` (`./bin/git-zf`) is unchanged.

Docs, `sudo` dropped from the source install:

- `README.md` and `website/docs/getting-started/install.md`, "From source":
  `make install` alone (the `make` line before it goes: `install` no longer
  depends on `build`, and it would compile twice), one line on where the
  binary lands, one on Windows (run it from Git Bash).
- The website's intro, "Each ends with the binary copied into git's exec
  path", is reworded: only the release archive does; the other two put the
  binary in Go's bin directory, on `PATH`.
- The "omit `sudo` if `git --exec-path` is writable" note and "remove the
  binary with `git zf uninstall`" move under the release archive, the only
  way they still describe. `git zf uninstall` looks in the exec path alone
  and fails on a binary installed the other ways: the source install says
  `make uninstall`, the `go install` one says to delete the file from Go's
  bin directory.
- `CLAUDE.md`: the `make install` comment, and the `./bin/git-zf install`
  line says it copies to git's exec path and needs `sudo` there.

## Calls made by the author (not decided in conversation)

- The check **warns and exits 0**: the install did succeed, and removing a
  root-owned file is the user's decision.
- The `go install github.com/piprim/git-zf@latest` section of the docs drops
  its `sudo git-zf install` line: after `go install` the binary is already on
  `PATH`.
- No automated test: the check is a few lines of shell in a Makefile. It is
  verified by hand on this machine, which holds both cases (a copy in
  `/usr/lib/git-core` and one in `/usr/local/bin`). Windows is not verified.

## Left to the builder

The exact wording of the warnings.
