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

assert_eq "$(run version)" 'dev'

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

run set A_KEY val1
assert_eq "$(run get A_KEY)" 'val1'
run set B_KEY='val=2' -m 'inline note'
assert_eq "$(run get B_KEY)" 'val=2'
assert_contains "$(run ls)" 'B_KEY  # inline note'
run set C_KEY -m 'flag first' 'val 3'
assert_eq "$(run get C_KEY)" 'val 3'
assert_contains "$(run ls)" 'C_KEY  # flag first'
run set D_KEY= < /dev/null
assert_contains "$(run ls)" 'D_KEY'
[ -z "$(run get D_KEY)" ] || fail 'D_KEY should be empty'
status=0; run set E_KEY a b 2>/dev/null || status=$?
assert_eq "$status" '2'
status=0; run set F_KEY=x y 2>/dev/null || status=$?
assert_eq "$status" '2'
status=0; run set 'bad-key=x' 2>/dev/null || status=$?
assert_eq "$status" '2'

commits=$(git -C "$HOME_DIR" log --oneline --all | wc -l | tr -d ' ')
[ "$commits" -ge 2 ] || fail "expected at least two audit commits, got $commits"

git -C "$REPO" commit --allow-empty -q -m initial
WT="$TMP/worktree"
git -C "$REPO" worktree add -q "$WT"
assert_eq "$(cd "$WT" && ENVKIT_HOME="$HOME_DIR" "$ENVKIT" project)" 'owner__repo'

echo 'ok: project URL forms, secret operations, audit log, and worktree resolution'
