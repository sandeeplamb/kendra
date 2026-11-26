#!/usr/bin/env bash
# Builds dist/Kendra-<version>.pkg around a knd binary built for this Mac:
#   packaging/macos/pkg.sh <knd binary> <output dir> <version>
# Env (all optional; without them the package is unsigned and macOS asks for
# right-click -> Open the first time):
#   SIGN_APP        "Developer ID Application: Name (TEAMID)"  signs Kendra.app
#   SIGN_PKG        "Developer ID Installer: Name (TEAMID)"    signs the .pkg
#   NOTARY_PROFILE  notarytool keychain profile: notarizes and staples the .pkg
set -euo pipefail
BIN="$1"; OUT="$2"; VERSION="$3"
cd "$(dirname "$0")/../.."
ROOT="$(mktemp -d)"; SCRIPTS="$(mktemp -d)"; STAGE="$(mktemp -d)"
trap 'rm -rf "$ROOT" "$SCRIPTS" "$STAGE"' EXIT
mkdir -p "$OUT" "$ROOT/Applications"

packaging/macos/bundle.sh "$BIN" "$STAGE" "$VERSION" >/dev/null
cp -R "$STAGE/Kendra.app" "$ROOT/Applications/Kendra.app"
if [ -n "${SIGN_APP:-}" ]; then
  codesign --force --deep --options runtime --timestamp --sign "$SIGN_APP" "$ROOT/Applications/Kendra.app"
fi

# postinstall: put `knd` on the PATH of every user
cat > "$SCRIPTS/postinstall" <<'POST'
#!/bin/bash
mkdir -p /usr/local/bin
ln -sf /Applications/Kendra.app/Contents/MacOS/knd /usr/local/bin/knd
exit 0
POST
chmod +x "$SCRIPTS/postinstall"

PKG="$OUT/Kendra-${VERSION}.pkg"
ARGS=(--root "$ROOT" --scripts "$SCRIPTS" --identifier dev.kendra.app --version "$VERSION" --install-location /)
[ -n "${SIGN_PKG:-}" ] && ARGS+=(--sign "$SIGN_PKG")
pkgbuild "${ARGS[@]}" "$PKG"
if [ -n "${NOTARY_PROFILE:-}" ]; then
  xcrun notarytool submit "$PKG" --keychain-profile "$NOTARY_PROFILE" --wait
  xcrun stapler staple "$PKG"
fi
echo "$PKG"
