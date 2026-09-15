#!/usr/bin/env bash
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
TMP=$(mktemp -d "${TMPDIR:-/tmp}/envkit-test.XXXXXX")
ENVKIT="$TMP/envkit"
HOME_DIR="$TMP/home"
REPO="$TMP/repo"
trap 'rm -rf "$TMP"' EXIT

go build -o "$ENVKIT" "$ROOT"

fail() { echo "FAIL: $*" >&2; exit 1; }
assert_eq() { [ "$1" = "$2" ] || fail "expected [$2], got [$1]"; }
assert_contains() { case "$1" in *"$2"*) ;; *) fail "missing [$2]" ;; esac; }
assert_not_contains() { case "$1" in *"$2"*) fail "unexpected [$2]" ;; *) ;; esac; }
run() { ENVKIT_HOME="$HOME_DIR" "$ENVKIT" "$@"; }

mkdir "$REPO"
git -C "$REPO" init -q
git -C "$REPO" config user.name test
git -C "$REPO" config user.email test@example.invalid
cd "$REPO"

for pair in \
  'https://github.com/owner/repo.git owner__repo' \
  'ssh://git@github.com/owner/repo.git owner__repo' \
  'git@github.com:owner/repo.git owner__repo' \
  'github-narayana:owner/repo.git owner__repo'; do
  url=${pair% *}
  expected=${pair#* }
  git remote remove origin 2>/dev/null || true
  git remote add origin "$url"
  assert_eq "$(run project)" "$expected"
done

printf '%s' 'super-secret' | run set API_TOKEN -m 'first token'
assert_eq "$(run get API_TOKEN)" 'super-secret'
listed=$(run ls)
assert_contains "$listed" 'API_TOKEN  # first token'
assert_not_contains "$listed" 'super-secret'

run comment API_TOKEN 'rotated token'
assert_contains "$(run ls)" 'API_TOKEN  # rotated token'
run run -- sh -c '[ "$API_TOKEN" = super-secret ]'

printf '%s' 'other-secret' | run set REMOVE_ME
run unset REMOVE_ME
[ -z "$(run get REMOVE_ME)" ] || fail 'unset key is still present'

commits=$(git -C "$HOME_DIR" log --oneline --all | wc -l | tr -d ' ')
[ "$commits" -ge 2 ] || fail "expected at least two audit commits, got $commits"

git -C "$REPO" commit --allow-empty -q -m initial
WT="$TMP/worktree"
git -C "$REPO" worktree add -q "$WT"
assert_eq "$(cd "$WT" && ENVKIT_HOME="$HOME_DIR" "$ENVKIT" project)" 'owner__repo'

echo 'ok: project URL forms, secret operations, audit log, and worktree resolution'
