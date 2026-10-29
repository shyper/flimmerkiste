# The frontend and the Go binary are built on the build machine itself, also for
# other architectures (Go cross-compiles), so a multi-arch build needs no emulation there.

# Base images are pinned by digest (Dependabot proposes updates), so a rebuild
# gets exactly the same system until the pin is moved on purpose.

# ---- frontend ----
FROM --platform=$BUILDPLATFORM node:22-alpine@sha256:fe4f6d74e20370acb04ad889077d14d0f64a9c8ea6a42bc60cddf1e844699914 AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

# ---- backend ----
FROM --platform=$BUILDPLATFORM golang:1.27-alpine@sha256:b8c9b82affbbb82a020bbcf998dad2ba9516944ac7e883524642a481a9505e9c AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
ARG VERSION=dev
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/flimmerkiste ./cmd/flimmerkiste

# ---- runtime ----
# One image for every kind of hardware: jellyfin-ffmpeg brings the VAAPI drivers
# for Intel and AMD along and supports NVIDIA (NVENC, with the NVIDIA Container
# Toolkit on the host) and the encoder of the Raspberry Pi 4.
FROM debian:trixie-slim@sha256:62bd423aeee912639ca8ee681c2b762cb3e25137e0a92b20be455898a9f034ad
ARG VERSION=dev
ARG DEBIAN_FRONTEND=noninteractive
LABEL org.opencontainers.image.title="Flimmerkiste" \
      org.opencontainers.image.description="Your own TV channels from your Plex, Jellyfin or Emby library" \
      org.opencontainers.image.version="${VERSION}"
COPY build/jellyfin-ffmpeg.sh /tmp/
# A font is needed to burn in text subtitles (libass); curl for the health check,
# CA certificates for HTTPS (plex.tv, TMDb, AI providers).
RUN apt-get update \
 && apt-get install -y --no-install-recommends tzdata fontconfig fonts-dejavu-core curl ca-certificates \
 && sh /tmp/jellyfin-ffmpeg.sh \
 && apt-get purge -y gnupg && apt-get autoremove -y && rm -rf /var/lib/apt/lists/* /tmp/jellyfin-ffmpeg.sh \
 && useradd -u 1000 -M -s /usr/sbin/nologin flimmerkiste \
 && mkdir -p /data && chown flimmerkiste /data
COPY --from=build /out/flimmerkiste /usr/local/bin/flimmerkiste
USER flimmerkiste
# NVIDIA: the Container Toolkit passes the encoder through with "video" (gpus: all in compose)
ENV FLIMMERKISTE_DATA_DIR=/data \
    FLIMMERKISTE_LISTEN=:8089 \
    FLIMMERKISTE_CONTAINER=1 \
    TZ=Europe/Berlin \
    HOME=/tmp \
    NVIDIA_DRIVER_CAPABILITIES=compute,video,utility
VOLUME /data
EXPOSE 8089
# the first start may migrate the database for a while
HEALTHCHECK --interval=60s --timeout=5s --start-period=120s --retries=3 CMD curl -fsS -o /dev/null http://127.0.0.1:8089/api/auth/status || exit 1
ENTRYPOINT ["/usr/local/bin/flimmerkiste"]
