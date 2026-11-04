#!/bin/bash
# SPDX-License-Identifier: GPL-3.0-only
# Copyright (C) 2026 Andrea Casali
#
# Builds a signed, notarized DMG of Covo for testers:
#
#   app/release.sh 0.1.0-beta.1
#
# Needs, once on this Mac (see docs/release.md):
#   - a "Developer ID Application" certificate in the login keychain
#     (COVO_SIGN_IDENTITY picks one when there are several);
#   - notarytool credentials stored as the keychain profile "covo-notary"
#     (COVO_NOTARY_PROFILE for another name).
# Output: dist/Covo-<version>.dmg and its .sha256 next to it.
set -euo pipefail

version="${1:-}"
if [[ ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$ ]]; then
	echo "usage: app/release.sh <version>   e.g. 0.1.0-beta.1" >&2
	exit 2
fi
app=$(cd "$(dirname "$0")" && pwd)
repo=$(dirname "$app")
dist="$repo/dist"
profile="${COVO_NOTARY_PROFILE:-covo-notary}"

if [[ -n "$(git -C "$repo" status --porcelain)" ]]; then
	echo "The working tree has uncommitted changes: commit them first, so the build matches a commit." >&2
	exit 1
fi

identity="${COVO_SIGN_IDENTITY:-}"
if [[ -z "$identity" ]]; then
	identity=$(security find-identity -v -p codesigning | sed -n 's/.*"\(Developer ID Application: [^"]*\)".*/\1/p' | head -1)
fi
if [[ -z "$identity" ]]; then
	echo "No \"Developer ID Application\" certificate in the keychain. See docs/release.md." >&2
	exit 1
fi
if ! xcrun notarytool history --keychain-profile "$profile" >/dev/null 2>&1; then
	echo "No notarytool credentials under the keychain profile \"$profile\". See docs/release.md." >&2
	exit 1
fi
echo "Signing as: $identity"

COVO_VERSION="$version" COVO_BUILD="$(git -C "$repo" rev-list --count HEAD)" "$app/build.sh" >/dev/null
bundle="$app/build/Covo.app"

# Inner executables first, then the app; hardened runtime and a secure
# timestamp are what notarization requires.
sign() { codesign --force --options runtime --timestamp --sign "$identity" "$@"; }
sign "$bundle/Contents/Resources/bin/covo" "$bundle/Contents/Resources/bin/covo-hostshelper"
sign "$bundle"
codesign --verify --deep --strict --verbose=2 "$bundle"

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

# 1. Notarize the app and staple its ticket, so it opens offline too.
ditto -c -k --keepParent "$bundle" "$work/Covo.zip"
xcrun notarytool submit "$work/Covo.zip" --keychain-profile "$profile" --wait
xcrun stapler staple "$bundle"

# 2. The DMG: the app and a link to /Applications; signed, notarized, stapled.
mkdir -p "$work/dmg" "$dist"
ditto "$bundle" "$work/dmg/Covo.app"
ln -s /Applications "$work/dmg/Applications"
dmg="$dist/Covo-$version.dmg"
rm -f "$dmg"
hdiutil create -volname "Covo $version" -srcfolder "$work/dmg" -fs HFS+ -format UDZO "$dmg" >/dev/null
codesign --force --timestamp --sign "$identity" "$dmg"
xcrun notarytool submit "$dmg" --keychain-profile "$profile" --wait
xcrun stapler staple "$dmg"

spctl --assess --type open --context context:primary-signature --verbose=2 "$dmg"
spctl --assess --type execute --verbose=2 "$bundle"
(cd "$dist" && shasum -a 256 "$(basename "$dmg")" >"$(basename "$dmg").sha256")
echo
echo "$dmg"
cat "$dmg.sha256"
