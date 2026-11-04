#!/bin/bash
# SPDX-License-Identifier: GPL-3.0-only
# Copyright (C) 2026 Andrea Casali
#
# Builds build/Covo.app: the SwiftUI app plus the covo and covo-hostshelper
# binaries it runs (Contents/Resources/bin). Ad-hoc signed, for local use;
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

# COVO_VERSION (e.g. 0.1.0-beta.1) is stamped into covo and the app; "dev"
# otherwise. COVO_BUILD is the bundle's build number (default 1).
version="${COVO_VERSION:-dev}"
ldflags="-X github.com/getcovo/covo/internal/version.Version=$version"
(cd "$repo" && go build -trimpath -ldflags "$ldflags" -o "$out/covo" ./cmd/covo &&
	go build -trimpath -ldflags "$ldflags" -o "$out/covo-hostshelper" ./cmd/covo-hostshelper)

if [[ "${1:-}" == "test" ]]; then
	COVO_BIN="$out/covo" swift test --package-path "$app"
	exit
fi

swift build --package-path "$app" -c release --product Covo
bin=$(swift build --package-path "$app" -c release --show-bin-path)

bundle="$out/Covo.app"
rm -rf "$bundle"
mkdir -p "$bundle/Contents/MacOS" "$bundle/Contents/Resources/bin"
cp "$app/Info.plist" "$bundle/Contents/Info.plist"
if [[ "$version" != "dev" ]]; then
	/usr/libexec/PlistBuddy -c "Set :CFBundleShortVersionString $version" "$bundle/Contents/Info.plist"
	/usr/libexec/PlistBuddy -c "Set :CFBundleVersion ${COVO_BUILD:-1}" "$bundle/Contents/Info.plist"
fi
cp "$bin/Covo" "$bundle/Contents/MacOS/Covo"
cp "$out/covo" "$out/covo-hostshelper" "$bundle/Contents/Resources/bin/"
# Icons come from the brand kit (docs/brand).
iconutil -c icns "$app/Resources/AppIcon.iconset" -o "$bundle/Contents/Resources/AppIcon.icns"
cp "$app/Resources/covo-menubar-template.png" "$app/Resources/covo-menubar-template@2x.png" "$bundle/Contents/Resources/"

codesign --force --sign - "$bundle/Contents/Resources/bin/covo" "$bundle/Contents/Resources/bin/covo-hostshelper"
codesign --force --sign - "$bundle"
codesign --verify --strict "$bundle"
echo "$bundle"
