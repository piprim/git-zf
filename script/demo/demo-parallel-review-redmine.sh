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
phase 2 "Alice creates parent issue X and start one sub-tasks"
# ─────────────────────────────────────────────────────────────────────────────

role "Alice" "starts the parent integration branch for issue X"
cd "$DEMO_DIR/dev-alice" || exit 1
pe "cd ../dev-alice"
hint "id → 1149829   title → big   type → feat"
pe "git zf issue start"
# → creates 1149829@feat@big from main
wait

role "Alice" "starts sub-task X.1 branched from X@feat@big"
hint "id → 1149830   title → one   type → feat   (base pre-set to 1149829@feat@big via --parent)"
pe "git zf issue start --parent 1149829"
# → creates 1149830@feat@one from X@feat@big
wait

role "Alice" "implements part one"
# silent: actually write the file so git has something to commit
printf "one implementation\n" >"$DEMO_DIR/dev-alice/one.txt"

pe "echo 'one implementation' > one.txt"
pe "git add one.txt && git zf commit"
pe "git push origin 1149830@feat@one 1149829@feat@big"
wait

# ─────────────────────────────────────────────────────────────────────────────
phase 2 "Bob start one sub-tasks of X"
# ─────────────────────────────────────────────────────────────────────────────

role "Bob" "Create sub-tacks X.2 from X"
cd "$DEMO_DIR/dev-bob" || exit 1
pe "cd ../dev-bob"
pe "git fetch origin"
hint "id → 1149831   title → two   type → feat   base → 1149829@feat@big"
pe "git zf issue start"
wait

role "Bob" "implements part two"
printf "two implementation\n" >"$DEMO_DIR/dev-bob/two.txt"

pe "echo 'two implementation' > two.txt"
pe "git add two.txt && git zf commit -a"
pe "git push origin 1149831@feat@two"
wait

# ─────────────────────────────────────────────────────────────────────────────
phase 4 "both developers submit for review simultaneously"
# ─────────────────────────────────────────────────────────────────────────────
printf "  %s(in real life these happen at the same time on different machines)%s\n\n" \
    "$MAGENTA" "$RESET"

role "Alice" "submits 1149830 — picker pre-selects current branch"
cd "$DEMO_DIR/dev-alice" || exit 1
pe "cd ../dev-alice && git checkout 1149830@feat@one"
hint "pick: 1149830@feat@one"
pe "git zf review request"
wait

role "Bob" "submits 1149831"
cd "$DEMO_DIR/dev-bob" || exit 1
pe "cd ../dev-bob && git checkout 1149831@feat@two"
hint "pick: 1149831@feat@two  (pre-selected — press Enter)"
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
# Should show: 1149830 in_review  1149831 in_review
wait

# ─────────────────────────────────────────────────────────────────────────────
phase 6 "reviewers start their reviews simultaneously"
# ─────────────────────────────────────────────────────────────────────────────

role "Carol" "starts reviewing 1149830"
cd "$DEMO_DIR/reviewer-carol" || exit 1
hint "pick: 1149830"
pe "git zf review start"
# → creates 1149830@review at lock-time SHA
pe "git log 1149830@review"
wait

role "Dan" "starts reviewing 1149831 and pushes an active fix"
cd "$DEMO_DIR/reviewer-dan" || exit 1
pe "cd ../reviewer-dan"
pe "git zf init"
pe "git fetch origin"
hint "pick: 1149831"
pe "git zf review start"
# → creates 1149831@review

pe "git checkout 1149831@review"
pe "echo 'reviewer fix by Dan' >> two.txt"
pe "git add two.txt && git zf commit -a"
pe "git push origin 1149831@review"
wait

# ─────────────────────────────────────────────────────────────────────────────
phase 7 "Carol rejects 1149830 · Dan approves 1149831"
# ─────────────────────────────────────────────────────────────────────────────

role "Carol" "rejects 1149830 — round 1 (no reviewer commits pushed)"
cd "$DEMO_DIR/reviewer-carol" || exit 1
pe "cd ../reviewer-carol"
hint "pick: 1149830"
pe "git zf review reject"
printf "  %s↳ 1149830@review deleted, feature branch unlocked for round 2%s\n" "$GREEN" "$RESET"
wait

role "Dan" "approves 1149831 — reviewer commits will be incorporated on close"
cd "$DEMO_DIR/reviewer-dan" || exit 1
pe "cd ../reviewer-dan"
hint "pick: 1149831"
pe "git zf review approve"
# → approved, has_commits=1 recorded
wait

