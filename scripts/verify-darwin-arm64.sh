#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
output="$root/build/darwin-arm64"
app="$output/mHost Phase 0.app"
proxy="$app/Contents/Resources/bin/mhost-dns-proxy"

test "$(uname -s)" = "Darwin"
test "$(uname -m)" = "arm64"
test -d "$app"
test -x "$proxy"

executable=$(plutil -extract CFBundleExecutable raw -o - "$app/Contents/Info.plist")
main="$app/Contents/MacOS/$executable"
test -x "$main"
test "$(lipo -archs "$main")" = "arm64"
test "$(lipo -archs "$proxy")" = "arm64"
codesign --verify --strict "$proxy"
codesign --verify --strict "$app"
"$proxy" -check

dmg=$(find "$output" -maxdepth 1 -name '*.dmg' -type f -print -quit)
test -n "$dmg"
commit=${GITHUB_SHA:-$(git -C "$root" rev-parse HEAD)}
mygo_version=$(go list -m -f '{{.Version}}' github.com/egoist/mygo)
{
  printf 'commit=%s\n' "$commit"
  printf 'runner_arch=%s\n' "$(uname -m)"
  printf 'go_version=%s\n' "$(go version)"
  printf 'mygo_version=%s\n' "$mygo_version"
  printf 'app=%s\n' "$(basename "$app")"
  printf 'dmg=%s\n' "$(basename "$dmg")"
} >"$output/build-metadata.txt"
(
  cd "$output"
  shasum -a 256 "$(basename "$dmg")" >SHA256SUMS
)
printf 'verified app: %s\n' "$app"
printf 'verified DMG: %s\n' "$dmg"
