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

export CGO_ENABLED := 0

.PHONY: all build ui swagger dist clean test vet docker docker-load buildx-setup sync-clients help

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

clean:
	rm -rf $(DIST) $(BINARY)

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
	@echo "  clean         remove $(DIST)/ and ./$(BINARY)"
	@echo ""
	@echo "Vars: VERSION=$(VERSION) IMAGE=$(IMAGE) IMAGE_TAG=$(IMAGE_TAG) PLATFORMS=$(PLATFORMS)"
