#!/usr/bin/env bash
# Moves the lateregate pin of every private repository in an organization to
# ci-gate's latest release, from a workstation.
#
# A public repository moves its pin on a hosted runner every night, through
# ci-gate-bump.yml in latere-ai/ci. A private repository runs no GitHub
# Actions: its bar, image, release and deploy run on a workstation, and so
# does this. Run it after each ci-gate release (docs/releasing.md).
#
# It works in the checkouts the workstation already has, one folder per
# repository under a common folder (default: the folder holding this ci-gate
# checkout), never in a clone of its own. For each repository it brings the
# checkout up to its default branch on origin, finds the go.mod that pins
# latere.ai/x/ci-gate/cmd/lateregate as a tool, moves the pin with
# `go get -tool` and `go mod tidy`, runs the whole bar (`lateregate check
# -all`), and commits `gate: ci-gate <version>` to the default branch and
# pushes it only when every gate passes. A checkout that is not on its default
# branch, has uncommitted changes to tracked files, or holds commits origin
# does not have is someone's work in progress: it is skipped and named, never
# touched. A repository whose bar fails is put back as it was found, its log
# kept, and the script exits non-zero after trying every repository.
#
# Usage:
#   scripts/bump-private.sh [-n] [-d folder] [-o org] [-x repository]... [repository...]
#
#   -n          report each pin and what would move; change and push nothing
#   -d folder   the folder holding one checkout per repository (default: the
#               folder that holds this ci-gate checkout)
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
root=$(cd "$(dirname "$0")/../.." && pwd)
while getopts nd:o:x: flag; do
  case "$flag" in
    n) dry_run=true ;;
    d) root=$OPTARG ;;
    o) org=$OPTARG ;;
    x) held="$held$OPTARG " ;;
    *) sed -n '/^# Usage:/,/^# Needs/p' "$0" | sed 's/^# \{0,1\}//' >&2; exit 2 ;;
  esac
done
shift $((OPTIND - 1))

# A repository is a plain name: it names a folder under the checkouts folder.
for name in "$@"; do
  if ! [[ $name =~ ^[A-Za-z0-9._-]+$ ]] || [ "$name" = . ] || [ "$name" = .. ]; then
    echo "bump-private: '$name' is not a repository name" >&2
    sed -n '/^# Usage:/,/^# Needs/p' "$0" | sed 's/^# \{0,1\}//' >&2
    exit 2
  fi
done
[ -d "$root" ] || { echo "bump-private: the checkouts folder $root does not exist" >&2; exit 2; }

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

logs=$(mktemp -d "${TMPDIR:-/tmp}/bump-private.XXXXXX")
keep=false
cleanup() { if [ "$keep" = false ]; then rm -rf "$logs"; fi; }
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

# restore puts go.mod and go.sum of module $2 in checkout $1 back to HEAD.
restore() { git -C "$1" checkout -q -- "$2/go.mod" "$2/go.sum" 2>/dev/null || git -C "$1" checkout -q -- "$2/go.mod"; }

