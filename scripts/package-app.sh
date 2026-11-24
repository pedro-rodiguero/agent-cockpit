#!/bin/bash
# Builds a self-contained Agent Cockpit.app for a release: the menu bar app and cockpit inside it, both
# universal (Apple silicon and Intel), ad-hoc signed, zipped. Runs on macOS with Go and Xcode.
#   scripts/package-app.sh <version> <out dir>
set -euo pipefail
cd "$(dirname "$0")/.."
VERSION="${1:?version}"; OUT="${2:?out dir}"
LABEL="io.github.pedro-rodiguero.agent-cockpit"
work="$(mktemp -d)"; trap 'rm -rf "$work"' EXIT
APP="$work/Agent Cockpit.app"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources" "$OUT"
bins=()

for arch in arm64 amd64; do
	CGO_ENABLED=0 GOOS=darwin GOARCH=$arch go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$work/cockpit-$arch" ./cmd/cockpit
done
lipo -create -output "$APP/Contents/MacOS/cockpit" "$work/cockpit-arm64" "$work/cockpit-amd64"

# One architecture at a time: building both at once goes through XCBuild, which loses SwiftTerm.
for arch in arm64 x86_64; do
	swift build -c release --arch $arch --package-path menubar --scratch-path "menubar/.build-$arch"
	bins+=("$(swift build -c release --arch $arch --package-path menubar --scratch-path "menubar/.build-$arch" --show-bin-path)/AgentCockpit")
done
lipo -create -output "$APP/Contents/MacOS/AgentCockpit" "${bins[@]}"
cp menubar/icon/AppIcon.icns "$APP/Contents/Resources/AppIcon.icns"
cat >"$APP/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>CFBundleIdentifier</key><string>$LABEL</string>
  <key>CFBundleName</key><string>Agent Cockpit</string>
  <key>CFBundleExecutable</key><string>AgentCockpit</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleIconFile</key><string>AppIcon</string>
  <key>CFBundleShortVersionString</key><string>${VERSION#v}</string>
  <key>CFBundleVersion</key><string>1</string>
  <key>LSMinimumSystemVersion</key><string>13.0</string>
  <key>LSUIElement</key><true/>
  <key>NSAppleEventsUsageDescription</key><string>Open the cockpit dashboard in Terminal.</string>
</dict></plist>
PLIST
codesign --force --sign - "$APP/Contents/MacOS/cockpit"
codesign --force --sign - "$APP"
lipo -archs "$APP/Contents/MacOS/AgentCockpit" "$APP/Contents/MacOS/cockpit"
ditto -c -k --keepParent "$APP" "$OUT/Agent-Cockpit-$VERSION-macos.zip"
echo "$OUT/Agent-Cockpit-$VERSION-macos.zip"
