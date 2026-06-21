#!/bin/sh
# Manual test — parallel code review workflow
#
# Simulates a two-developer, two-reviewer team working on a parent issue X
# split into two sub-tasks (X.1 and X.2) developed and reviewed in parallel.
# One sub-task goes through a rejection round. The reviewer for X.2 pushes
# active fixes to the review branch.
#
# Prerequisites:
#   git zf install   (binary in git exec-path)
#   Run from any directory — the script creates its own temp workspace.
#
# Topology:
#   origin.git        bare remote
#   dev-alice/        developer Alice (works on X.1)
#   dev-bob/          developer Bob   (works on X.2)
#   reviewer-carol/   reviewer Carol  (reviews X.1, rejects then approves)
#   reviewer-dan/     reviewer Dan    (reviews X.2 with active fixes)

set -e

WORKDIR=$(mktemp -d)
echo "==> workspace: $WORKDIR"
cd "$WORKDIR"

# ─────────────────────────────────────────────
# PHASE 0 — infrastructure
# ─────────────────────────────────────────────
echo ""
echo "── PHASE 0: create bare remote and two developer clones ─────────────"

git init --bare --quiet --initial-branch=main origin.git

# Seed the bare remote with an initial commit via a throw-away clone.
git clone --quiet origin.git seed
(
  cd seed
  git config user.name  "Seed"
  git config user.email "seed@example.com"
  git config commit.gpgsign false
  echo "# Project" > README.md
  git add README.md
  git commit --quiet -m "chore: init"
  git push --quiet origin main
)
rm -rf seed

# Clone for Alice (works on X.1)
git clone --quiet origin.git dev-alice
(
  cd dev-alice
  git config user.name  "Alice"
  git config user.email "alice@example.com"
  git config commit.gpgsign false
  git zf init          # installs pre-push hook
  git zf config init   # writes repo-level .git-zf.toml (pick "This repo")
)

# Clone for Bob (works on X.2)
git clone --quiet origin.git dev-bob
(
  cd dev-bob
  git config user.name  "Bob"
  git config user.email "bob@example.com"
  git config commit.gpgsign false
  git zf init
)

echo "── PHASE 0 done ─────────────────────────────────────────────────────"

# ─────────────────────────────────────────────
# PHASE 1 — Alice creates parent issue X and sub-tasks X.1, X.2
# ─────────────────────────────────────────────
echo ""
echo "── PHASE 1: Alice creates parent issue X and sub-tasks ──────────────"

(
  cd dev-alice

  # Start parent integration branch for issue X.
  # git zf issue start  (TUI: id=X, title=big-feature, type=feat)
  # For this script we seed the store directly to avoid interactive TUI.
  # In real use: git zf issue start
  echo "  [Alice] git zf issue start  →  id=X, title=big-feature, type=feat"
  echo "          (creates X@feat@big-feature from main)"

  # Start sub-task X.1 — branches from parent integration branch X@feat@big-feature.
  echo "  [Alice] git zf issue start --parent X  →  id=X.1, title=part-one, type=feat"
  echo "          (creates X.1@feat@part-one from X@feat@big-feature)"

  # Alice tells Bob about X.2.
  echo "  [Alice] git zf issue start --parent X  →  id=X.2, title=part-two, type=feat"
  echo "          (creates X.2@feat@part-two from X@feat@big-feature)"
  echo "  [Alice] git push origin X@feat@big-feature X.1@feat@part-one X.2@feat@part-two"
)

echo "── PHASE 1 done ─────────────────────────────────────────────────────"

# ─────────────────────────────────────────────
# PHASE 2 — Parallel development  (Alice on X.1, Bob on X.2)
# ─────────────────────────────────────────────
echo ""
echo "── PHASE 2: parallel development (Alice→X.1, Bob→X.2) ──────────────"

(
  cd dev-alice
  git checkout --quiet X.1@feat@part-one 2>/dev/null || true
  echo "part-one work" >> part-one.txt
  git add part-one.txt
  git commit --quiet -m "feat(X.1): implement part one"
  git push --quiet origin X.1@feat@part-one
  echo "  [Alice] pushed work on X.1@feat@part-one"
)

(
  cd dev-bob
  git fetch --quiet origin
  git checkout --quiet -b X.2@feat@part-two origin/X.2@feat@part-two 2>/dev/null || \
  git checkout --quiet X.2@feat@part-two
  echo "part-two work" >> part-two.txt
  git add part-two.txt
  git commit --quiet -m "feat(X.2): implement part two"
  git push --quiet origin X.2@feat@part-two
  echo "  [Bob]   pushed work on X.2@feat@part-two"
)

