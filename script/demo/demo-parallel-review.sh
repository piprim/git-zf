#!/bin/bash
# ─────────────────────────────────────────────────────────────────────────────
# git-zf parallel code review — live demo script
#
# Prerequisites:
#   curl -s https://raw.githubusercontent.com/paxtonhare/demo-magic/master/demo-magic.sh \
#        -o /usr/local/binz/demo-magic.sh
#   git zf install
#
# Run:
#   chmod +x docs/demo-parallel-review.sh
#   bash docs/demo-parallel-review.sh
#
# Controls:
#   <any key>   advance to next step
#   Ctrl-C      abort
# ─────────────────────────────────────────────────────────────────────────────

. /usr/local/bin/demo-magic.sh

# ── presentation settings ─────────────────────────────────────────────────
TYPE_SPEED=50    # characters per second
PROMPT_TIMEOUT=0 # wait indefinitely at each `wait` call
DEMO_PROMPT="${GREEN}❯ ${COLOR_RESET}"

# ── colour helpers ────────────────────────────────────────────────────────
BOLD=$(tput bold)
RESET=$(tput sgr0)
CYAN=$(tput setaf 6)
YELLOW=$(tput setaf 3)
MAGENTA=$(tput setaf 5)
BLUE=$(tput setaf 4)
GREEN=$(tput setaf 2)
RED=$(tput setaf 1)

banner() {
    local color="$1"
    shift
    local label="$1"
    shift
    local text="$*"
    printf "\n%s%s[ %-10s ] %s%s\n\n" "$color" "$BOLD" "$label" "$text" "$RESET"
}

role() { banner "$CYAN" "$1" "$2"; }          # who is acting
phase() { banner "$YELLOW" "PHASE $1" "$2"; } # phase header

# hint — display what to enter in the next TUI form, printed just before the
# command that opens it so the operator can read it before interacting.
hint() { printf "  %s%s↳ TUI: %s%s\n" "$BLUE" "$BOLD" "$*" "$RESET"; }

# ─────────────────────────────────────────────────────────────────────────────
# SILENT SETUP — creates the demo workspace before the audience sees anything
# ─────────────────────────────────────────────────────────────────────────────
DEMO_DIR=$(mktemp -d)

# bare remote
git init --bare --quiet --initial-branch=main "$DEMO_DIR/origin.git"
_seed=$(mktemp -d)
git -C "$_seed" init --quiet --initial-branch=main
git -C "$_seed" config user.name "Seed"
git -C "$_seed" config user.email "seed@example.com"
git -C "$_seed" config commit.gpgsign false
printf "# Project\n" >"$_seed/README.md"
git -C "$_seed" add README.md
git -C "$_seed" commit --quiet -m "chore: init"
git -C "$_seed" remote add origin "$DEMO_DIR/origin.git"
git -C "$_seed" push --quiet origin main
rm -rf "$_seed"

# developer clones
for name in alice bob; do
    git clone --quiet "$DEMO_DIR/origin.git" "$DEMO_DIR/dev-$name"
    git -C "$DEMO_DIR/dev-$name" config user.name "$(echo "$name" | sed 's/./\u&/')"
    git -C "$DEMO_DIR/dev-$name" config user.email "$name@example.com"
    git -C "$DEMO_DIR/dev-$name" config commit.gpgsign false
done

# reviewer clones (created later but prepared now)
for name in carol dan; do
    git clone --quiet "$DEMO_DIR/origin.git" "$DEMO_DIR/reviewer-$name"
    git -C "$DEMO_DIR/reviewer-$name" config user.name "$(echo "$name" | sed 's/./\u&/')"
    git -C "$DEMO_DIR/reviewer-$name" config user.email "$name@example.com"
    git -C "$DEMO_DIR/reviewer-$name" config commit.gpgsign false
done

cd "$DEMO_DIR"

# ─────────────────────────────────────────────────────────────────────────────
# DEMO BEGINS
# ─────────────────────────────────────────────────────────────────────────────
clear

printf "%s%s\n" "$BOLD" \
    "  GIT-ZF — PARALLEL CODE REVIEW WORKFLOW DEMO"
printf "%s\n\n" \
    "  Two developers (Alice, Bob) · Two reviewers (Carol, Dan)"
printf "  Workspace: %s\n\n%s" "$DEMO_DIR" "$RESET"
tree -I origin.git
printf "  Press any key to start...\n"
wait

# ─────────────────────────────────────────────────────────────────────────────
phase 1 "git zf init — install the pre-push lock guard"
# ─────────────────────────────────────────────────────────────────────────────

role "Alice" "initialises git-zf in her clone"
cd "$DEMO_DIR/" || exit 1
pe "cd dev-alice"
pe "git zf init"
wait

role "Bob" "same step in his clone"
cd "$DEMO_DIR/dev-bob" || exit 1
pe "cd ../dev-bob"
pe "git zf init"
wait

# ─────────────────────────────────────────────────────────────────────────────
phase 2 "create parent issue X and two sub-tasks"
# ─────────────────────────────────────────────────────────────────────────────