# ─────────────────────────────────────────────────────────────────────────────
phase 8 "Alice addresses feedback — round 2"
# ─────────────────────────────────────────────────────────────────────────────

role "Alice" "checks review status and sees round 1 was rejected"
cd "$DEMO_DIR/dev-alice" || exit 1
pe "cd ../dev-alice"
hint "pick: 1149830  (press Enter to view history)"
pe "git zf review status"
# auto-fetches refs/zf/reviews/* — shows: Round 1  changes_requested
wait

role "Alice" "fixes the issue and resubmits"

printf "one implementation\nfixed per Carol review\n" \
    >"$DEMO_DIR/dev-alice/one.txt"

pe "echo 'fixed per Carol review' >> one.txt"
pe "git add one.txt && git commit -m 'fix(1149830): address review feedback'"
pe "git push origin 1149830@feat@one"
hint "pick: 1149830  (pre-selected — press Enter)"
pe "git zf review request"
# → round 2 lock created
wait

# ─────────────────────────────────────────────────────────────────────────────
phase 9 "Carol approves X.1 round 2"
# ─────────────────────────────────────────────────────────────────────────────

role "Carol" "starts round 2 review and approves"
cd "$DEMO_DIR/reviewer-carol" || exit 1
pe "cd ../reviewer-carol"
hint "pick: 1149830  (round 2 — press Enter)"
pe "git zf review start"
# → 1149830@review at round-2 SHA
hint "pick: 1149830"
pe "git zf review approve"
wait

# ─────────────────────────────────────────────────────────────────────────────
phase 10 "Bob closes X.2 — Dan's reviewer commits are incorporated"
# ─────────────────────────────────────────────────────────────────────────────

role "Bob" "closes 1149831 — preflight fast-forwards 1149831 to 1149831@review tip"
cd "$DEMO_DIR/dev-bob" || exit 1
pe "cd ../dev-bob"
pe "git fetch origin"
hint "① branch: 1149831@feat@two   ② strategy: rebase   ③ confirm: Enter   ④ commit form: Enter"
pe "git zf issue close"
# preflight: fast-forwards 1149831@feat@two to 1149831@review, deletes 1149831@review,
# then rebases 1149831 onto 1149829@feat@big

printf "  %s↳ Dan's reviewer commit is now part of the squashed merge%s\n" "$GREEN" "$RESET"
pe "git push origin 1149829@feat@big"
wait

# ─────────────────────────────────────────────────────────────────────────────
phase 11 "Alice closes 1149830"
# ─────────────────────────────────────────────────────────────────────────────

role "Alice" "closes 1149830 — merges into parent integration branch"
cd "$DEMO_DIR/dev-alice" || exit 1
pe "cd ../dev-alice"
pe "git fetch origin"
hint "① branch: 1149830@feat@one   ② strategy: rebase   ③ confirm: Enter   ④ commit form: Enter"
pe "git zf issue close"
# preflight: approved, no reviewer commits → proceeds directly
pe "git push origin 1149829@feat@big"
wait

# ─────────────────────────────────────────────────────────────────────────────
phase 12 "optional integration review on parent X, then final close"
# ─────────────────────────────────────────────────────────────────────────────

role "Alice" "requests integration review on parent X"
cd "$DEMO_DIR/dev-alice" || exit 1
pe "git checkout 1149829@feat@big"
hint "pick: 1149829@feat@big  (pre-selected — press Enter)"
pe "git zf review request"
wait

role "Carol" "approves the composed result"
cd "$DEMO_DIR/reviewer-carol" || exit 1
pe "cd ../reviewer-carol"
hint "pick: 1149829  (press Enter)"
pe "git zf review start"
hint "pick: 1149829  (press Enter)"
pe "git zf review approve"
wait

role "Alice" "closes parent X — ChildrenAllMerged guard satisfied"
cd "$DEMO_DIR/dev-alice" || exit 1
pe "cd ../dev-alice"
pe "git fetch origin"
hint "① branch: 1149829@feat@big   ② strategy: rebase   ③ confirm: Enter   ④ commit form: Enter"
pe "git zf issue close"
# ChildrenAllMerged: 1149830 ✓  1149831 ✓ → close allowed
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
printf "    1149830  2 review rounds  (rejected → approved)\n"
printf "    1149831  1 review round   (approved, active reviewer commits incorporated)\n"
printf "    1149829  1 integration review\n"
printf "\n"
printf "  Workspace left at: %s%s\n\n" "$DEMO_DIR" "$RESET"

cd "$DEMO_DIR" || exit 1
tree -I origin.git