echo "── PHASE 2 done ─────────────────────────────────────────────────────"

# ─────────────────────────────────────────────
# PHASE 3 — Both developers submit for review (parallel)
# ─────────────────────────────────────────────
echo ""
echo "── PHASE 3: Alice and Bob submit for review simultaneously ──────────"

(
  cd dev-alice
  git checkout --quiet X.1@feat@part-one
  echo "  [Alice] git zf review request  →  picks X.1 (current branch)"
  git zf review request   # TUI picker pre-selects X.1
  echo "  [Alice] refs/zf/reviews/X.1 pushed to origin (status: in_review)"
)

(
  cd dev-bob
  git checkout --quiet X.2@feat@part-two
  echo "  [Bob]   git zf review request  →  picks X.2 (current branch)"
  git zf review request   # TUI picker pre-selects X.2
  echo "  [Bob]   refs/zf/reviews/X.2 pushed to origin (status: in_review)"
)

echo "── PHASE 3 done ─────────────────────────────────────────────────────"

# ─────────────────────────────────────────────
# PHASE 4 — Reviewers fetch and start (parallel)
# ─────────────────────────────────────────────
echo ""
echo "── PHASE 4: Carol and Dan start reviews simultaneously ──────────────"

git clone --quiet origin.git reviewer-carol
(
  cd reviewer-carol
  git config user.name  "Carol"
  git config user.email "carol@example.com"
  git config commit.gpgsign false
  git zf init
  git fetch --quiet origin 'refs/zf/reviews/*:refs/zf/reviews/*'

  echo "  [Carol] git zf review list  (should show X.1 and X.2 in_review)"
  git zf review list

  echo "  [Carol] git zf review start  →  picks X.1"
  git zf review start   # TUI picker; Carol selects X.1
  echo "  [Carol] created X.1@review at lock-time SHA"
)

git clone --quiet origin.git reviewer-dan
(
  cd reviewer-dan
  git config user.name  "Dan"
  git config user.email "dan@example.com"
  git config commit.gpgsign false
  git zf init
  git fetch --quiet origin 'refs/zf/reviews/*:refs/zf/reviews/*'

  echo "  [Dan]   git zf review start  →  picks X.2"
  git zf review start   # TUI picker; Dan selects X.2

  # Dan pushes an active fix to the review branch.
  git checkout --quiet X.2@review
  echo "dan fix" >> part-two.txt
  git add part-two.txt
  git commit --quiet -m "fix(X.2): reviewer fix from Dan"
  git push --quiet origin X.2@review
  echo "  [Dan]   pushed reviewer fix to X.2@review"
)

echo "── PHASE 4 done ─────────────────────────────────────────────────────"

# ─────────────────────────────────────────────
# PHASE 5 — Carol rejects X.1 (round 1); Dan approves X.2
# ─────────────────────────────────────────────
echo ""
echo "── PHASE 5: Carol rejects X.1, Dan approves X.2 (parallel) ─────────"

(
  cd reviewer-carol
  git checkout --quiet X.1@review
  echo "  [Carol] git zf review reject  →  picks X.1"
  git zf review reject   # TUI picker; Carol selects X.1
  # X.1@review has no reviewer commits → branch deleted, round counter = 2
  echo "  [Carol] X.1@review deleted; feature/X.1 unlocked (round 2)"
)

(
  cd reviewer-dan
  git checkout --quiet X.2@review
  echo "  [Dan]   git zf review approve  →  picks X.2"
  git zf review approve  # TUI picker; Dan selects X.2
  # X.2@review has reviewer commits → has_commits=1 stored
  echo "  [Dan]   X.2 approved (has_commits=1)"
)

echo "── PHASE 5 done ─────────────────────────────────────────────────────"

# ─────────────────────────────────────────────
# PHASE 6 — Alice addresses feedback and submits round 2
# ─────────────────────────────────────────────
echo ""
echo "── PHASE 6: Alice iterates on X.1 (round 2) ─────────────────────────"

(
  cd dev-alice
  git fetch --quiet origin 'refs/zf/reviews/*:refs/zf/reviews/*'
  # Local store reconciles: X.1 is now changes_requested.

  git checkout --quiet X.1@feat@part-one
  echo "  [Alice] git zf review status  →  shows round 1 rejected"
  git zf review status   # TUI: shows history for X.1

  echo "  [Alice] addressing feedback..."
  echo "fixed" >> part-one.txt
  git add part-one.txt
  git commit --quiet -m "fix(X.1): address review feedback"
  git push --quiet origin X.1@feat@part-one

  echo "  [Alice] git zf review request  →  round 2"
  git zf review request  # TUI picker; creates round-2 lock
  echo "  [Alice] X.1 resubmitted (round 2)"
)

