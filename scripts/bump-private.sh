#!/usr/bin/env bash
# Moves the lateregate pin of every private repository in an organization to
# ci-gate's latest release, from a workstation.
#
# A public repository moves its pin on a hosted runner every night, through
# ci-gate-bump.yml in latere-ai/ci. A private repository runs no GitHub
# Actions: its bar, image, release and deploy run on a workstation, and so
# does this. Run it after each ci-gate release (docs/releasing.md).
#
# For each repository, in a fresh clone under a temporary folder, it finds the
# go.mod that pins latere.ai/x/ci-gate/cmd/lateregate as a tool, moves the pin
# with `go get -tool` and `go mod tidy`, runs the whole bar on the result
# (`lateregate check -all`), and pushes `gate: ci-gate <version>` to the
# default branch only when every gate passes. A repository whose bar fails is
# left unpushed, its clone and log kept, and the script exits non-zero after
# trying every repository.
#
# Usage:
#   scripts/bump-private.sh [-n] [-o org] [-x repository]... [repository...]
#
#   -n          report each pin and what would move; clone, change and push
#               nothing beyond the clone
#   -o org      the GitHub organization (default latere-ai)
#   -x name     leave this repository out, for one whose pin is held back on
#               purpose; repeat it for more than one
#   repository  only these repositories, by name; default every private,
#               unarchived repository of the organization
#
# Needs gh (signed in), git that can push to the organization, go, and what
# each repository's bar needs, as `make build` does.
set -euo pipefail

org=latere-ai
dry_run=false
held=" "
while getopts no:x: flag; do
  case "$flag" in
    n) dry_run=true ;;
    o) org=$OPTARG ;;
    x) held="$held$OPTARG " ;;
    *) sed -n '/^# Usage:/,/^# Needs/p' "$0" | sed 's/^# \{0,1\}//' >&2; exit 2 ;;
  esac
done
shift $((OPTIND - 1))

for tool in gh git go jq; do
  command -v "$tool" >/dev/null || { echo "bump-private: $tool is not on PATH" >&2; exit 2; }
done

if [ "$#" -gt 0 ]; then
  repos=("$@")
else
  repos=()
  while IFS= read -r name; do repos+=("$name"); done < <(
    gh repo list "$org" --visibility private --no-archived --limit 1000 --json name --jq '.[].name' | sort
  )
fi
[ "${#repos[@]}" -gt 0 ] || { echo "bump-private: no private repositories in $org" >&2; exit 1; }

work=$(mktemp -d "${TMPDIR:-/tmp}/bump-private.XXXXXX")
keep=false
cleanup() { if [ "$keep" = false ]; then rm -rf "$work"; fi; }
trap cleanup EXIT

# pins_tool succeeds when the go.mod at $1 declares the lateregate tool, in a
# tool block or on a tool line of its own.
pins_tool() {
  awk '
    /^[[:space:]]*tool[[:space:]]*\(/ { block = 1; next }
    block && /^[[:space:]]*\)/ { block = 0; next }
    block && $1 == "latere.ai/x/ci-gate/cmd/lateregate" { found = 1 }
    /^[[:space:]]*tool[[:space:]]+latere\.ai\/x\/ci-gate\/cmd\/lateregate/ { found = 1 }
    END { exit !found }
  ' "$1"
}

# directives prints the go and toolchain lines, which moving the pin must not
# raise: a newer Go is a change of its own, made by hand.
directives() { go -C "$1" mod edit -json | jq -r '"go \(.Go // "none"), toolchain \(.Toolchain // "none")"'; }

semver='^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$'
summary=()
failed=0

note() { summary+=("$(printf '%-24s %s' "$1" "$2")"); echo "$1: $2"; }
fail() { note "$1" "FAILED: $2"; failed=$((failed + 1)); keep=true; }

