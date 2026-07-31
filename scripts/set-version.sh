#!/usr/bin/env bash
# Set the release version in the one place a human has to touch it: run this, then tag.
#
# WHY THIS IS A SCRIPT AND NOT AN ENVIRONMENT VARIABLE, because that is the obvious question:
# the two files that carry the version are `.claude-plugin/marketplace.json` and
# `plugin/claude-code/.claude-plugin/plugin.json`, and CLAUDE CODE READS THEM AS LITERAL JSON.
# There is no interpolation step between the file on disk and the plugin loader, so `${VERSION}`
# in there is the string `${VERSION}`, not a version. The binary is the opposite case: it takes
# `-X main.version` from GoReleaser and never hardcodes anything.
#
# So the single source of truth is the git tag, and this script is what pushes it into the two
# files that cannot read it themselves. One command, then `git tag`.
#
# scripts/release-check.sh <tag> verifies the result. Run it after this, always: this script
# writes, that one proves.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

if [ $# -ne 1 ]; then
	echo "usage: $0 <version>    # with or without the leading v: v0.1.0-beta.2 or 0.1.0-beta.2" >&2
	echo "then:  scripts/release-check.sh v<version>" >&2
	exit 2
fi

ver="${1#v}" # the manifests carry it WITHOUT the v; the tag carries it WITH one

# Refuse anything that is not a version before writing to two tracked files. A typo here lands in
# the manifests, and the plugin loader reports it as the plugin's identity.
case "$ver" in
'' | *[!0-9A-Za-z.+-]* | [!0-9]*)
	echo "set-version: '$1' does not look like a semver (expected e.g. 0.1.0 or 0.1.0-beta.2)" >&2
	exit 1
	;;
esac

manifests="plugin/claude-code/.claude-plugin/plugin.json .claude-plugin/marketplace.json"

for f in $manifests; do
	[ -f "$f" ] || {
		echo "set-version: $f is missing" >&2
		exit 1
	}
	# Only the FIRST "version" key, which is the manifest's own. Touching a nested one would
	# rewrite something that is not the plugin's version.
	perl -0pi -e 's/("version"\s*:\s*")[^"]*(")/${1}'"$ver"'${2}/' "$f"
	got="$(grep -o '"version" *: *"[^"]*"' "$f" | head -1 | sed 's/.*: *"//;s/"//')"
	if [ "$got" != "$ver" ]; then
		echo "set-version: $f still says '$got' after the rewrite" >&2
		exit 1
	fi
	printf '  %-52s -> %s\n' "$f" "$got"
done

cat <<EOF

Next, in order:
  scripts/release-check.sh v$ver     # proves the manifests, the six cross-compiles and the assets
  git add $manifests
  git commit
  git tag v$ver                      # NOT done here: a published tag cannot be taken back
EOF
