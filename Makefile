VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
BUILD   := CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)"
# Windows tepsi uygulaması: konsol penceresi açmaz, argümansız "tray" çalışır.
BUILDGUI := CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS) -H=windowsgui -X main.defaultCmd=tray"

.PHONY: all mac windows winassets app install-app test clean

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
	cd tools/winassets && go run . -out ../..

# macOS menü çubuğu uygulaması: bin/Discord Bypass.app (Xcode Command Line Tools gerekir)
app:
	./macos/build.sh

install-app: app
	rm -rf "/Applications/Discord Bypass.app"
	ditto "bin/Discord Bypass.app" "/Applications/Discord Bypass.app"

test:
	go vet ./...
	go test -race ./...

clean:
	rm -rf bin
