#!/bin/sh
# Manual check of the split screen (git-zf on top, command output below).
# Builds the binary, seeds a throwaway repository whose pre-commit hook reads
# stdin, and prints the steps to go through in a terminal (tmux helps for the
# resize steps). It is a checklist, not an automated test.
#
#   scripts/split-check.sh          # seed, print the steps, open a shell there
#                                   # (it does not start git-zf: step 1 does)
#   scripts/split-check.sh --seed   # seed only, print the directory
set -eu

root=$(cd "$(dirname "$0")/.." && pwd)
(cd "$root" && mise exec -- go build -o ./bin/git-zf .)
zf="$root/bin/git-zf"

repo=$(mktemp -d)
cd "$repo"
git init -q -b master
git config user.name "Split Check"
git config user.email "split@check.invalid"
git commit -q --allow-empty -m "chore: init"
cat > .git/hooks/pre-commit <<'HOOK'
#!/bin/sh
exec < /dev/tty
printf 'hook asks, continue? [y/n] '; read a
[ "$a" = y ] || { echo 'hook: refused'; exit 1; }
echo 'hook: ok'
HOOK
chmod +x .git/hooks/pre-commit
for i in 1 2 3; do echo "$i" > "file$i.txt"; done
git add .

steps="$repo/.git/split-check-steps.txt"
cat > "$steps" <<STEPS

Repository: $repo        Binary: $zf

 1. $zf
      home in the top half, a separator, an empty bottom half.
 2. c, fill the form, submit
      the form opens under the separator; the hook's question appears there;
      y: 'hook: ok' and git's summary stay visible; home shows the new state.
 3. echo x > new.txt; git add new.txt; then c again and answer n
      'hook: refused' and 'error: commit: ...' stay visible, and there is
      NO '[enter] to continue'.
 4. Three screens, each from the home screen:
      i (issues):   /, type, Esc, Esc
      b (branches): enter, Esc, Esc
      l (log):      enter, /, Esc, Esc, then c (the log's command picker), Esc, q
      every hint line is visible, nothing under the separator is disturbed.
      (c on the HOME screen is the commit form: Esc leaves it too.)
 5. Resize taller and wider, then under 20 rows, press a key
      usable, the separator as wide as the terminal; the next screen is full
      screen when the terminal is too small. Resize with a table's row menu
      open: nothing spills under the separator.
 6. While the hook waits for its answer: Ctrl-Z, then fg; then Ctrl-C
      Ctrl-Z: 'Stopped', and the shell scrolls the whole window.
      fg: git-zf resumes, the hook still waits (the top is redrawn at the
      next screen). Ctrl-C: the commit fails, git-zf ENDS, and the whole
      window scrolls. Start it again for the next step.
 7. q
      git-zf's band is gone, the output is at the top with the prompt under
      it; 'seq 1 60' scrolls normally.
 8. printf '[ui]\nsplit = false\n' > .git/.git-zf.toml; $zf
      full screen; an error waits for enter.
 9. rm .git/.git-zf.toml; $zf 2>/tmp/err.log   then b, Esc, q (NOT c)
      full screen, not split, and /tmp/err.log is empty. Do not open the
      commit form here: it draws on stderr, so it would go to the file and
      the terminal would look frozen (Esc gets out).

STEPS

if [ "${1:-}" = --seed ]; then
	echo "$repo"
	exit 0
fi

cat "$steps"
cat <<NEXT
----------------------------------------------------------------------------
Nothing was started: this script only prepares the repository. You are now
in a shell inside it (type 'exit' to leave it).

git-zf scrolls this list away when it starts. Keep it open in another pane:
    less $steps

Then begin with step 1, here:
    $zf

NEXT
exec "${SHELL:-sh}"
