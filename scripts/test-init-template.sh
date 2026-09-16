#!/usr/bin/env bash

set -euo pipefail

template_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
test_root=$(mktemp -d "${TMPDIR:-/tmp}/mcp-template-init.XXXXXX")

cleanup() {
  rm -rf -- "$test_root"
}
trap cleanup EXIT

fail() {
  printf 'error: %s\n' "$1" >&2
  exit 1
}

assert_contains() {
  local expected=$1
  local file=$2
  grep -Fqx -- "$expected" "$file" || fail "expected $file to contain: $expected"
}

assert_not_contains() {
  local unexpected=$1
  local file=$2
  if grep -Fqx -- "$unexpected" "$file"; then
    fail "did not expect $file to contain: $unexpected"
  fi
}

assert_actions_pinned() {
  local file line revision found

  for file in "$@"; do
    found=false
    while IFS= read -r line; do
      if [[ "$line" =~ ^[[:space:]]*-[[:space:]]+uses:[[:space:]]*[^@[:space:]#]+@([^[:space:]#]+) ]]; then
        found=true
        revision=${BASH_REMATCH[1]}
        [[ "$revision" =~ ^[0-9a-f]{40}$ ]] \
          || fail "expected $file to pin Actions by a 40-character lowercase SHA: $line"
      elif [[ "$line" =~ ^[[:space:]]*-[[:space:]]+uses: ]]; then
        fail "expected $file to pin every Action by a 40-character lowercase SHA: $line"
      fi
    done <"$file"
    [ "$found" = true ] || fail "expected $file to contain an Action reference"
  done
}

copy_template() {
  local destination=$1
  mkdir -p "$destination"
  cp -R "$template_root"/. "$destination"
}

initialize() {
  local destination=$1
  local module=$2
  local binary=$3
  local npm_package=$4
  local image=$5
  shift 5

  "$destination/scripts/init-template.sh" \
    --module "$module" \
    --binary "$binary" \
    --npm-package "$npm_package" \
    --image "$image" \
    "$@"
}

assert_ignored() {
  local destination=$1
  local path=$2
  git -C "$destination" check-ignore --no-index -q "$path" \
    || fail "expected $path to be ignored"
}

assert_not_ignored() {
  local destination=$1
  local path=$2
  [ -f "$destination/$path" ] || fail "expected generated file: $path"
  if git -C "$destination" check-ignore --no-index -q "$path"; then
    fail "did not expect $path to be ignored"
  fi
}

ghcr_template="$test_root/ghcr"
copy_template "$ghcr_template"
initialize "$ghcr_template" example.com/ghcr-check ghcr-check @acme/ghcr-check ghcr.io/acme/ghcr-check --with-release --use-ghcr

ghcr_workflow="$ghcr_template/.github/workflows/release-docker.yaml"
assert_contains '      packages: write' "$ghcr_workflow"
assert_contains '          username: ${{ github.actor }}' "$ghcr_workflow"
assert_contains '          password: ${{ secrets.GITHUB_TOKEN }}' "$ghcr_workflow"
assert_not_contains '          username: ${{ secrets.DOCKER_USERNAME }}' "$ghcr_workflow"
assert_not_contains '          password: ${{ secrets.DOCKER_PASSWORD }}' "$ghcr_workflow"

packages_count=$(grep -Fxc '      packages: write' "$ghcr_workflow" || true)
[ "$packages_count" -eq 1 ] || fail 'expected packages: write only once in the GHCR workflow'

release_line=$(grep -n '^  release:$' "$ghcr_workflow" | cut -d: -f1)
packages_line=$(grep -n '^      packages: write$' "$ghcr_workflow" | cut -d: -f1)
[ "$packages_line" -gt "$release_line" ] || fail 'expected packages: write to belong to the release job'

build_workflow="$ghcr_template/.github/workflows/build.yaml"
assert_contains '        run: ./bin/${BINARY_NAME} version' "$build_workflow"
assert_not_contains '          ./bin/${BINARY_NAME} tools list' "$build_workflow"
assert_contains '        run: go test -race ./...' "$build_workflow"
assert_contains '        run: make test-template-init' "$build_workflow"

npm_workflow="$ghcr_template/.github/workflows/release-npm.yaml"
assert_contains '  validate-release:' "$npm_workflow"
assert_contains '    needs: [resolve-platforms, validate-tag, validate-release]' "$npm_workflow"
assert_contains '    needs: [validate-tag, validate-release, build-platform]' "$npm_workflow"
assert_contains '          registry-url: ${{ vars.NPM_REGISTRY_URL }}' "$npm_workflow"
assert_contains '          NPM_REGISTRY_URL: ${{ vars.NPM_REGISTRY_URL }}' "$npm_workflow"
assert_contains '    needs: [resolve-platforms, validate-tag, validate-release]' "$ghcr_workflow"
assert_actions_pinned "$build_workflow" "$npm_workflow" "$ghcr_workflow"

assert_ignored "$ghcr_template" bin/ghcr-check
assert_ignored "$ghcr_template" .auto-runs/report.md
assert_not_ignored "$ghcr_template" npm/acme-ghcr-check/bin/index.js

generic_template="$test_root/generic"
copy_template "$generic_template"
initialize "$generic_template" example.com/generic-check generic-check @acme/generic-check docker.io/acme/generic-check --with-release

generic_workflow="$generic_template/.github/workflows/release-docker.yaml"
generic_npm_workflow="$generic_template/.github/workflows/release-npm.yaml"
assert_not_contains '      packages: write' "$generic_workflow"
assert_contains '          username: ${{ secrets.DOCKER_USERNAME }}' "$generic_workflow"
assert_contains '          password: ${{ secrets.DOCKER_PASSWORD }}' "$generic_workflow"
assert_not_contains '          username: ${{ github.actor }}' "$generic_workflow"
assert_not_contains '          password: ${{ secrets.GITHUB_TOKEN }}' "$generic_workflow"
assert_contains '  validate-release:' "$generic_workflow"
assert_actions_pinned "$generic_workflow" "$generic_npm_workflow"
assert_not_ignored "$generic_template" npm/acme-generic-check/bin/index.js

invalid_template="$test_root/invalid"
copy_template "$invalid_template"
if initialize "$invalid_template" example.com/missing-release missing-release @acme/missing-release ghcr.io/acme/missing-release --use-ghcr --dry-run >"$test_root/missing-release.out" 2>&1; then
  fail 'expected --use-ghcr without --with-release to fail'
fi
assert_contains 'error: --use-ghcr requires --with-release' "$test_root/missing-release.out"

if initialize "$invalid_template" example.com/wrong-registry wrong-registry @acme/wrong-registry docker.io/acme/wrong-registry --with-release --use-ghcr --dry-run >"$test_root/wrong-registry.out" 2>&1; then
  fail 'expected --use-ghcr with a non-GHCR image to fail'
fi
assert_contains 'error: --use-ghcr requires --image to use the ghcr.io registry' "$test_root/wrong-registry.out"

printf '%s\n' 'Template initialization checks passed.'