for repo in "${repos[@]}"; do
  case "$held" in
    *" $repo "*) note "$repo" "held back (-x); skipped"; continue ;;
  esac
  dir="$work/$repo"
  log="$work/$repo.log"
  echo
  echo "== $repo"
  if ! git clone -q --filter=blob:none "https://github.com/$org/$repo.git" "$dir" 2> "$log"; then
    fail "$repo" "clone failed ($log)"
    continue
  fi

  module=""
  count=0
  while IFS= read -r mod; do
    if pins_tool "$dir/$mod"; then
      module=$(dirname "$mod")
      count=$((count + 1))
    fi
  done < <(git -C "$dir" ls-files 'go.mod' '*/go.mod')
  if [ "$count" -eq 0 ]; then
    note "$repo" "no lateregate pin; skipped"
    rm -rf "$dir" "$log"
    continue
  fi
  if [ "$count" -gt 1 ]; then
    fail "$repo" "more than one go.mod pins the tool"
    continue
  fi
  mod="$dir/$module"

  if ! current=$(go -C "$mod" list -m -f '{{.Version}}' latere.ai/x/ci-gate 2>> "$log") ||
    ! latest=$(go -C "$mod" list -m -f '{{.Version}}' latere.ai/x/ci-gate@latest 2>> "$log"); then
    fail "$repo" "go list could not read the pin ($log)"
    continue
  fi
  for v in "$current" "$latest"; do
    if ! printf '%s' "$v" | grep -Eq "$semver"; then
      fail "$repo" "go list answered '$v', which is not a release version"
      continue 2
    fi
  done
  if [ "$current" = "$latest" ]; then
    note "$repo" "at $latest"
    rm -rf "$dir" "$log"
    continue
  fi
  if [ "$dry_run" = true ]; then
    note "$repo" "would move $current -> $latest (module ${module})"
    rm -rf "$dir" "$log"
    continue
  fi

  before=$(directives "$mod")
  if ! { go -C "$mod" get -tool "latere.ai/x/ci-gate/cmd/lateregate@$latest" &&
    go -C "$mod" mod tidy; } >> "$log" 2>&1; then
    fail "$repo" "moving the pin failed ($log)"
    continue
  fi
  after=$(directives "$mod")
  if [ "$before" != "$after" ]; then
    fail "$repo" "moving the pin to $latest changes go.mod from $before to $after; raise Go by hand first"
    continue
  fi

  echo "running the whole bar on $latest; the log is $log"
  if ! (cd "$dir" && go -C "$mod" tool lateregate check -all -C "$dir") >> "$log" 2>&1; then
    tail -n 30 "$log"
    fail "$repo" "the bar fails on $latest; clone $dir, log $log"
    continue
  fi

  git -C "$dir" add "$module/go.mod" "$module/go.sum"
  git -C "$dir" commit -q -m "gate: ci-gate $latest"
  branch=$(git -C "$dir" symbolic-ref --short refs/remotes/origin/HEAD)
  branch=${branch#origin/}
  git -C "$dir" config core.hooksPath .githooks
  pushed=false
  for attempt in 1 2 3; do
    if git -C "$dir" push -q origin "HEAD:refs/heads/$branch" >> "$log" 2>&1; then
      pushed=true
      break
    fi
    sleep $((attempt * 5))
    git -C "$dir" fetch -q origin "$branch" >> "$log" 2>&1 || true
    if ! git -C "$dir" rebase -q "origin/$branch" >> "$log" 2>&1; then
      git -C "$dir" rebase --abort >> "$log" 2>&1 || true
      break
    fi
  done
  if [ "$pushed" = false ]; then
    fail "$repo" "the push to $branch was refused; clone $dir, log $log"
    continue
  fi
  note "$repo" "moved $current -> $latest, pushed $(git -C "$dir" rev-parse --short HEAD) to $branch"
  rm -rf "$dir" "$log"
done

echo
echo "== summary"
printf '%s\n' "${summary[@]}"
if [ "$failed" -gt 0 ]; then
  echo "$failed repository(ies) failed; clones and logs are kept in $work" >&2
  exit 1
fi