role "Alice" "starts the parent integration branch for issue X"
cd "$DEMO_DIR/dev-alice" || exit 1
pe "cd ../dev-alice"
hint "id → X   title → big   type → feat"
pe "git zf issue start"
# → creates X@feat@big from main
wait

role "Alice" "starts sub-task X.1 branched from X@feat@big"
hint "id → X.1   title → one   type → feat   (base pre-set to X@feat@big via --parent)"
pe "git zf issue start --parent X"
# → creates X.1@feat@one from X@feat@big
wait

role "Alice" "starts sub-task X.2 (Bob will work on this)"
hint "id → X.2   title → two   type → feat   base → X@feat@big"
pe "git zf issue start"
pe "git push origin X@feat@big X.1@feat@one X.2@feat@two"
wait

# ─────────────────────────────────────────────────────────────────────────────
phase 3 "parallel development — Alice on X.1, Bob on X.2"
# ─────────────────────────────────────────────────────────────────────────────

role "Alice" "implements part one"
cd "$DEMO_DIR/dev-alice" || exit 1
pe "git checkout X.1@feat@one"

# silent: actually write the file so git has something to commit
printf "one implementation\n" >"$DEMO_DIR/dev-alice/one.txt"

pe "echo 'one implementation' > one.txt"
pe "git add one.txt && git commit -m 'feat(X.1): implement part one'"
pe "git push origin X.1@feat@one"
wait

role "Bob" "implements part two"
cd "$DEMO_DIR/dev-bob" || exit 1
pe "cd ../dev-bob"
pe "git fetch origin"
pe "git checkout -b X.2@feat@two origin/X.2@feat@two"

printf "two implementation\n" >"$DEMO_DIR/dev-bob/two.txt"

pe "echo 'two implementation' > two.txt"
pe "git add two.txt && git commit -m 'feat(X.2): implement part two'"
pe "git push origin X.2@feat@two"
wait

# ─────────────────────────────────────────────────────────────────────────────
phase 4 "both developers submit for review simultaneously"
# ─────────────────────────────────────────────────────────────────────────────
printf "  %s(in real life these happen at the same time on different machines)%s\n\n" \
    "$MAGENTA" "$RESET"

role "Alice" "submits X.1 — picker pre-selects current branch"
cd "$DEMO_DIR/dev-alice" || exit 1
pe "cd ../dev-alice && git checkout X.1@feat@one"
hint "pick: X.1@feat@one  (pre-selected — press Enter)"
pe "git zf review request"
wait

role "Bob" "submits X.2"
cd "$DEMO_DIR/dev-bob" || exit 1
pe "cd ../dev-bob && git checkout X.2@feat@two"
# first request fails (branch not tracked yet) → track → retry
pe "git zf review request"
pe "git zf review track"
hint "pick: X.2@feat@two  (pre-selected — press Enter)"
pe "git zf review request"
wait

# ─────────────────────────────────────────────────────────────────────────────
phase 5 "reviewers see what is waiting"
# ─────────────────────────────────────────────────────────────────────────────

role "Carol" "fetches review refs and lists what needs reviewing"
cd "$DEMO_DIR/reviewer-carol" || exit 1
pe "cd ../reviewer-carol"
pe "git zf init"
pe "git fetch origin"
pe "git zf review list"
# Should show: X.1 in_review  X.2 in_review
wait

# ─────────────────────────────────────────────────────────────────────────────
phase 6 "reviewers start their reviews simultaneously"
# ─────────────────────────────────────────────────────────────────────────────

role "Carol" "starts reviewing X.1"
cd "$DEMO_DIR/reviewer-carol" || exit 1
hint "pick: X.1  (only issue in review — press Enter)"
pe "git zf review start"
# → creates X.1@review at lock-time SHA
pe "git log --oneline X.1@review"
wait

role "Dan" "starts reviewing X.2 and pushes an active fix"
cd "$DEMO_DIR/reviewer-dan" || exit 1
pe "cd ../reviewer-dan"
pe "git zf init"
pe "git fetch origin"
hint "pick: X.2  (press Enter)"
pe "git zf review start"
# → creates X.2@review

pe "git checkout X.2@review"
pe "echo 'reviewer fix by Dan' >> two.txt"
pe "git add two.txt && git commit -m 'fix(X.2): reviewer fix'"
pe "git push origin X.2@review"
wait

# ─────────────────────────────────────────────────────────────────────────────
phase 7 "Carol rejects X.1 · Dan approves X.2"
# ─────────────────────────────────────────────────────────────────────────────

role "Carol" "rejects X.1 — round 1 (no reviewer commits pushed)"
cd "$DEMO_DIR/reviewer-carol" || exit 1
pe "cd ../reviewer-carol"
hint "pick: X.1  (press Enter)"
pe "git zf review reject"
# → X.1@review deleted, feature branch unlocked for round 2
wait

role "Dan" "approves X.2 — reviewer commits will be incorporated on close"
cd "$DEMO_DIR/reviewer-dan" || exit 1
pe "cd ../reviewer-dan"
hint "pick: X.2  (press Enter)"
pe "git zf review approve"
# → approved, has_commits=1 recorded
wait

