#!/bin/bash
# SPDX-License-Identifier: GPL-3.0-only
# Copyright (C) 2026 Andrea Casali
#
# Builds build/Benbow Dev.app (build/Benbow.app with BENBOW_VERSION, for release.sh):
# the SwiftUI app plus the benbow and benbow-hostshelper
# binaries it runs (Contents/Resources/bin) and Sparkle. Ad-hoc signed, for local use;
# signing and notarization come with the public beta.
#
#   app/build.sh          build the app
#   app/build.sh test     run the Swift tests, including a live daemon test
#   app/release.sh        signed, notarized DMG for testers (uses this script)
set -euo pipefail

app=$(cd "$(dirname "$0")" && pwd)
repo=$(dirname "$app")
out="$app/build"
mkdir -p "$out"

# BENBOW_VERSION (e.g. 0.1.0-beta.1) is stamped into benbow and the app; "dev"
# otherwise. BENBOW_BUILD is the bundle's build number (default 1).
version="${BENBOW_VERSION:-dev}"
ldflags="-X github.com/benbowdev/benbow/internal/version.Version=$version"
(cd "$repo" && go build -trimpath -ldflags "$ldflags" -o "$out/benbow" ./cmd/benbow &&
	go build -trimpath -ldflags "$ldflags" -o "$out/benbow-hostshelper" ./cmd/benbow-hostshelper)

if [[ "${1:-}" == "test" ]]; then
	BENBOW_BIN="$out/benbow" swift test --package-path "$app"
	exit
fi

swift build --package-path "$app" -c release --product Benbow
bin=$(swift build --package-path "$app" -c release --show-bin-path)

# Development builds are "Benbow Dev" with their own bundle ID, so Spotlight,
# Raycast and Launch Services never mix them up with the installed Benbow.
# Both use the same Benbow home and daemon: opening one restarts the daemon
# from its own binaries.
if [[ "$version" == "dev" ]]; then
	bundle="$out/Benbow Dev.app"
else
	bundle="$out/Benbow.app"
fi
rm -rf "$bundle"
mkdir -p "$bundle/Contents/MacOS" "$bundle/Contents/Resources/bin"
cp "$app/Info.plist" "$bundle/Contents/Info.plist"
if [[ "$version" != "dev" ]]; then
	/usr/libexec/PlistBuddy -c "Set :CFBundleShortVersionString $version" "$bundle/Contents/Info.plist"
	/usr/libexec/PlistBuddy -c "Set :CFBundleVersion ${BENBOW_BUILD:-1}" "$bundle/Contents/Info.plist"
	# Only versioned builds look for app updates; dev builds never do.
	/usr/libexec/PlistBuddy -c "Add :SUFeedURL string ${BENBOW_FEED_URL:-https://benbow.dev/appcast.xml}" "$bundle/Contents/Info.plist"
else
	/usr/libexec/PlistBuddy -c "Set :CFBundleIdentifier dev.benbow.app.dev" "$bundle/Contents/Info.plist"
	/usr/libexec/PlistBuddy -c "Set :CFBundleName Benbow Dev" "$bundle/Contents/Info.plist"
	/usr/libexec/PlistBuddy -c "Set :CFBundleDisplayName Benbow Dev" "$bundle/Contents/Info.plist"
fi
cp "$bin/Benbow" "$bundle/Contents/MacOS/Benbow"
# Sparkle (app updates). Benbow is not sandboxed, so Sparkle's XPC services
# are not needed: dropped, then the framework is signed again below.
mkdir -p "$bundle/Contents/Frameworks"
ditto "$bin/Sparkle.framework" "$bundle/Contents/Frameworks/Sparkle.framework"
rm -rf "$bundle/Contents/Frameworks/Sparkle.framework/Versions/B/XPCServices" "$bundle/Contents/Frameworks/Sparkle.framework/XPCServices"
cp "$out/benbow" "$out/benbow-hostshelper" "$bundle/Contents/Resources/bin/"
# Icons come from the brand kit (docs/brand). Benbow.icon (Icon Composer) compiles
# with Xcode's actool into Assets.car, the shape macOS 26 expects (otherwise it
# puts the icon on a grey plate), plus Benbow.icns for macOS 14 and 15. Without
# Xcode (only the Command Line Tools) the iconset is used: fine before macOS 26.
rm -f "$bundle/Contents/Resources/AppIcon.icns"
if xcrun --find actool >/dev/null 2>&1; then
	xcrun actool "$app/Resources/Benbow.icon" --compile "$bundle/Contents/Resources" \
		--platform macosx --minimum-deployment-target 14.0 --app-icon Benbow \
		--output-partial-info-plist "$out/icon-partial.plist" >/dev/null
else
	echo "actool not found (Xcode is not installed): using the iconset, macOS 26 will show the icon on a plate" >&2
	iconutil -c icns "$app/Resources/AppIcon.iconset" -o "$bundle/Contents/Resources/Benbow.icns"
fi
cp "$app/Resources/benbow-menubar-template.png" "$app/Resources/benbow-menubar-template@2x.png" "$bundle/Contents/Resources/"

sparkle="$bundle/Contents/Frameworks/Sparkle.framework"
codesign --force --sign - "$sparkle/Versions/B/Autoupdate" "$sparkle/Versions/B/Updater.app" "$sparkle"
codesign --force --sign - "$bundle/Contents/Resources/bin/benbow" "$bundle/Contents/Resources/bin/benbow-hostshelper"
codesign --force --sign - "$bundle"
codesign --verify --strict "$bundle"
echo "$bundle"
