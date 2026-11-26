VERSION := $(shell cat internal/version/VERSION)
LDFLAGS := -s -w -X github.com/sandeeplamb/kendra/internal/version.Number=$(VERSION)
BIN     := knd
GOBIN   ?= $(shell go env GOPATH)/bin

.PHONY: build test test-go test-js run install app pkg uninstall icon release clean

# On macOS this builds the native window (cgo, needs the Xcode command line tools).
build:
	CGO_ENABLED=1 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/knd

test: test-go test-js

test-go:
	go vet ./...
	go test ./...

test-js:
	node --check internal/web/static/app.js
	node --check internal/web/static/core.js
	node --check internal/web/static/views.js
	node --check internal/web/static/views2.js
	node --check internal/web/static/views3.js
	node --check internal/web/static/views4.js
	node --check internal/web/static/graph.js
	node internal/web/jstest/filter_test.js

run: build
	./$(BIN)

install: build
	mkdir -p $(GOBIN)
	cp $(BIN) $(GOBIN)/$(BIN)
	@echo "installed $(GOBIN)/$(BIN)  (make sure $(GOBIN) is on your PATH)"

# macOS: /Applications/Kendra.app plus `knd` on your PATH. See install.sh.
app:
	./install.sh

# macOS installer package for distribution: dist/Kendra-<version>.pkg installs
# /Applications/Kendra.app and links /usr/local/bin/knd. It holds a universal binary
# (Apple silicon + Intel), built with cgo for each CPU and joined with lipo. Set SIGN_APP /
# SIGN_PKG to "Developer ID Application: ..." / "Developer ID Installer: ..." to sign it,
# and NOTARY_PROFILE to a `xcrun notarytool store-credentials` profile to notarize it.
MACENV = MACOSX_DEPLOYMENT_TARGET=12.0 CGO_CFLAGS="-mmacosx-version-min=12.0" CGO_LDFLAGS="-mmacosx-version-min=12.0" CGO_ENABLED=1
pkg:
	@mkdir -p dist
	$(MACENV) GOARCH=arm64 go build -buildvcs=false -trimpath -ldflags "$(LDFLAGS)" -o dist/knd-arm64 ./cmd/knd
	$(MACENV) GOARCH=amd64 go build -buildvcs=false -trimpath -ldflags "$(LDFLAGS)" -o dist/knd-amd64 ./cmd/knd
	lipo -create -output dist/knd-universal dist/knd-arm64 dist/knd-amd64
	packaging/macos/pkg.sh dist/knd-universal dist $(VERSION)
	@rm -f dist/knd-arm64 dist/knd-amd64 dist/knd-universal
	@lipo -info "$$(pkgutil --expand-full dist/Kendra-$(VERSION).pkg dist/.expand >/dev/null 2>&1; find dist/.expand -path '*MacOS/knd' | head -1)" ; rm -rf dist/.expand

uninstall:
	rm -f $(GOBIN)/$(BIN)
	rm -rf "$(HOME)/Applications/Kendra.app" /Applications/Kendra.app

# redraw the icon (docs/images/icon.png and the web logo)
icon:
	go run packaging/icon.go docs/images/icon.png internal/web/static/logo.svg

release:
	@mkdir -p dist
	@for p in darwin/arm64 darwin/amd64 linux/amd64 linux/arm64 windows/amd64; do \
	  os=$${p%/*}; arch=$${p#*/}; ext=; [ $$os = windows ] && ext=.exe; \
	  echo "  dist/knd_$${os}_$${arch}$$ext"; \
	  GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/knd_$${os}_$${arch}$$ext ./cmd/knd || exit 1; \
	done

clean:
	rm -rf $(BIN) dist
