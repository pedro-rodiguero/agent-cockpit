#!/bin/bash
# Removes what scripts/install.sh installed: the cockpit binary, Agent Cockpit.app and its login
# LaunchAgent. Settings and the to-do list stay (see docs/configuration.md), and so does a running
# pane server until its panes end: stop it with `pkill -f "cockpit serve"`, which stops their agents.
#
#   PREFIX and APP_DIR as for install.sh
set -euo pipefail

PREFIX="${PREFIX:-$HOME/.local/bin}"
APP="${APP_DIR:-$HOME/Applications}/Agent Cockpit.app"
LABEL="io.github.pedro-rodiguero.agent-cockpit"
PLIST="$HOME/Library/LaunchAgents/$LABEL.plist"

if [[ "$(uname -s)" == Darwin ]]; then
	launchctl bootout "gui/$(id -u)/$LABEL" 2>/dev/null || true
	rm -f "$PLIST"
	# ps, not pkill, which leaves out its own ancestors: the app is one when this runs from a pane.
	ps -axo pid=,command= | awk -v bin="$APP/Contents/MacOS/AgentCockpit" '{ pid = $1; $1 = ""; sub(/^ +/, "") } index($0, bin) == 1 { print pid }' | xargs kill 2>/dev/null || true
	rm -rf "$APP"
	echo "✓ removed $APP and its login item"
fi
rm -f "$PREFIX/cockpit"
echo "✓ removed $PREFIX/cockpit"
