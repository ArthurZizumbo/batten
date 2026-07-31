#!/usr/bin/env bash
# Everything about a release that can be checked BEFORE publishing one.
#
#   scripts/release-check.sh v0.1.0-beta.1
#
# The reason this file exists is written in the plan as an admission: the release path had been
# "verified by reading it". Reading it is what shipped a bootstrap that installed the binary
# where no hook would ever invoke it. So this executes instead:
#
#   1. the tag agrees with both hand-written version numbers (the same check release.yml runs,
#      run before the tag exists rather than after it is pushed);
#   2. all six platforms actually cross-compile with the release settings;
#   3. each archive is named exactly what bootstrap.sh will ask
#      releases/latest/download for — the contract that, when it drifted, 404'd on
#      every platform at once;
#   4. each archive really holds a binary FOR that platform, checked by magic bytes. A
#      cross-compile that silently produced the host's binary would pass every name check and
#      fail on the user's machine.
#
# What it cannot check is what only exists after publishing: that
# releases/latest/download resolves. `prerelease: auto` marks a `-beta` tag as a prerelease and
# GitHub excludes prereleases from `latest`, so the release needs
# `gh release edit <tag> --prerelease=false --latest` before the six URLs answer 200. Verify that
# with curl against the real URLs, after.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"

tag="${1:-}"
if [ -z "$tag" ]; then
  echo "usage: $0 <tag>   (e.g. v0.1.0-beta.1 — the leading v is required, release.yml triggers on 'v*')" >&2
  exit 2
fi
case "$tag" in
  v*) ;;
  *) echo "FAIL: the tag must start with v; release.yml triggers on tags: ['v*'] and '$tag' would publish nothing" >&2
     exit 1 ;;
esac
version="${tag#v}"

fail=0
note() { printf '  %s\n' "$*"; }
bad() { printf 'FAIL: %s\n' "$*" >&2; fail=1; }

echo "== 1. the manifests agree with $tag"
for f in plugin/claude-code/.claude-plugin/plugin.json .claude-plugin/marketplace.json; do
  found="$(grep -o '"version": *"[^"]*"' "$f" | head -1 | sed 's/.*"version": *"//;s/"//')"
  if [ "$found" != "$version" ]; then
    bad "$f declares version $found, but the tag is $tag"
  else
    note "ok  $f -> $found"
  fi
done

echo "== 2..4. the six assets"
dist="$root/dist/release-check"
rm -rf "$dist"
mkdir -p "$dist"

# Kept in step with .goreleaser.yaml by hand, because goreleaser is not a dependency of this
# repo and a check you cannot run locally is a check nobody runs. If the yaml changes, change
# these lines in the same commit — a preflight that builds something other than what ships is
# worse than no preflight, because it reports READY TO TAG about the wrong artifact.
platforms="linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64"
ldflags="-s -w -X main.version=${version}"
flags="-trimpath" # .goreleaser.yaml builds[].flags

# .goreleaser.yaml `before.hooks`: the Windows VERSIONINFO resource. Go links the .syso whose
# _windows_<arch> suffix matches, so generating it here is what makes the two Windows binaries
# below the same ones the release publishes.
bash "$(dirname "$0")/gen-winres.sh" "$version" >/dev/null

for p in $platforms; do
  os="${p%/*}"; arch="${p#*/}"
  ext=""; [ "$os" = windows ] && ext=".exe"
  out="$dist/$os-$arch"
  mkdir -p "$out"

  if ! CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
       go build $flags -ldflags "$ldflags" -o "$out/batten${ext}" ./cmd/batten 2>"$out/build.log"; then
    bad "$os/$arch does not build:"; sed 's/^/      /' "$out/build.log" >&2
    continue
  fi

  # The magic bytes. `file` is not on every machine, and this is three cases.
  magic="$(od -An -tx1 -N4 "$out/batten${ext}" | tr -d ' \n')"
  case "$os" in
    linux)   want=7f454c46; kind=ELF ;;                 # \x7fELF
    darwin)  want=cffaedfe; kind="Mach-O 64" ;;         # little-endian MH_MAGIC_64
    windows) want=4d5a9000; kind="PE (MZ)" ;;           # MZ
  esac
  if [ "$magic" != "$want" ]; then
    bad "$os/$arch built something that is not $kind (magic $magic, wanted $want)"
    continue
  fi

  # The name is the contract with bootstrap.sh's ${base}/batten_${os}_${arch}.tar.gz.
  name="batten_${os}_${arch}.tar.gz"
  ( cd "$out" && cp "$root/README.md" "$root/LICENSE" "$root/batten.schema.json" . \
    && tar -czf "$dist/$name" "batten${ext}" README.md LICENSE batten.schema.json )

  # And the archive has to hand bootstrap a file at the root called exactly batten[.exe].
  #
  # Capture, then match in the shell — no `tar | grep -q`. `grep -q` exits at the first match and
  # closes the pipe, tar takes SIGPIPE, and under `set -o pipefail` the pipeline reports failure
  # for a check that just SUCCEEDED. Every archive here was reported broken by a passing test.
  # Same shape as the mode guard in ci.yml: the shell's exit codes disagree with the question.
  list="$(tar -tzf "$dist/$name")"
  case $'\n'"$list"$'\n' in
    *$'\n'"batten${ext}"$'\n'*) ;;
    *) bad "$name has no batten${ext} at its root; bootstrap looks for exactly that"
       continue ;;
  esac
  note "ok  $name  ($kind, $(wc -c <"$out/batten${ext}") bytes)"
done

echo "== the six URLs a first run will fetch"
for p in $platforms; do
  os="${p%/*}"; arch="${p#*/}"
  echo "  https://github.com/ArthurZizumbo/batten/releases/latest/download/batten_${os}_${arch}.tar.gz"
done

if [ "$fail" != 0 ]; then
  echo
  echo "NOT READY TO TAG." >&2
  exit 1
fi

cat <<EOS

READY TO TAG. What is verified and what is not:

  verified here   the manifests match $tag; six platforms build; six archives are named what
                  bootstrap asks for and hold a binary for the right OS.
  verified by     go test ./internal/install — the real bootstrap.sh and bootstrap.ps1
  the suite       installing a real archive into \${CLAUDE_PLUGIN_ROOT}/bin and running it.
  NOT verifiable  that releases/latest/download resolves. 'prerelease: auto' will mark $tag a
  before          prerelease and GitHub excludes those from 'latest'. After the workflow finishes:
  publishing        gh release edit $tag --prerelease=false --latest
                  and then curl -sIL each URL above and confirm six 200s.
EOS
