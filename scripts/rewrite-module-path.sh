#!/usr/bin/env bash
# Rewrite the Go module path from the upstream BirdNET-Go path to the SoundNet fork path.
#
# Used twice:
#   1. Once, in M0, to perform the fork's module rename.
#   2. Repeatedly, before merging upstream: check out the upstream branch, run this,
#      commit, then merge. Upstream's import lines then already match ours, which turns
#      what would be ~1,190 conflicting files back into a clean merge.
#
# Deliberately NARROW. It rewrites only files where the string is a Go module path:
#   - *.go          import statements
#   - go.mod        module directive
#   - .mockery.yaml codegen package paths
#
# It deliberately does NOT touch:
#   - LICENSE, NOTICE, AUTHORS  - attribution, must name upstream (CC BY-NC-SA requirement)
#   - *.md                      - user-facing docs describing upstream install/releases
#   - Docker*/Podman*, CI yml   - container image refs (ghcr.io/tphakala/...), a separate concern
#
# Usage: scripts/rewrite-module-path.sh [--check]
#   --check  report what would change, modify nothing (exit 1 if changes are pending)

set -euo pipefail

OLD_PATH="github.com/tphakala/birdnet-go"
NEW_PATH="github.com/bert386/soundnet-go"

CHECK_ONLY=0
[[ "${1:-}" == "--check" ]] && CHECK_ONLY=1

cd "$(dirname "$0")/.."

mapfile -t FILES < <(
  {
    grep -rl --include="*.go" -F "$OLD_PATH" . --exclude-dir=.git || true
    grep -l  -F "$OLD_PATH" go.mod || true
    grep -l  -F "$OLD_PATH" .mockery.yaml 2>/dev/null || true
  } | sort -u
)

if [[ ${#FILES[@]} -eq 0 ]]; then
  echo "No occurrences of ${OLD_PATH} in Go module path locations - nothing to do."
  exit 0
fi

echo "Module path rewrite: ${OLD_PATH} -> ${NEW_PATH}"
echo "Files affected: ${#FILES[@]}"

if [[ $CHECK_ONLY -eq 1 ]]; then
  printf '%s\n' "${FILES[@]}" | head -20
  [[ ${#FILES[@]} -gt 20 ]] && echo "  ... and $(( ${#FILES[@]} - 20 )) more"
  echo "(--check: nothing modified)"
  exit 1
fi

printf '%s\0' "${FILES[@]}" | xargs -0 sed -i "s|${OLD_PATH}|${NEW_PATH}|g"

echo "Done. Verify with: go build ./... && go vet ./..."
