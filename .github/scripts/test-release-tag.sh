#!/usr/bin/env bash
set -euo pipefail

script=$(cd "$(dirname "$0")" && pwd)/release-tag.sh
scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT

new_repo() {
  cd "$scratch"
  git init -q -b main "$1"
  cd "$1"
  git config user.name 'Release test'
  git config user.email 'release-test@example.invalid'
  printf 'tag_prefix = "v"\nbranch_whitelist = ["main"]\n' > cog.toml
  git add cog.toml
  commit 'feat: initialize fixture'
}

commit() {
  local parent=() next
  if git rev-parse --verify HEAD >/dev/null 2>&1; then
    parent=(-p HEAD)
  fi
  next=$(git commit-tree "$(git write-tree)" "${parent[@]+${parent[@]}}" -m "$1")
  git update-ref refs/heads/main "$next"
}

expect_tag() {
  local expected=$1 head actual
  head=$(git rev-parse HEAD)
  actual=$(bash "$script")
  if [[ $actual != "$expected" || $(git rev-parse "$expected^{commit}") != "$head" || $(git rev-parse HEAD) != "$head" || -n $(git status --porcelain) ]]; then
    printf 'Expected %s at unchanged HEAD; got %s\n' "$expected" "$actual" >&2
    exit 1
  fi
}

new_repo off-history
unrelated=$(git commit-tree 'HEAD^{tree}' -m 'feat: unrelated published history')
git tag v0.1.0 "$unrelated"
git tag v0.9.9 "$unrelated"
git tag v0.11.0 "$unrelated"
git tag v0x99x0 "$unrelated"
git tag v0.12.0-rc.1 "$unrelated"
commit 'fix: release without rewriting history'
expect_tag v0.11.1
expect_tag v0.11.1
[[ $(git rev-parse v0.11.0) = "$unrelated" ]]
[[ $(git tag --list 'v0.11.*' | wc -l | tr -d ' ') = 2 ]]

new_repo reachable
commit 'docs: describe usage'
expect_tag v0.1.0
commit 'docs: update usage'
expect_tag v0.1.1
commit 'fix: handle a missing entry'
expect_tag v0.1.2
commit 'feat: add a new operation'
expect_tag v0.2.0

printf 'Release tag tests passed.\n'
