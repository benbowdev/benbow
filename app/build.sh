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
set -euo pipefail

app=$(cd "$(dirname "$0")" && pwd)
repo=$(dirname "$app")
out="$app/build"
mkdir -p "$out"

(cd "$repo" && go build -o "$out/covo" ./cmd/covo && go build -o "$out/covo-hostshelper" ./cmd/covo-hostshelper)

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
cp "$bin/Covo" "$bundle/Contents/MacOS/Covo"
cp "$out/covo" "$out/covo-hostshelper" "$bundle/Contents/Resources/bin/"
# Icons come from the brand kit (docs/brand).
iconutil -c icns "$app/Resources/AppIcon.iconset" -o "$bundle/Contents/Resources/AppIcon.icns"
cp "$app/Resources/covo-menubar-template.png" "$app/Resources/covo-menubar-template@2x.png" "$bundle/Contents/Resources/"

codesign --force --sign - "$bundle/Contents/Resources/bin/covo" "$bundle/Contents/Resources/bin/covo-hostshelper"
codesign --force --sign - "$bundle"
codesign --verify --strict "$bundle"
echo "$bundle"
