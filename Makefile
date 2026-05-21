VERSION  ?= 0.1.0
BIN       = bin/claude-monitor
DIST      = dist
LDFLAGS   = -s -w -X 'github.com/achton/claude-monitor-linux/internal/cli.AppVersion=$(VERSION)'

GO       ?= go
RM       := rm -rf
MKDIR    := mkdir -p

# Resolve Go's bin dir so `go install`-ed tools (nfpm, etc.) work even when
# $GOPATH/bin is not on the user's PATH.
GOBIN    := $(shell $(GO) env GOPATH)/bin
NFPM     := $(shell command -v nfpm 2>/dev/null || echo $(GOBIN)/nfpm)
APPIMAGETOOL := $(shell command -v appimagetool 2>/dev/null || echo $(GOBIN)/appimagetool)

# Icon sizes used by hicolor and the .deb nfpm spec.
ICON_SIZES = 16 24 32 48 64 128 256 512

.PHONY: all
all: build

.PHONY: build
build: $(BIN)

$(BIN): $(shell find . -name '*.go' -not -path './bin/*')
	@$(MKDIR) bin
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/claude-monitor

.PHONY: test
test:
	$(GO) test ./...

.PHONY: vet
vet:
	$(GO) vet ./...

.PHONY: lint
lint: vet
	@command -v staticcheck >/dev/null && staticcheck ./... || echo "(staticcheck not installed; skipped)"

.PHONY: headless-test
headless-test: $(BIN)
	@echo "Running CLI under env -i (no DISPLAY/WAYLAND)..."
	@env -i HOME=$$(mktemp -d) PATH=/usr/bin XDG_RUNTIME_DIR=$$(mktemp -d) $(BIN) version
	@env -i HOME=$$(mktemp -d) PATH=/usr/bin XDG_RUNTIME_DIR=$$(mktemp -d) $(BIN) accounts >/dev/null
	@echo "OK: CLI runs with no display."

.PHONY: icons
icons: assets/icon.svg
	@if ! command -v rsvg-convert >/dev/null && ! command -v inkscape >/dev/null; then \
	  echo "Need rsvg-convert (librsvg2-bin) or inkscape to render icons."; exit 1; \
	fi
	@for size in $(ICON_SIZES); do \
	  $(MKDIR) assets/icons/$${size}x$${size}/apps; \
	  if command -v rsvg-convert >/dev/null; then \
	    rsvg-convert -w $$size -h $$size assets/icon.svg -o assets/icons/$${size}x$${size}/apps/claude-monitor.png; \
	  else \
	    inkscape --export-type=png --export-width=$$size --export-height=$$size \
	      --export-filename=assets/icons/$${size}x$${size}/apps/claude-monitor.png assets/icon.svg; \
	  fi; \
	done

.PHONY: manpage
manpage: packaging/claude-monitor.1.gz

packaging/claude-monitor.1.gz: packaging/claude-monitor.1
	gzip -9 -k -f $<

.PHONY: deb
deb: build icons manpage
	@$(MKDIR) $(DIST)
	@test -x "$(NFPM)" || (echo "nfpm not found at $(NFPM)"; \
	  echo "Install:  go install github.com/goreleaser/nfpm/v2/cmd/nfpm@latest"; exit 1)
	VERSION=$(VERSION) $(NFPM) pkg --packager deb --config packaging/deb/nfpm.yaml --target $(DIST)/

.PHONY: appimage
appimage: build icons
	@$(MKDIR) $(DIST)/AppDir/usr/bin $(DIST)/AppDir/usr/share/applications $(DIST)/AppDir/usr/share/icons/hicolor/256x256/apps
	cp $(BIN) $(DIST)/AppDir/usr/bin/
	cp packaging/claude-monitor.desktop $(DIST)/AppDir/usr/share/applications/
	cp packaging/claude-monitor.desktop $(DIST)/AppDir/claude-monitor.desktop
	cp assets/icons/256x256/apps/claude-monitor.png $(DIST)/AppDir/usr/share/icons/hicolor/256x256/apps/
	cp assets/icons/256x256/apps/claude-monitor.png $(DIST)/AppDir/claude-monitor.png
	install -m 0755 packaging/appimage/AppRun $(DIST)/AppDir/AppRun
	@test -x "$(APPIMAGETOOL)" || (echo "appimagetool not found at $(APPIMAGETOOL)"; \
	  echo "Install:  curl -L -o ~/.local/bin/appimagetool \\"; \
	  echo "    https://github.com/AppImage/appimagetool/releases/download/continuous/appimagetool-x86_64.AppImage \\"; \
	  echo "  && chmod +x ~/.local/bin/appimagetool"; exit 1)
	ARCH=x86_64 $(APPIMAGETOOL) $(DIST)/AppDir $(DIST)/claude-monitor-$(VERSION)-x86_64.AppImage

.PHONY: checksums
checksums:
	cd $(DIST) && sha256sum *.deb *.AppImage 2>/dev/null > SHA256SUMS || true

.PHONY: release
release: test deb appimage checksums

.PHONY: install
install: build icons manpage
	install -Dm0755 $(BIN) $(DESTDIR)/usr/bin/claude-monitor
	install -Dm0644 packaging/claude-monitor.desktop $(DESTDIR)/usr/share/applications/claude-monitor.desktop
	install -Dm0644 packaging/claude-monitor.1.gz $(DESTDIR)/usr/share/man/man1/claude-monitor.1.gz
	@for size in $(ICON_SIZES); do \
	  install -Dm0644 assets/icons/$${size}x$${size}/apps/claude-monitor.png \
	    $(DESTDIR)/usr/share/icons/hicolor/$${size}x$${size}/apps/claude-monitor.png; \
	done
	install -Dm0644 assets/icon.svg $(DESTDIR)/usr/share/icons/hicolor/scalable/apps/claude-monitor.svg

.PHONY: clean
clean:
	$(RM) bin/ dist/ assets/icons/ packaging/claude-monitor.1.gz

.PHONY: run
run: build
	./$(BIN)
