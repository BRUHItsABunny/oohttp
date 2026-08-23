#!/bin/bash
#
# Merges upstream Go's net/http into this fork.
#
# The previous version of this script ran `git subtree split` over the whole
# Go repository on every sync. That walks all ~67k commits (hours on Windows,
# with a ~45k file scratch cache in .git) only to regenerate a chain whose
# *new* content is the handful of commits added since the last sync -- 103 of
# them for go1.26.6 -> go1.27.0. It also checked the entire Go source tree out
# over your working directory to do it.
#
# Instead we replay just the new commits onto the existing chain with
# `git commit-tree`. That takes seconds, needs no checkout, and produces the
# same thing `git subtree split` would have: one commit per upstream commit
# that touched net/http, with the original author, date and message, whose
# tree is that commit's src/net/http. `golang-http-upstream` stays an ancestor,
# so `git merge` still resolves the same merge base and the same conflicts.
#
# IMPORTANT: golang-http-upstream is now durable state. The chain of upstream
# commits *is* the branch, so unlike before it must not be deleted between
# runs, and it should be pushed to origin. If it is ever lost, recover it from
# the second parent of the most recent sync merge:
#
#     git branch golang-http-upstream $(git rev-parse <sync-merge-commit>^2)
#
# Usage:
#
#     echo go1.28.0 > UPSTREAM
#     ./tools/merge.bash
#
# Set PREV_TAG to override which tag the chain is assumed to sit at, in the
# rare case auto-detection cannot work it out.

set -euo pipefail

SUBTREE_PREFIX=src/net/http
SPLIT_BRANCH=golang-http-upstream
GO_REMOTE=golang
GO_REMOTE_URL=https://github.com/golang/go.git

die() {
	echo "merge.bash: $*" >&2
	exit 1
}

test -f UPSTREAM || die "run me from the top level of the repository"
NEW_TAG=$(cat UPSTREAM)

# --- 1. fetch upstream ------------------------------------------------------

git remote get-url "$GO_REMOTE" >/dev/null 2>&1 ||
	git remote add "$GO_REMOTE" "$GO_REMOTE_URL"
echo "fetching $GO_REMOTE ..."
git fetch --tags --quiet "$GO_REMOTE"

git rev-parse -q --verify "$NEW_TAG^{commit}" >/dev/null ||
	die "UPSTREAM names '$NEW_TAG', which is not a commit"

new_tree=$(git rev-parse "$NEW_TAG:$SUBTREE_PREFIX")

# --- 2. work out where the existing chain sits ------------------------------

tip=$(git rev-parse -q --verify "$SPLIT_BRANCH^{commit}") || die "\
branch '$SPLIT_BRANCH' does not exist.

It is the chain of upstream net/http commits and is required to graft onto.
Recover it from the last sync merge commit:

    git branch $SPLIT_BRANCH \$(git rev-parse <sync-merge-commit>^2)"

tip_tree=$(git rev-parse "$tip^{tree}")

if [ "$tip_tree" = "$new_tree" ]; then
	echo "already at $NEW_TAG: net/http is unchanged since the last sync"
	exit 0
fi

# Every commit on the chain is a verbatim copy of some upstream commit's
# net/http tree, so the tag the chain currently sits at is the newest one
# whose subtree tree matches the tip. Newest wins: if several releases left
# net/http untouched there is nothing to replay for the older ones anyway.
PREV_TAG=${PREV_TAG:-}
if [ -z "$PREV_TAG" ]; then
	for t in $(git tag -l 'go1.*' --sort=-version:refname); do
		t_tree=$(git rev-parse -q --verify "$t:$SUBTREE_PREFIX" 2>/dev/null) || continue
		if [ "$t_tree" = "$tip_tree" ]; then
			PREV_TAG=$t
			break
		fi
	done
fi
test -n "$PREV_TAG" || die "\
cannot work out which upstream tag '$SPLIT_BRANCH' currently sits at.

