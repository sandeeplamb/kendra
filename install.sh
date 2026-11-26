#!/usr/bin/env bash
# Installs Kendra on macOS or Linux. On macOS:
#   /Applications/Kendra.app   double-click, Spotlight, Dock: opens the window
#   knd on your PATH           `knd`, `knd ui`, `knd version`
#
#   ./install.sh              install or update (builds from source, needs Go)
#   ./install.sh --uninstall  remove the app and the knd command (keeps ~/.kendra)
set -euo pipefail
cd "$(dirname "$0")"

APP_NAME="Kendra"
APP_DIR="/Applications/${APP_NAME}.app"
[ -w /Applications ] || APP_DIR="$HOME/Applications/${APP_NAME}.app"
BIN_DIR="/usr/local/bin"
[ -w "$BIN_DIR" ] || BIN_DIR="$HOME/.local/bin"

say() { printf '\033[1;34m==>\033[0m %s\n' "$*"; }

if [ "${1:-}" = "--uninstall" ]; then
  pkill -f "${APP_NAME}.app/Contents/MacOS/knd" 2>/dev/null || true
  rm -rf "/Applications/${APP_NAME}.app" "$HOME/Applications/${APP_NAME}.app"
  for d in /usr/local/bin "$HOME/.local/bin"; do
    # only remove knd if it is the link this script made
    [ -L "$d/knd" ] && [[ "$(readlink "$d/knd")" == *"${APP_NAME}.app"* ]] && rm -f "$d/knd"
  done
  rm -f "$HOME/.local/share/applications/kendra.desktop" "$HOME/.local/share/icons/hicolor/256x256/apps/kendra.png"
  [ "$(uname -s)" = "Linux" ] && rm -f "$HOME/.local/bin/knd"
  say "Removed. Your audit log is still in ~/.kendra (rm -rf ~/.kendra to remove it too)."
  exit 0
fi

VERSION="$(cat internal/version/VERSION 2>/dev/null || echo 0.1.0)"
LDFLAGS="-s -w -X github.com/sandeeplamb/kendra/internal/version.Number=${VERSION}"

if [ "$(uname -s)" = "Linux" ] && { [ -f go.mod ] || [ ! -x ./knd ]; }; then
  command -v go >/dev/null || { echo "Go is required to build from source: https://go.dev/dl/"; exit 1; }
fi
if [ "$(uname -s)" = "Darwin" ]; then
  command -v go >/dev/null || { echo "Go is required to build from source: https://go.dev/dl/ (macOS: brew install go)"; exit 1; }
fi

if [ "$(uname -s)" = "Linux" ]; then
  BIN_DIR="$HOME/.local/bin"; mkdir -p "$BIN_DIR"
  ICON_DIR="$HOME/.local/share/icons/hicolor/256x256/apps"; mkdir -p "$ICON_DIR"
  if [ -x ./knd ] && [ ! -f go.mod ]; then
    # Unpacked release archive: knd is already built.
    say "Installing knd"; cp ./knd "$BIN_DIR/knd"; [ -f ./kendra.png ] && cp ./kendra.png "$ICON_DIR/"
  else
    say "Building knd ${VERSION}"
    go build -trimpath -ldflags "$LDFLAGS" -o "$BIN_DIR/knd" ./cmd/knd
    go run packaging/icon.go "$ICON_DIR/kendra.png"
  fi
  mkdir -p "$HOME/.local/share/applications"
  cat > "$HOME/.local/share/applications/kendra.desktop" <<DESKTOP
[Desktop Entry]
Type=Application
Name=Kendra
Comment=Read-only console for Kubernetes clusters and kapp apps
Exec=$BIN_DIR/knd ui
Icon=kendra
Terminal=false
Categories=Development;
DESKTOP
  case ":$PATH:" in *":$BIN_DIR:"*) ;; *) echo "export PATH=\"$BIN_DIR:\$PATH\"" >> "$HOME/.bashrc"; say "Added $BIN_DIR to PATH in ~/.bashrc";; esac
  say "Done. Window: knd, or Kendra in your app menu"
  exit 0
fi

BUILD="$(mktemp -d)"; trap 'rm -rf "$BUILD"' EXIT
pkill -f "${APP_NAME}.app/Contents/MacOS/knd" 2>/dev/null || true
say "Building knd ${VERSION} (with the native window)"
MACOSX_DEPLOYMENT_TARGET=12.0 CGO_CFLAGS="-mmacosx-version-min=12.0" CGO_LDFLAGS="-mmacosx-version-min=12.0" \
CGO_ENABLED=1 go build -trimpath -ldflags "$LDFLAGS" -o "$BUILD/knd" ./cmd/knd 2> "$BUILD/build.log" \
  || { cat "$BUILD/build.log"; echo "Build failed. The native window needs the Xcode command line tools: xcode-select --install"; exit 1; }
say "Installing ${APP_DIR}"
packaging/macos/bundle.sh "$BUILD/knd" "$BUILD" "$VERSION" >/dev/null
rm -rf "$APP_DIR"
cp -R "$BUILD/${APP_NAME}.app" "$APP_DIR"
xattr -dr com.apple.quarantine "$APP_DIR" 2>/dev/null || true

say "Linking knd into ${BIN_DIR}"
mkdir -p "$BIN_DIR"
ln -sf "$APP_DIR/Contents/MacOS/knd" "$BIN_DIR/knd"
# a copy from `make install` in ~/go/bin would confuse `which knd`
[ -f "$HOME/go/bin/knd" ] && [ ! -L "$HOME/go/bin/knd" ] && rm -f "$HOME/go/bin/knd"

case ":$PATH:" in *":$BIN_DIR:"*) ;; *)
  echo "export PATH=\"$BIN_DIR:\$PATH\"" >> "$HOME/.zshrc"
  say "Added $BIN_DIR to PATH in ~/.zshrc (open a new terminal)";;
esac

say "Done."
cat <<MSG

  Open the app      Spotlight: "Kendra"  ·  or: open -a Kendra
  Window from CLI   knd
  Version           knd version
  Uninstall         ./install.sh --uninstall

MSG
