#!/bin/bash
# Builds cockpit and the menu bar app, installs them for the current user, and registers a LaunchAgent
# that opens the menu bar app at login.
#
#   PREFIX     where the cockpit binary goes (default ~/.local/bin)
#   APP_DIR    where Agent Cockpit.app goes (default ~/Applications)
#   NO_MENUBAR=1 skips the menu bar app and the LaunchAgent (always skipped outside macOS)
set -euo pipefail
cd "$(dirname "$0")/.."

PREFIX="${PREFIX:-$HOME/.local/bin}"
APP="${APP_DIR:-$HOME/Applications}/Agent Cockpit.app"
LABEL="io.github.pedro-rodiguero.agent-cockpit"
PLIST="$HOME/Library/LaunchAgents/$LABEL.plist"
VERSION="$(git describe --tags --always --dirty 2>/dev/null || echo dev)"

echo "→ cockpit $VERSION"
mkdir -p "$PREFIX"
go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$PREFIX/cockpit" ./cmd/cockpit

if [[ "${NO_MENUBAR:-}" == 1 || "$(uname -s)" != Darwin ]]; then
	echo "✓ installed $PREFIX/cockpit"
	exit 0
fi

echo "→ Agent Cockpit.app"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
cat >"$APP/Contents/Info.plist" <<EOF
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
  <key>CockpitBinary</key><string>$PREFIX/cockpit</string>
  <key>NSAppleEventsUsageDescription</key><string>Open the cockpit dashboard in Terminal.</string>
</dict></plist>
EOF
swift build -c release --package-path menubar
cp menubar/.build/release/AgentCockpit "$APP/Contents/MacOS/AgentCockpit"
cp menubar/icon/AppIcon.icns "$APP/Contents/Resources/AppIcon.icns"
# Ad-hoc signature: enough for notifications and for macOS to remember permissions between builds.
codesign --force --sign - "$APP" >/dev/null

cat >"$PLIST" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>$LABEL</string>
  <key>ProgramArguments</key><array><string>/usr/bin/open</string><string>-a</string><string>$APP</string></array>
  <key>RunAtLoad</key><true/>
</dict></plist>
EOF

pkill -x AgentCockpit 2>/dev/null || true
launchctl bootout "gui/$(id -u)/$LABEL" 2>/dev/null || true
launchctl bootstrap "gui/$(id -u)" "$PLIST"
echo "✓ installed $PREFIX/cockpit and $APP (opens at login)"