No go1.* tag has a $SUBTREE_PREFIX tree matching $tip_tree. If the chain was
built from something other than a release tag, re-run with PREV_TAG set:

    PREV_TAG=go1.27.0 ./tools/merge.bash"

# Note that $PREV_TAG is normally *not* an ancestor of $NEW_TAG: Go cuts each
# release branch from master separately, so go1.26.6 carries 1.26-only
# backports that go1.27.0 never sees. That is fine. "$PREV_TAG..$NEW_TAG" still
# means "what 1.27 has that 1.26.6 did not", which is what we want to replay,
# and the tree check after the replay is what actually guarantees we land on
# exactly what upstream ships.
test "$(printf '%s\n%s\n' "$PREV_TAG" "$NEW_TAG" | sort -V | head -n 1)" = "$PREV_TAG" ||
	die "$NEW_TAG is older than $PREV_TAG; is UPSTREAM going backwards?"

echo "replaying $SUBTREE_PREFIX commits in $PREV_TAG..$NEW_TAG onto $SPLIT_BRANCH"

# --- 3. replay the new commits ----------------------------------------------

# `git subtree split` linearises the subtree history: commits that leave the
# subtree untouched are skipped, and the rest are rewritten one parent at a
# time. With no merges in range the replay below is exactly that. If upstream
# ever does land a merge that touches net/http, we still get the right trees,
# but the branch shape is flattened -- hence the warning.
merges=$(git rev-list --count --merges "$PREV_TAG..$NEW_TAG" -- "$SUBTREE_PREFIX")
if [ "$merges" -ne 0 ]; then
	echo "warning: $merges merge commit(s) touch $SUBTREE_PREFIX; history will be linearised" >&2
fi

created=0
skipped=0
while read -r commit; do
	tree=$(git rev-parse "$commit:$SUBTREE_PREFIX")
	if [ "$tree" = "$tip_tree" ]; then
		# A revert, or a change that cancelled out: nothing to record.
		skipped=$((skipped + 1))
		continue
	fi

	{
		read -r GIT_AUTHOR_NAME
		read -r GIT_AUTHOR_EMAIL
		read -r GIT_AUTHOR_DATE
		read -r GIT_COMMITTER_NAME
		read -r GIT_COMMITTER_EMAIL
		read -r GIT_COMMITTER_DATE
	} < <(git show -s --format='%an%n%ae%n%aI%n%cn%n%ce%n%cI' "$commit")
	export GIT_AUTHOR_NAME GIT_AUTHOR_EMAIL GIT_AUTHOR_DATE
	export GIT_COMMITTER_NAME GIT_COMMITTER_EMAIL GIT_COMMITTER_DATE

	tip=$(git show -s --format=%B "$commit" | git commit-tree "$tree" -p "$tip")
	tip_tree=$tree
	created=$((created + 1))
done < <(git rev-list --reverse --topo-order "$PREV_TAG..$NEW_TAG" -- "$SUBTREE_PREFIX")

unset GIT_AUTHOR_NAME GIT_AUTHOR_EMAIL GIT_AUTHOR_DATE
unset GIT_COMMITTER_NAME GIT_COMMITTER_EMAIL GIT_COMMITTER_DATE

echo "replayed $created commit(s), skipped $skipped that did not change the subtree"

# The chain must end up holding exactly what upstream ships, or the merge below
# would quietly import the wrong thing.
test "$tip_tree" = "$new_tree" ||
	die "replayed tree $tip_tree != $NEW_TAG:$SUBTREE_PREFIX ($new_tree)"

git branch -f "$SPLIT_BRANCH" "$tip"
echo "$SPLIT_BRANCH is now $tip"

# --- 4. merge into the fork -------------------------------------------------

merge_branch="merged-${NEW_TAG}"
git checkout main
git checkout -B "$merge_branch"
git merge "$SPLIT_BRANCH"
