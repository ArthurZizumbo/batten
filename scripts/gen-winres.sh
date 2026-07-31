#!/usr/bin/env bash
# Generate the Windows VERSIONINFO resources that get linked into batten.exe.
#
# WHY THIS EXISTS, because it will look like decoration to whoever reads it next:
# a Go executable ships with NO version resource at all — no CompanyName, no ProductName, no
# LegalCopyright. Every legitimate Windows program has them, so their absence is one of the cheap
# negative signals a heuristic scanner has to work with. Filling them in costs no measurable bytes
# and is the least invasive thing that can be done about the false positives.
#
# It does NOT promise the false positive goes away. Nothing does — not even a signature. See the
# Windows note in docs/INSTALL.md.
#
# The .syso files are build output: gitignored, regenerated here. Go links them automatically
# because of the _windows_<arch> suffix, so nothing in the Go source refers to them.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

# Accepts v0.1.0, v0.1.0-beta.1, 0.1.0 — anything the tag can be. Everything after the first
# hyphen is a pre-release label, which VERSIONINFO has no field for: the four numbers are all
# Windows shows in the Details tab, and they must BE numbers or the resource compiler rejects them.
raw="${1:-0.0.0}"
ver_str="${raw#v}"   # strip the leading v — goversioninfo rejects "v0.1.0", it wants "0.1.0"
ver="${ver_str%%-*}" # strip -beta.1 and friends, leaving x.y.z for the four numeric fields

major="$(printf '%s' "$ver" | cut -d. -f1)"
minor="$(printf '%s' "$ver" | cut -d. -f2)"
patch="$(printf '%s' "$ver" | cut -d. -f3)"

# A missing component is 0, not empty: goversioninfo takes ints and an empty string is not one.
: "${major:=0}" "${minor:=0}" "${patch:=0}"
for n in "$major" "$minor" "$patch"; do
	case "$n" in
	'' | *[!0-9]*)
		echo "gen-winres: '$raw' does not parse as a version (component '$n' is not a number)" >&2
		exit 1
		;;
	esac
done

# The full string keeps the pre-release label; the four numbers cannot.
go tool goversioninfo \
	-platform-specific \
	-ver-major "$major" -ver-minor "$minor" -ver-patch "$patch" -ver-build 0 \
	-product-ver-major "$major" -product-ver-minor "$minor" -product-ver-patch "$patch" -product-ver-build 0 \
	-file-version "$ver_str" -product-version "$ver_str" \
	cmd/batten/versioninfo.json

# -platform-specific ignores -o and writes into the working directory, so move them next to the
# main package, which is the only place the Go toolchain looks. It emits four architectures;
# .goreleaser.yaml builds two, and an unused .syso is dead weight in the tree rather than in the
# binary — Go only links the one whose suffix matches GOARCH.
for f in resource_windows_*.syso; do
	[ -e "$f" ] || continue
	case "$f" in
	*_amd64.syso | *_arm64.syso) mv -f "$f" "cmd/batten/$f" ;;
	*) rm -f "$f" ;;
	esac
done

echo "gen-winres: $raw -> $major.$minor.$patch.0 ($ver_str)"
ls cmd/batten/resource_windows_*.syso
