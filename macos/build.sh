#!/bin/bash
# "Discord Bypass.app" paketini oluşturur: Go çekirdeği + SwiftUI menü çubuğu
# uygulaması, ikisi de Apple Silicon ve Intel için (universal).
# Gereksinim: Go ve Xcode Command Line Tools (xcode-select --install).
set -euo pipefail
cd "$(dirname "$0")/.."

VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
# v1.2.3 etiketinden 1.2.3; etiket yoksa 0.0.0.
APP_VERSION="${APP_VERSION:-$(echo "$VERSION" | sed -nE 's/^v?([0-9]+\.[0-9]+\.[0-9]+).*/\1/p')}"
APP_VERSION="${APP_VERSION:-0.0.0}"
BUILD_NUMBER="$(git rev-list --count HEAD 2>/dev/null || echo 1)"
APP="${APP_DIR:-bin}/Discord Bypass.app"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"

echo "→ Go çekirdeği"
for arch in arm64 amd64; do
  GOOS=darwin GOARCH=$arch CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X main.version=$VERSION" -o "$TMP/core-$arch" .
done
lipo -create -output "$APP/Contents/MacOS/discord-bypass" "$TMP/core-arm64" "$TMP/core-amd64"

echo "→ Menü çubuğu uygulaması"
for arch in arm64 x86_64; do
  swiftc -O -swift-version 5 -parse-as-library -target "$arch-apple-macos13.0" \
    macos/*.swift -o "$TMP/ui-$arch"
done
lipo -create -output "$APP/Contents/MacOS/DiscordBypass" "$TMP/ui-arm64" "$TMP/ui-x86_64"

echo "→ Simge"
swiftc -O macos/tools/make-icon.swift -o "$TMP/make-icon"
"$TMP/make-icon" "$TMP/AppIcon.iconset"
iconutil -c icns "$TMP/AppIcon.iconset" -o "$APP/Contents/Resources/AppIcon.icns"

cat > "$APP/Contents/Info.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleDevelopmentRegion</key><string>tr</string>
  <key>CFBundleExecutable</key><string>DiscordBypass</string>
  <key>CFBundleIconFile</key><string>AppIcon</string>
  <key>CFBundleIdentifier</key><string>com.ziyacivan.discord-bypass</string>
  <key>CFBundleName</key><string>Discord Bypass</string>
  <key>CFBundleDisplayName</key><string>Discord Bypass</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>$APP_VERSION</string>
  <key>CFBundleVersion</key><string>$BUILD_NUMBER</string>
  <key>LSMinimumSystemVersion</key><string>13.0</string>
  <key>LSUIElement</key><true/>
  <key>LSApplicationCategoryType</key><string>public.app-category.utilities</string>
  <key>NSAppleEventsUsageDescription</key><string>Tarayıcı desteğini kurmak için sistem proxy ayarı yönetici yetkisiyle değiştirilir.</string>
</dict>
</plist>
EOF

# İmzasız uygulama Apple Silicon'da çalışmaz; yerel (ad-hoc) imza yeterli.
codesign --force --sign - "$APP/Contents/MacOS/discord-bypass"
codesign --force --sign - "$APP"

echo "hazır: $APP ($VERSION)"