for repo in "${repos[@]}"; do
  case "$held" in
    *" $repo "*) note "$repo" "held back (-x); skipped"; continue ;;
  esac
  dir="$root/$repo"
  log="$logs/$repo.log"
  echo
  echo "== $repo"
  if [ ! -d "$dir/.git" ]; then
    note "$repo" "no checkout at $dir; skipped"
    continue
  fi

  if ! branch=$(git -C "$dir" symbolic-ref --short refs/remotes/origin/HEAD 2>> "$log"); then
    fail "$repo" "the default branch is unknown: origin/HEAD is not set in $dir ($log)"
    continue
  fi
  branch=${branch#origin/}
  if ! on=$(git -C "$dir" symbolic-ref --short HEAD 2>> "$log") || [ "$on" != "$branch" ]; then
    note "$repo" "$dir is not on $branch; skipped, left as it is"
    continue
  fi
  if [ -n "$(git -C "$dir" status --porcelain --untracked-files=no)" ]; then
    note "$repo" "$dir has uncommitted changes; skipped, left as it is"
    continue
  fi
  if ! git -C "$dir" fetch -q origin "$branch" >> "$log" 2>&1; then
    fail "$repo" "fetching $branch failed ($log)"
    continue
  fi
  if [ "$(git -C "$dir" rev-list --count "origin/$branch..HEAD")" != 0 ]; then
    note "$repo" "$dir holds commits origin does not have; skipped, left as it is"
    continue
  fi
  # A dry run reads the pin from the checkout as it is and moves nothing.
  if [ "$dry_run" = false ] && ! git -C "$dir" merge -q --ff-only "origin/$branch" >> "$log" 2>&1; then
    fail "$repo" "fast-forwarding $dir to origin/$branch failed ($log)"
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
    rm -f "$log"
    continue
  fi
  if [ "$count" -gt 1 ]; then
    fail "$repo" "more than one go.mod pins the tool"
    continue
  fi
  mod="$dir/$module"

  # The latest release comes from the repository, not the module proxy: the
  # proxy answers @latest from a cache that lags a release this script runs
  # right after.
  if ! current=$(go -C "$mod" list -m -f '{{.Version}}' latere.ai/x/ci-gate 2>> "$log") ||
    ! latest=$(GOPROXY=direct go -C "$mod" list -m -f '{{.Version}}' latere.ai/x/ci-gate@latest 2>> "$log"); then
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
    rm -f "$log"
    continue
  fi
  if [ "$dry_run" = true ]; then
    note "$repo" "would move $current -> $latest (module ${module})"
    rm -f "$log"
    continue
  fi

  if ! before=$(directives "$mod" 2>> "$log"); then
    fail "$repo" "go mod edit could not read go.mod ($log)"
    continue
  fi
  if ! { go -C "$mod" get -tool "latere.ai/x/ci-gate/cmd/lateregate@$latest" &&
    go -C "$mod" mod tidy; } >> "$log" 2>&1; then
    restore "$dir" "$module"
    fail "$repo" "moving the pin failed ($log)"
    continue
  fi
  if ! after=$(directives "$mod" 2>> "$log"); then
    restore "$dir" "$module"
    fail "$repo" "go mod edit could not read go.mod after moving the pin ($log)"
    continue
  fi
  if [ "$before" != "$after" ]; then
    restore "$dir" "$module"
    fail "$repo" "moving the pin to $latest changes go.mod from $before to $after; raise Go by hand first"
    continue
  fi

  echo "running the whole bar on $latest; the log is $log"
  if ! (cd "$dir" && go -C "$mod" tool lateregate check -all -C "$dir") >> "$log" 2>&1; then
    tail -n 30 "$log"
    restore "$dir" "$module"
    fail "$repo" "the bar fails on $latest; $dir is back as it was, log $log"
    continue
  fi

  if ! { git -C "$dir" add "$module/go.mod" "$module/go.sum" &&
    git -C "$dir" commit -q -m "gate: ci-gate $latest"; } >> "$log" 2>&1; then
    git -C "$dir" reset -q -- "$module/go.mod" "$module/go.sum" >> "$log" 2>&1 || true
    restore "$dir" "$module"
    fail "$repo" "committing the new pin failed ($log)"
    continue
  fi
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
    # The commit is undone, so the checkout holds nothing origin lacks.
    git -C "$dir" reset -q --soft HEAD~1 >> "$log" 2>&1 &&
      git -C "$dir" reset -q -- "$module/go.mod" "$module/go.sum" >> "$log" 2>&1 &&
      restore "$dir" "$module"
    fail "$repo" "the push to $branch was refused; $dir is back as it was, log $log"
    continue
  fi
  note "$repo" "moved $current -> $latest, pushed $(git -C "$dir" rev-parse --short HEAD) to $branch"
  rm -f "$log"
done

echo
echo "== summary"
printf '%s\n' "${summary[@]}"
if [ "$failed" -gt 0 ]; then
  echo "$failed repository(ies) failed; logs are kept in $logs" >&2
  exit 1
fi