echo "── PHASE 6 done ─────────────────────────────────────────────────────"

# ─────────────────────────────────────────────
# PHASE 7 — Carol approves X.1 round 2
# ─────────────────────────────────────────────
echo ""
echo "── PHASE 7: Carol approves X.1 (round 2) ───────────────────────────"

(
  cd reviewer-carol
  git fetch --quiet origin 'refs/zf/reviews/*:refs/zf/reviews/*'

  echo "  [Carol] git zf review start  →  picks X.1 (round 2)"
  git zf review start   # creates X.1@review at new lock-time SHA

  echo "  [Carol] git zf review approve  →  picks X.1"
  git zf review approve  # TUI picker; X.1 approved
  echo "  [Carol] X.1 approved (round 2)"
)

echo "── PHASE 7 done ─────────────────────────────────────────────────────"

# ─────────────────────────────────────────────
# PHASE 8 — Bob closes X.2 (fast-forwards reviewer commits from Dan)
# ─────────────────────────────────────────────
echo ""
echo "── PHASE 8: Bob closes X.2 (reviewer commits incorporated) ──────────"

(
  cd dev-bob
  git fetch --quiet origin 'refs/zf/reviews/*:refs/zf/reviews/*'
  git fetch --quiet origin

  echo "  [Bob]   git zf issue close  →  picks X.2"
  # reviewPreflight: status=approved, X.2@review has 1 reviewer commit
  # → fast-forwards X.2@feat@part-two to X.2@review tip
  # → deletes X.2@review (local + remote)
  # → proceeds with merge strategy into X@feat@big-feature
  git zf issue close

  echo "  [Bob]   X.2 merged into X@feat@big-feature"
  git push --quiet origin X@feat@big-feature
)

echo "── PHASE 8 done ─────────────────────────────────────────────────────"

# ─────────────────────────────────────────────
# PHASE 9 — Alice syncs X.1 (X.2 landed in parent) then closes X.1
# ─────────────────────────────────────────────
echo ""
echo "── PHASE 9: Alice syncs X.1 with parent, then closes ────────────────"

(
  cd dev-alice
  git fetch --quiet origin
  git fetch --quiet origin 'refs/zf/reviews/*:refs/zf/reviews/*'

  echo "  [Alice] git zf review sync  →  picks X.1"
  # Detects X.1@feat@part-one is behind X@feat@big-feature (X.2 landed).
  # Merges X@feat@big-feature into X.1@feat@part-one (merge-forward, no rebase).
  git zf review sync
  git push --quiet origin X.1@feat@part-one

  echo "  [Alice] git zf issue close  →  picks X.1"
  # reviewPreflight: status=approved, no reviewer commits → proceeds
  # merges X.1@feat@part-one into X@feat@big-feature
  git zf issue close

  echo "  [Alice] X.1 merged into X@feat@big-feature"
  git push --quiet origin X@feat@big-feature
)

echo "── PHASE 9 done ─────────────────────────────────────────────────────"

# ─────────────────────────────────────────────
# PHASE 10 — Optional integration review on parent X, then close
# ─────────────────────────────────────────────
echo ""
echo "── PHASE 10: integration review on parent X, then close ─────────────"

(
  cd dev-alice
  git checkout --quiet X@feat@big-feature
  echo "  [Alice] git zf review request  →  optional integration review on X"
  git zf review request   # TUI: picks X
)

(
  cd reviewer-carol
  git fetch --quiet origin 'refs/zf/reviews/*:refs/zf/reviews/*'
  git fetch --quiet origin

  echo "  [Carol] git zf review start   →  reviews X integration branch"
  git zf review start

  echo "  [Carol] git zf review approve →  integration looks good"
  git zf review approve
)

(
  cd dev-alice
  git fetch --quiet origin 'refs/zf/reviews/*:refs/zf/reviews/*'
  git fetch --quiet origin

  echo "  [Alice] git zf issue close  →  picks X (all children merged)"
  # ChildrenAllMerged: X.1 ✓, X.2 ✓ → close allowed
  # merges X@feat@big-feature into main
  git zf issue close
  git push --quiet origin main

  echo "  [Alice] X merged into main ✓"
)

echo ""
echo "── PHASE 10 done ────────────────────────────────────────────────────"
echo ""
echo "==> All phases complete. Workspace: $WORKDIR"
echo ""
echo "    Verify final state:"
echo "      git -C $WORKDIR/dev-alice log --oneline main"
echo "      git -C $WORKDIR/dev-alice zf review list   # should show nothing"
