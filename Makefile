BINARY      := emission
PKG         := .
DIST        := dist
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS     := -s -w -X main.version=$(VERSION)

IMAGE       ?= ghcr.io/jdel/emission
IMAGE_TAG   ?= $(VERSION)
PLATFORMS   ?= linux/amd64,linux/arm64

PLATFORMS_BIN := \
	linux/amd64 \
	linux/arm64 \
	darwin/amd64 \
	darwin/arm64 \
	windows/amd64

# Host platform, for picking the matching slice of the dist matrix.
HOST_OS   := $(shell go env GOOS)
HOST_ARCH := $(shell go env GOARCH)
EXE       := $(if $(filter windows,$(HOST_OS)),.exe,)

export CGO_ENABLED := 0

.PHONY: all build ui swagger dist clean test vet docker docker-load buildx-setup sync-clients tauri-deps tauri-bin tauri-icon tauri-run tauri-build help

all: build

# Build the host binary. swagger runs first so embedded API docs always match
# the handler annotations (go build never runs go generate on its own).
build: swagger ui
	go build -trimpath -ldflags '$(LDFLAGS)' -o $(BINARY) $(PKG)

# Build the web UI into internal/web/dist so the Go binary can embed it.
# The output is committed to the repo so `go install` produces a binary with
# the embedded SPA — rerun this target and commit the result whenever the UI
# source changes.
ui:
	cd ui && npm ci && npm run build

swagger:
	go generate ./cmd/...

sync-clients:
	scripts/sync-clients.sh

test:
	go test . ./cmd/... ./internal/...

vet:
	go vet . ./cmd/... ./internal/...

# Cross-compile matrix.
dist: swagger ui $(addprefix $(DIST)/,$(PLATFORMS_BIN))

# Pattern target: dist/<os>/<arch>/<binary>[.exe]
$(DIST)/%:
	@os=$(word 1,$(subst /, ,$*)); arch=$(word 2,$(subst /, ,$*)); \
	out=$(DIST)/$$os/$$arch/$(BINARY); \
	if [ "$$os" = "windows" ]; then out=$$out.exe; fi; \
	echo "build $$os/$$arch -> $$out"; \
	mkdir -p $(DIST)/$$os/$$arch; \
	GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags '$(LDFLAGS)' -o $$out $(PKG)

# Remove all build/generated artifacts, including gitignored ones (node_modules,
# Rust target, generated icons, etc.). Leaves user data alone — config files,
# secrets/keys, and runtime torrent state are not build output.
clean:
	rm -rf $(DIST) $(BINARY) $(BINARY).exe
	rm -rf tauri/target tauri/gen tauri/bin
	find tauri/icons -mindepth 1 -maxdepth 1 ! -name icon.png -exec rm -rf {} +
	rm -rf ui/node_modules ui/.vite
	rm -f ui/*.log *.test *.prof *.out coverage.html
	find . -name .DS_Store -delete

buildx-setup:
	@docker buildx inspect emission-builder >/dev/null 2>&1 || \
		docker buildx create --name emission-builder --driver docker-container --use
	@docker run --privileged --rm tonistiigi/binfmt --install all >/dev/null

docker: buildx-setup
	docker buildx build \
		--builder emission-builder \
		--platform $(PLATFORMS) \
		--build-arg VERSION=$(VERSION) \
		-t $(IMAGE):$(IMAGE_TAG) \
		-t $(IMAGE):latest \
		$(if $(PUSH),--push,) \
		.

docker-load:
	docker buildx build \
		--load \
		--build-arg VERSION=$(VERSION) \
		-t $(IMAGE):$(IMAGE_TAG) \
		.

# --- Tauri desktop shell ---------------------------------------------------

# Install the Tauri CLI (needs a Rust toolchain — rustup.rs).
tauri-deps:
	cargo install tauri-cli --version "^2" --locked

# Stage the host-arch emission binary where the Tauri bundler expects it
# (tauri/bin), reusing the matching slice of the `dist` cross-compile matrix.
# swagger+ui first so the embedded docs/UI match. EXE adds .exe on Windows.
tauri-bin: swagger ui $(DIST)/$(HOST_OS)/$(HOST_ARCH)
	mkdir -p tauri/bin
	cp $(DIST)/$(HOST_OS)/$(HOST_ARCH)/$(BINARY)$(EXE) tauri/bin/$(BINARY)$(EXE)

# Generate the full app icon set (.icns/.ico/pngs) from a square source PNG.
# Defaults to the committed placeholder; override with SRC=path/to/icon.png.
tauri-icon:
	cd tauri && cargo tauri icon $(or $(SRC),icons/icon.png)

# Run the Tauri app in dev. tauri-bin stages the bundled resource (Tauri
# validates bundle.resources at compile time, dev included); build drops
# ./emission at the repo root, which the dev app spawns. On macOS the kernel
# SIGKILLs a loose binary with no valid signature, so ad-hoc sign it first
# (packaged builds are signed by Tauri as part of the bundle).
tauri-run: tauri-bin tauri-icon build
	@if [ "$$(uname)" = "Darwin" ]; then \
		echo "ad-hoc signing ./$(BINARY) for dev spawn"; \
		codesign --force --sign - $(BINARY); \
	fi
	cd tauri && cargo tauri dev

# Package the Tauri app for the host OS (.app/.dmg on macOS). Needs icons.
tauri-build: tauri-bin tauri-icon
	cd tauri && cargo tauri build

help:
	@echo "Targets:"
	@echo "  build         build host binary (./$(BINARY))"
	@echo "  ui            build the web UI into internal/web/dist"
	@echo "  dist          cross-compile to $(DIST)/<os>/<arch>/"
	@echo "  test          go test ./..."
	@echo "  vet           go vet ./..."
	@echo "  docker        buildx multi-arch image ($(PLATFORMS)); add PUSH=1 to push"
	@echo "  docker-load   single-arch image into local Docker"
	@echo "  swagger       regenerate internal/docs from handler godoc annotations"
	@echo "  sync-clients  regenerate internal/client/clients/*.json from upstream"
	@echo "  tauri-deps    install the Tauri CLI (needs Rust)"
	@echo "  tauri-run     run the experimental Tauri shell in dev"
	@echo "  tauri-build   package the Tauri app for host OS (.app/.dmg on macOS)"
	@echo "  clean         remove $(DIST)/ and ./$(BINARY)"
	@echo ""
	@echo "Vars: VERSION=$(VERSION) IMAGE=$(IMAGE) IMAGE_TAG=$(IMAGE_TAG) PLATFORMS=$(PLATFORMS)"
