#!/bin/bash
# SPDX-License-Identifier: GPL-3.0-only
# Copyright (C) 2026 Andrea Casali
#
# Builds a signed, notarized DMG of Benbow for testers:
#
#   app/release.sh 0.1.0-beta.1
#
# Needs, once on this Mac (see docs/release.md):
#   - a "Developer ID Application" certificate in the login keychain
#     (BENBOW_SIGN_IDENTITY picks one when there are several);
#   - notarytool credentials stored as the keychain profile "benbow-notary"
#     (BENBOW_NOTARY_PROFILE for another name).
#   - the Sparkle signing key in the login keychain (account "benbow",
#     BENBOW_SPARKLE_ACCOUNT for another name).
# Output: dist/Benbow-<version>.dmg, its .sha256 and dist/appcast.xml.
set -euo pipefail

version="${1:-}"
if [[ ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$ ]]; then
	echo "usage: app/release.sh <version>   e.g. 0.1.0-beta.1" >&2
	exit 2
fi
app=$(cd "$(dirname "$0")" && pwd)
repo=$(dirname "$app")
dist="$repo/dist"
profile="${BENBOW_NOTARY_PROFILE:-benbow-notary}"
sparkle_account="${BENBOW_SPARKLE_ACCOUNT:-benbow}"

if [[ -n "$(git -C "$repo" status --porcelain)" ]]; then
	echo "The working tree has uncommitted changes: commit them first, so the build matches a commit." >&2
	exit 1
fi

identity="${BENBOW_SIGN_IDENTITY:-}"
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

BENBOW_VERSION="$version" BENBOW_BUILD="$(git -C "$repo" rev-list --count HEAD)" "$app/build.sh" >/dev/null
bundle="$app/build/Benbow.app"

# Inner executables first, then the app; hardened runtime and a secure
# timestamp are what notarization requires.
sign() { codesign --force --options runtime --timestamp --sign "$identity" "$@"; }
sparkle="$bundle/Contents/Frameworks/Sparkle.framework"
sign "$sparkle/Versions/B/Autoupdate" "$sparkle/Versions/B/Updater.app"
sign "$sparkle"
sign "$bundle/Contents/Resources/bin/benbow" "$bundle/Contents/Resources/bin/benbow-hostshelper"
sign "$bundle"
codesign --verify --deep --strict --verbose=2 "$bundle"

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

# 1. Notarize the app and staple its ticket, so it opens offline too.
ditto -c -k --keepParent "$bundle" "$work/Benbow.zip"
xcrun notarytool submit "$work/Benbow.zip" --keychain-profile "$profile" --wait
xcrun stapler staple "$bundle"

# 2. The DMG: the app and a link to /Applications; signed, notarized, stapled.
mkdir -p "$work/dmg" "$dist"
ditto "$bundle" "$work/dmg/Benbow.app"
ln -s /Applications "$work/dmg/Applications"
dmg="$dist/Benbow-$version.dmg"
rm -f "$dmg"
hdiutil create -volname "Benbow $version" -srcfolder "$work/dmg" -fs HFS+ -format UDZO "$dmg" >/dev/null
codesign --force --timestamp --sign "$identity" "$dmg"
xcrun notarytool submit "$dmg" --keychain-profile "$profile" --wait
xcrun stapler staple "$dmg"

spctl --assess --type open --context context:primary-signature --verbose=2 "$dmg"
spctl --assess --type execute --verbose=2 "$bundle"
(cd "$dist" && shasum -a 256 "$(basename "$dmg")" >"$(basename "$dmg").sha256")

# 3. The appcast for Sparkle: the DMG signed with the EdDSA key in the login
# keychain (account "benbow", made by Sparkle's generate_keys). It points at
# the GitHub release asset; publish it at $feed (docs/release.md).
tools="$app/.build/artifacts/sparkle/Sparkle/bin"
edsig=$("$tools/sign_update" --account "$sparkle_account" "$dmg")
build=$(/usr/libexec/PlistBuddy -c "Print :CFBundleVersion" "$bundle/Contents/Info.plist")
feed=$(/usr/libexec/PlistBuddy -c "Print :SUFeedURL" "$bundle/Contents/Info.plist")
# Release notes shown in Sparkle's window: dist/notes-<version>.md (basic
# Markdown, also used for the GitHub release). A link to the release page
# would show all of GitHub's page inside that window.
notes="$dist/notes-$version.md"
description=""
if [[ -f "$notes" ]]; then
	description="<description sparkle:format=\"markdown\"><![CDATA[$(cat "$notes")]]></description>"
else
	echo "warning: no $notes, the update window will show no release notes" >&2
fi
cat >"$dist/appcast.xml" <<XML
<?xml version="1.0" encoding="utf-8"?>
<rss version="2.0" xmlns:sparkle="http://www.andymatuschak.org/xml-namespaces/sparkle">
  <channel>
    <title>Benbow</title>
    <link>$feed</link>
    <item>
      <title>Benbow $version</title>
      <pubDate>$(LC_ALL=C date -u "+%a, %d %b %Y %H:%M:%S +0000")</pubDate>
      <sparkle:version>$build</sparkle:version>
      <sparkle:shortVersionString>$version</sparkle:shortVersionString>
      <sparkle:minimumSystemVersion>14.0</sparkle:minimumSystemVersion>
      $description
      <sparkle:fullReleaseNotesLink>https://github.com/benbowdev/benbow/releases</sparkle:fullReleaseNotesLink>
      <enclosure url="https://github.com/benbowdev/benbow/releases/download/v$version/$(basename "$dmg")" type="application/octet-stream" $edsig/>
    </item>
  </channel>
</rss>
XML
echo
echo "$dmg"
cat "$dmg.sha256"
echo "$dist/appcast.xml"
