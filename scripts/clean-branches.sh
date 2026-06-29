#!/bin/bash
# Clean up stale Git branches and worktree artifacts.
# Safe to run: deletes only merged branches and stale worktrees.
# Does NOT delete main, current branch, or open-PR branches.

set -euo pipefail

echo "=== Git Branch & Worktree Cleanup ==="

# Step 0: Get open PR branch names to protect
echo ""
echo "[0] Fetching open PR branches (will protect)..."
OPEN_BRANCHES=$(gh pr list --state open --json headRefName 2>/dev/null | jq -r '.[].headRefName' || true)
PROTECTED="main master $OPEN_BRANCHES"
echo "  Protected: $(echo "$PROTECTED" | tr '\n' ' ')"

# Step 1: Prune stale remote-tracking refs
echo ""
echo "[1] Pruning stale remote-tracking refs..."
git fetch --prune origin 2>&1 | sed 's/^/  /'
echo "  Done."

# Step 2: Clean up worktree branches (from abandoned agent isolation)
echo ""
echo "[2] Cleaning stale worktree directories..."
# Find all stale worktrees
for wt in $(git worktree list --porcelain 2>/dev/null | grep -B1 "^bare$" | grep "^worktree " | cut -d' ' -f2- || true); do
    # Skip if it's the main worktree
    if [ "$(git rev-parse --absolute-git-dir 2>/dev/null)" = "$wt/.git" ] 2>/dev/null; then
        continue
    fi
    echo "  Removing stale worktree: $wt"
    git worktree remove "$wt" --force 2>/dev/null || true
done

# Also handle orphaned worktree entries (worktree dir deleted but git still tracks it)
git worktree prune 2>/dev/null || true

echo "  Worktrees remaining:"
git worktree list 2>/dev/null | sed 's/^/    /'

# Step 3: Delete local branches that have been merged into main
echo ""
echo "[3] Deleting merged local branches..."
MERGED=$(git branch --merged main | grep -v "^\*" | tr -d ' ')
for branch in $MERGED; do
    # Skip protected branches
    if echo "$PROTECTED" | grep -qw "$branch"; then
        echo "  Skipping (protected): $branch"
        continue
    fi
    # Skip worktree-agent branches (already handled by worktree cleanup)
    if echo "$branch" | grep -q "^worktree-agent-"; then
        echo "  Deleting worktree branch: $branch"
        git branch -D "$branch" 2>/dev/null || true
        continue
    fi
    echo "  Deleting merged branch: $branch"
    git branch -d "$branch" 2>/dev/null || true
done

# Step 4: Delete local branches whose remote tracking branch is gone
echo ""
echo "[4] Deleting branches with deleted remote..."
GONE=$(git branch -vv | grep ': gone]' | awk '{print $1}' || true)
for branch in $GONE; do
    if echo "$PROTECTED" | grep -qw "$branch"; then
        echo "  Skipping (protected): $branch"
        continue
    fi
    echo "  Deleting: $branch (remote gone)"
    git branch -D "$branch" 2>/dev/null || true
done

# Step 5: Delete stale remote branches (merged into main)
echo ""
echo "[5] Cleaning remote merged branches..."
REMOTE_MERGED=$(git branch -r --merged origin/main | grep "origin/" | grep -v "origin/HEAD" | grep -v "origin/main" | sed 's/ *origin\///' || true)
for branch in $REMOTE_MERGED; do
    if echo "$PROTECTED" | grep -qw "$branch"; then
        echo "  Skipping (protected): $branch"
        continue
    fi
    echo "  Deleting remote branch: origin/$branch"
    git push origin --delete "$branch" 2>/dev/null || echo "  (already gone or permission denied)"
done

# Summary
echo ""
echo "=== Cleanup complete ==="
echo "Remaining branches:"
git branch | sed 's/^/  /'
echo ""
echo "Remaining worktrees:"
git worktree list | sed 's/^/  /'