# syntax=docker/dockerfile:1.7

# --- Stage 1: build the web UI -----------------------------------------------
FROM --platform=$BUILDPLATFORM node:20-alpine AS ui
WORKDIR /src/ui
COPY ui/package.json ui/package-lock.json ./
RUN npm ci
COPY ui/ ./
# vite's outDir writes the build to /src/internal/web/dist.
RUN npm run build

# --- Stage 2: build the Go binary --------------------------------------------
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ENV CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH

RUN apk add --no-cache ca-certificates

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Bring in the UI build so go:embed can include it.
COPY --from=ui /src/internal/web/dist ./internal/web/dist
RUN go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/emission .

# --- Stage 3: minimal runtime image ------------------------------------------
FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/emission /emission

USER 65534

# Default storage paths inside the container. Mount volumes at these paths
# (or override with EMISSION_STORAGE_* env vars / --storage.* flags).
ENV EMISSION_STORAGE_TORRENTS=/data/torrents \
    EMISSION_STORAGE_AUTH=/data/auth/auth.json

ENTRYPOINT ["/emission"]
# Default to `serve` — override with `docker run <image> seed` etc.
CMD ["serve"]
