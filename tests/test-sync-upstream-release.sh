#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
script="$repo_root/scripts/sync-upstream-release.sh"

if [[ ! -f "$script" ]]; then
  echo "missing script: $script" >&2
  exit 1
fi

content="$(<"$script")"

assert_contains() {
  local needle="$1"
  if [[ "$content" != *"$needle"* ]]; then
    echo "expected script to contain: $needle" >&2
    exit 1
  fi
}

assert_not_contains() {
  local needle="$1"
  if [[ "$content" == *"$needle"* ]]; then
    echo "script must not contain: $needle" >&2
    exit 1
  fi
}

assert_contains 'git fetch "$upstream_remote" --tags'
assert_contains "git for-each-ref --sort=-creatordate"
assert_contains "Use latest upstream tag"
assert_contains "read -r"
assert_contains "git switch release"
assert_contains 'git branch "$bookmark_branch" "$selected_tag"'
assert_contains 'git branch "$backup_branch" release'
assert_contains 'git merge --no-ff "$selected_tag"'
assert_contains "git reset --hard"
assert_not_contains "pre-release"

echo "sync script contract ok"
