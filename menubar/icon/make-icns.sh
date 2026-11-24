#!/bin/sh
# Regenerates AppIcon.icns from AppIcon.svg. Needs Google Chrome (to rasterize the SVG) and the macOS
# command line tools (sips, iconutil). Run it after changing the SVG and commit both files.
set -eu
cd "$(dirname "$0")"
chrome="/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
"$chrome" --headless=new --disable-gpu --hide-scrollbars --force-device-scale-factor=1 \
  --default-background-color=00000000 --window-size=1024,1024 \
  --screenshot="$work/1024.png" "file://$PWD/AppIcon.svg" >/dev/null 2>&1
set=$work/AppIcon.iconset
mkdir "$set"
for s in 16 32 128 256 512; do
  sips -z $s $s "$work/1024.png" --out "$set/icon_${s}x${s}.png" >/dev/null
  d=$((s * 2))
  sips -z $d $d "$work/1024.png" --out "$set/icon_${s}x${s}@2x.png" >/dev/null
done
iconutil -c icns "$set" -o AppIcon.icns
echo "wrote AppIcon.icns"