# ─────────────────────────────────────────────────────────────────────────────
phase 8 "Alice addresses feedback — round 2"
# ─────────────────────────────────────────────────────────────────────────────

role "Alice" "checks review status and sees round 1 was rejected"
cd "$DEMO_DIR/dev-alice" || exit 1
pe "cd ../dev-alice"
hint "pick: X.1  (press Enter to view history)"
pe "git zf review status"
# auto-fetches refs/zf/reviews/* — shows: Round 1  changes_requested
wait

role "Alice" "fixes the issue and resubmits"

printf "one implementation\nfixed per Carol review\n" \
    >"$DEMO_DIR/dev-alice/one.txt"

pe "echo 'fixed per Carol review' >> one.txt"
pe "git add one.txt && git commit -m 'fix(X.1): address review feedback'"
pe "git push origin X.1@feat@one"
hint "pick: X.1  (pre-selected — press Enter)"
pe "git zf review request"
# → round 2 lock created
wait

# ─────────────────────────────────────────────────────────────────────────────
phase 9 "Carol approves X.1 round 2"
# ─────────────────────────────────────────────────────────────────────────────

role "Carol" "starts round 2 review and approves"
cd "$DEMO_DIR/reviewer-carol" || exit 1
pe "cd ../reviewer-carol"
hint "pick: X.1  (round 2 — press Enter)"
pe "git zf review start"
# → X.1@review at round-2 SHA
hint "pick: X.1  (press Enter)"
pe "git zf review approve"
wait

# ─────────────────────────────────────────────────────────────────────────────
phase 10 "Bob closes X.2 — Dan's reviewer commits are incorporated"
# ─────────────────────────────────────────────────────────────────────────────

role "Bob" "closes X.2 — preflight fast-forwards X.2 to X.2@review tip"
cd "$DEMO_DIR/dev-bob" || exit 1
pe "cd ../dev-bob"
pe "git fetch origin"
hint "① branch: X.2@feat@two   ② strategy: rebase   ③ confirm: Enter   ④ commit form: Enter"
pe "git zf issue close"
# preflight: fast-forwards X.2@feat@two to X.2@review, deletes X.2@review,
# then rebases X.2 onto X@feat@big

printf "  %s↳ Dan's reviewer commit is now part of the squashed merge%s\n" \
    "$GREEN" "$RESET"
pe "git push origin X@feat@big"
wait

# ─────────────────────────────────────────────────────────────────────────────
phase 11 "Alice syncs X.1 (X.2 landed in parent) then closes"
# ─────────────────────────────────────────────────────────────────────────────

role "Alice" "X.2 landed in parent — X.1 has drifted, sync first"
cd "$DEMO_DIR/dev-alice" || exit 1
pe "cd ../dev-alice"
pe "git fetch origin"
hint "pick: X.1@feat@one  (press Enter)"
pe "git zf review sync"
# → merges origin/X@feat@big into X.1@feat@one
pe "git push origin X.1@feat@one"
wait

role "Alice" "closes X.1 — merges into parent integration branch"
hint "① branch: X.1@feat@one   ② strategy: rebase   ③ confirm: Enter   ④ commit form: Enter"
pe "git zf issue close"
# preflight: approved, no reviewer commits → proceeds directly
pe "git push origin X@feat@big"
wait

# ─────────────────────────────────────────────────────────────────────────────
phase 12 "optional integration review on parent X, then final close"
# ─────────────────────────────────────────────────────────────────────────────

role "Alice" "requests integration review on parent X"
cd "$DEMO_DIR/dev-alice" || exit 1
pe "git checkout X@feat@big"
hint "pick: X@feat@big  (pre-selected — press Enter)"
pe "git zf review request"
wait

role "Carol" "approves the composed result"
cd "$DEMO_DIR/reviewer-carol" || exit 1
pe "cd ../reviewer-carol"
hint "pick: X  (press Enter)"
pe "git zf review start"
hint "pick: X  (press Enter)"
pe "git zf review approve"
wait

role "Alice" "closes parent X — ChildrenAllMerged guard satisfied"
cd "$DEMO_DIR/dev-alice" || exit 1
pe "cd ../dev-alice"
pe "git fetch origin"
hint "① branch: X@feat@big   ② strategy: rebase   ③ confirm: Enter   ④ commit form: Enter"
pe "git zf issue close"
# ChildrenAllMerged: X.1 ✓  X.2 ✓ → close allowed
pe "git push origin main"
wait

# ─────────────────────────────────────────────────────────────────────────────
phase 13 "final state"
# ─────────────────────────────────────────────────────────────────────────────

role "Anyone" "nothing left in review"
pe "git zf review list"
printf "\n"

role "Anyone" "full history on main"
pe "git log --oneline origin/main"
wait

clear
printf "%s%s\n" "$BOLD" \
    "  ✓  Demo complete"
printf "\n"
printf "  Round summary:\n"
printf "    X.1  2 review rounds  (rejected → approved)\n"
printf "    X.2  1 review round   (approved, active reviewer commits incorporated)\n"
printf "    X    1 integration review\n"
printf "\n"
printf "  Workspace left at: %s%s\n\n" "$DEMO_DIR" "$RESET"
