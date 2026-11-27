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

# Everything is built before anything is installed, so a failed build leaves the old install whole.
# Half an install is worse than none: an app whose Info.plist changed without being signed again has
# a broken signature, and macOS withdraws the folder access it was given, from it and from the panes
# its dashboard runs.
mkdir -p "$PREFIX"
NEW="$PREFIX/.cockpit.new"
trap 'rm -f "$NEW"' EXIT

echo "→ cockpit $VERSION"
go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$NEW" ./cmd/cockpit

if [[ "${NO_MENUBAR:-}" == 1 || "$(uname -s)" != Darwin ]]; then
	mv -f "$NEW" "$PREFIX/cockpit"
	echo "✓ installed $PREFIX/cockpit"
	exit 0
fi

echo "→ Agent Cockpit.app"
swift build -c release --package-path menubar

mv -f "$NEW" "$PREFIX/cockpit" # a rename: the running dashboard and pane server keep their file
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
cp menubar/.build/release/AgentCockpit "$APP/Contents/MacOS/AgentCockpit"
cp menubar/icon/AppIcon.icns "$APP/Contents/Resources/AppIcon.icns"
# SwiftTerm's Metal shaders: without them the dashboard draws on the CPU, most of a core while agents print.
rm -rf "$APP/Contents/Resources/SwiftTerm_SwiftTerm.bundle"
cp -R menubar/.build/release/SwiftTerm_SwiftTerm.bundle "$APP/Contents/Resources/"
# Ad-hoc signature: enough for notifications and for macOS to remember permissions between builds.
codesign --force --sign - "$APP" >/dev/null

cat >"$PLIST" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>$LABEL</string>
  <key>ProgramArguments</key><array><string>/usr/bin/open</string><string>-a</string><string>$APP</string><string>--args</string><string>--background</string></array>
  <key>RunAtLoad</key><true/>
</dict></plist>
EOF

# Stop the running app and wait until it has exited: opened while the old one is still quitting, the
# app would only bring that one forward and the new build would not start. Its dashboard, if open, is
# opened again afterwards.
BIN="$APP/Contents/MacOS/AgentCockpit"
# The running app is found with ps, not pgrep: pgrep leaves out its own ancestors, and the app is one
# whenever this script is started from a pane, so the old app would stay and the new build never start.
app_pids() { ps -axo pid=,command= | awk -v bin="$BIN" '{ pid = $1; $1 = ""; sub(/^ +/, "") } index($0, bin) == 1 { print pid }'; }
has_dashboard() { ps -axo ppid=,command= | awk -v parent="$1" -v bin="$PREFIX/cockpit" '{ ppid = $1; $1 = ""; sub(/^ +/, "") } ppid == parent && index($0, bin) == 1 { found = 1 } END { exit !found }'; }
had_dashboard=0
for pid in $(app_pids); do
  has_dashboard "$pid" && had_dashboard=1
  kill "$pid" 2>/dev/null || true
done
for _ in $(seq 50); do
  [ -z "$(app_pids)" ] && break
  sleep 0.1
done
for pid in $(app_pids); do kill -9 "$pid" 2>/dev/null || true; done
launchctl bootout "gui/$(id -u)/$LABEL" 2>/dev/null || true
launchctl bootstrap "gui/$(id -u)" "$PLIST"
if [ "$had_dashboard" = 1 ]; then
  for _ in $(seq 50); do
    [ -n "$(app_pids)" ] && break
    sleep 0.1
  done
  open -a "$APP"
fi
echo "✓ installed $PREFIX/cockpit and $APP (opens at login)"
