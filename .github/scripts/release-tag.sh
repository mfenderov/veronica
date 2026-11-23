#!/usr/bin/env bash
set -euo pipefail

stable_tag='^v(0|[1-9][0-9]*)[.](0|[1-9][0-9]*)[.](0|[1-9][0-9]*)$'

stable_tags() {
  git tag --list --sort=-version:refname "$@" | awk -v pattern="$stable_tag" '$0 ~ pattern'
}

exact=$(stable_tags --points-at HEAD | sed -n '1p')
if [[ -n $exact ]]; then
  printf '%s\n' "$exact"
  exit 0
fi

latest=$(stable_tags | sed -n '1p')
if [[ -z $latest ]] || git merge-base --is-ancestor "$latest" HEAD; then
  next=$(cog bump --auto --dry-run)
  if ! [[ $next =~ $stable_tag ]]; then
    next=$(cog bump --patch --dry-run)
  fi
else
  patch=${latest##*.}
  next="${latest%.*}.$((patch + 1))"
fi

if ! [[ $next =~ $stable_tag ]]; then
  printf 'Cocogitto did not return a stable release version: %s\n' "$next" >&2
  exit 1
fi

git tag "$next" HEAD
printf '%s\n' "$next"
