#!/usr/bin/env bash
# Builds Kendra.app: a launcher that starts `knd` (native window, own Dock icon) plus the icon.
# usage: bundle.sh <path-to-knd-binary> <output-dir> <version>
set -euo pipefail
BIN=$(cd "$(dirname "$1")" && pwd)/$(basename "$1"); OUT=$2; VER=${3:-0.1.0}
HERE=$(cd "$(dirname "$0")" && pwd); ROOT=$(cd "$HERE/../.." && pwd)
APP="$OUT/Kendra.app"
rm -rf "$APP"; mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources" "$OUT"
cp "$BIN" "$APP/Contents/MacOS/knd"

# icon: draw it, then build the .icns set
TMP=$(mktemp -d); trap 'rm -rf "$TMP"' EXIT
(cd "$ROOT" && go run packaging/icon.go "$TMP/icon.png")
SET="$TMP/Kendra.iconset"; mkdir "$SET"
for s in 16 32 128 256 512; do
  sips -z $s $s "$TMP/icon.png" --out "$SET/icon_${s}x${s}.png" >/dev/null
  sips -z $((s*2)) $((s*2)) "$TMP/icon.png" --out "$SET/icon_${s}x${s}@2x.png" >/dev/null
done
iconutil -c icns "$SET" -o "$APP/Contents/Resources/Kendra.icns"

# launcher: Finder starts apps with a bare PATH, so ask the login shell for its PATH first
# (aws, az, kubelogin, gke-gcloud-auth-plugin live outside the default one).
cat > "$APP/Contents/MacOS/Kendra" <<'SH'
#!/bin/bash
SHELL_PATH="$("${SHELL:-/bin/zsh}" -lic 'printf %s "$PATH"' 2>/dev/null | tail -n1)"
export PATH="${SHELL_PATH:-$PATH}:$HOME/.local/bin:/opt/homebrew/bin:/usr/local/bin:$HOME/go/bin:$HOME/.krew/bin"
mkdir -p "$HOME/.kendra"
exec "$(dirname "$0")/knd" ui >>"$HOME/.kendra/ui.log" 2>&1
SH
chmod +x "$APP/Contents/MacOS/Kendra" "$APP/Contents/MacOS/knd"

cat > "$APP/Contents/Info.plist" <<PL
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleName</key><string>Kendra</string>
<key>CFBundleDisplayName</key><string>Kendra</string>
<key>CFBundleIdentifier</key><string>dev.kendra.app</string>
<key>CFBundleExecutable</key><string>Kendra</string>
<key>CFBundleIconFile</key><string>Kendra</string>
<key>CFBundleShortVersionString</key><string>$VER</string>
<key>CFBundleVersion</key><string>$VER</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>LSMinimumSystemVersion</key><string>12.0</string>
<key>LSUIElement</key><false/>
<key>NSHighResolutionCapable</key><true/>
<key>NSAppTransportSecurity</key><dict><key>NSAllowsLocalNetworking</key><true/></dict>
</dict></plist>
PL
codesign --force --deep --sign - "$APP" >/dev/null 2>&1 || true
xattr -dr com.apple.quarantine "$APP" 2>/dev/null || true
echo "Built $APP"
