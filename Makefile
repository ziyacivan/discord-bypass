VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
# 1.2.3 biçimindeki sürüm (etiket yoksa 0.0.0): exe özelliklerinde ve Info.plist'te görünür.
VERNUM  := $(or $(shell echo "$(VERSION)" | sed -nE 's/^v?([0-9]+\.[0-9]+\.[0-9]+).*/\1/p'),0.0.0)
LDFLAGS := -s -w -X main.version=$(VERSION)
BUILD   := CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)"
# Windows tepsi uygulaması: konsol penceresi açmaz, argümansız "tray" çalışır.
BUILDGUI := CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS) -H=windowsgui -X main.defaultCmd=tray"
DIST    := dist

.PHONY: all mac windows winassets app install-app dist test clean

all: mac windows

mac:
	GOOS=darwin GOARCH=arm64 $(BUILD) -o bin/discord-bypass-macos-arm64 .
	GOOS=darwin GOARCH=amd64 $(BUILD) -o bin/discord-bypass-macos-amd64 .

windows:
	GOOS=windows GOARCH=amd64 $(BUILDGUI) -o bin/windows-x64/DiscordBypass.exe .
	GOOS=windows GOARCH=amd64 $(BUILD) -o bin/windows-x64/discord-bypass.exe .
	GOOS=windows GOARCH=arm64 $(BUILDGUI) -o bin/windows-arm64/DiscordBypass.exe .
	GOOS=windows GOARCH=arm64 $(BUILD) -o bin/windows-arm64/discord-bypass.exe .

# Windows simgelerini ve .exe kaynaklarını (assets/, rsrc_windows_*.syso) yeniden üretir.
winassets:
	cd tools/winassets && go run . -out ../.. -version $(VERNUM)

# macOS menü çubuğu uygulaması: bin/Discord Bypass.app (Xcode Command Line Tools gerekir)
app:
	VERSION=$(VERSION) ./macos/build.sh

install-app: app
	rm -rf "/Applications/Discord Bypass.app"
	ditto "bin/Discord Bypass.app" "/Applications/Discord Bypass.app"

# Release dosyalarını dist/ altına hazırlar (yalnızca macOS'ta; bin/ içindeki
# uygulamaya dokunmaz). GitHub'da v* etiketi atılınca CI bunu çalıştırır.
dist: windows
	rm -rf $(DIST)
	mkdir -p $(DIST)/build
	APP_DIR=$(DIST)/build VERSION=$(VERSION) ./macos/build.sh
	ditto -c -k --keepParent "$(DIST)/build/Discord Bypass.app" $(DIST)/DiscordBypass-macOS.zip
	rm -rf $(DIST)/build
	cp bin/windows-x64/DiscordBypass.exe     $(DIST)/DiscordBypass.exe
	cp bin/windows-arm64/DiscordBypass.exe   $(DIST)/DiscordBypass-arm64.exe
	cp bin/windows-x64/discord-bypass.exe    $(DIST)/discord-bypass.exe
	cp bin/windows-arm64/discord-bypass.exe  $(DIST)/discord-bypass-arm64.exe
	cd $(DIST) && shasum -a 256 * > SHA256SUMS.txt

test:
	go vet ./...
	GOOS=windows go vet ./...
	go test -race ./...

clean:
	rm -rf bin $(DIST)
