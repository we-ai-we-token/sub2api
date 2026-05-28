#!/usr/bin/env bash
set -euo pipefail

upstream_remote="${UPSTREAM_REMOTE:-upstream}"

run() {
  printf '+ %q' "$@"
  printf '\n'
  "$@"
}

require_clean_worktree() {
  if [[ -n "$(git status --porcelain)" ]]; then
    echo "Working tree is not clean. Commit or stash changes before syncing." >&2
    exit 1
  fi
}

ensure_upstream_remote() {
  if ! git remote get-url "$upstream_remote" >/dev/null 2>&1; then
    echo "Missing remote '$upstream_remote'. Add it first:" >&2
    echo "  git remote add $upstream_remote <upstream-repository-url>" >&2
    exit 1
  fi
}

list_upstream_tags() {
  git for-each-ref --sort=-creatordate --format='%(refname:short)' "refs/tags" | head -20
}

select_tag() {
  local latest_tag="$1"
  local answer=""
  local selected_tag=""

  echo "Latest upstream tag: $latest_tag" >&2
  printf 'Use latest upstream tag %s? [Y/n] ' "$latest_tag" >&2
  read -r answer

  case "$answer" in
    ""|y|Y|yes|YES)
      selected_tag="$latest_tag"
      ;;
    *)
      echo "Recent tags:" >&2
      list_upstream_tags >&2
      printf 'Enter tag to merge into pre-release: ' >&2
      read -r selected_tag
      ;;
  esac

  if [[ -z "$selected_tag" ]]; then
    echo "No tag selected." >&2
    exit 1
  fi

  if ! git rev-parse -q --verify "refs/tags/$selected_tag" >/dev/null; then
    echo "Tag not found: $selected_tag" >&2
    exit 1
  fi

  echo "$selected_tag"
}

main() {
  require_clean_worktree
  ensure_upstream_remote

  run git fetch "$upstream_remote" --tags

  local latest_tag=""
  latest_tag="$(git for-each-ref --sort=-creatordate --format='%(refname:short)' "refs/tags" | head -1)"
  if [[ -z "$latest_tag" ]]; then
    echo "No tags found after fetching $upstream_remote." >&2
    exit 1
  fi

  local selected_tag=""
  selected_tag="$(select_tag "$latest_tag")"

  run git switch pre-release
  run git merge --no-ff "$selected_tag"

  echo "Merged $selected_tag into pre-release."
  echo "Next steps: run the project verification on pre-release, then merge pre-release into release when it passes."
  echo "This script stops after updating pre-release; it never merges into release."
}

main "$@"
