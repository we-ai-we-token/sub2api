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
      printf 'Enter tag to merge into release: ' >&2
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

  run git switch release

  local bookmark_branch="upstream-${selected_tag}"
  local backup_branch="backup/release-before-${selected_tag}"

  if git rev-parse -q --verify "refs/heads/$bookmark_branch" >/dev/null; then
    echo "Branch $bookmark_branch already exists; leaving it as is." >&2
  else
    run git branch "$bookmark_branch" "$selected_tag"
  fi

  if git rev-parse -q --verify "refs/heads/$backup_branch" >/dev/null; then
    echo "Branch $backup_branch already exists; leaving it as is." >&2
  else
    run git branch "$backup_branch" release
  fi

  run git merge --no-ff "$selected_tag" -m "Merge tag '$selected_tag' into release"

  echo "Merged $selected_tag into release."
  echo "Next steps: resolve the recurring conflicts documented in CLAUDE.md, commit the VERSION bump separately,"
  echo "then run the verification checklist (backend build/vet/unit/integration, frontend typecheck/test) before pushing."
  echo "If the merge goes wrong: git reset --hard $backup_branch"
}

main "$@"
