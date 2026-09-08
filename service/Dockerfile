# Native aiwr HTTP image.
#
# Build from the repository root:
#   docker build -f service/Dockerfile -t aiwr .
#
# Run the Go service (the image contains no Python, Torch, Transformers,
# Diffusers, model weights, or third-party research checkout):
#   docker run --rm -p 127.0.0.1:8765:8765 --read-only --tmpfs /tmp aiwr
#
# Optional research adapters remain explicit. This minimal image intentionally
# has no Python/ML runtime; use an operator-managed host environment or a
# third-party sidecar/service rather than expanding this core image.

# syntax=docker/dockerfile:1

FROM golang:1.24-bookworm AS build

ARG VERSION=dev

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY config ./config

RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags="-s -w -X github.com/iruanp/aiwr/internal/core.Version=${VERSION}" \
    -o /out/aiwr ./cmd/aiwr

FROM debian:bookworm-slim

ARG C2PATOOL_VERSION=c2patool-v0.27.15
ARG C2PATOOL_SHA256=7a035b727a6cdda8ad08d98fe94b3c20febee4aea4e7c1de02571b295730faf0
ARG VERSION=dev
ARG TARGETARCH

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        ffmpeg \
        ghostscript \
        libimage-exiftool-perl \
        qpdf \
    && if [ "${TARGETARCH}" = "amd64" ]; then \
         curl -fsSL -o /tmp/c2patool.tar.gz \
           "https://github.com/contentauth/c2pa-rs/releases/download/${C2PATOOL_VERSION}/${C2PATOOL_VERSION}-x86_64-unknown-linux-gnu.tar.gz" \
         && echo "${C2PATOOL_SHA256}  /tmp/c2patool.tar.gz" | sha256sum -c - \
         && tar -xzf /tmp/c2patool.tar.gz -C /tmp \
         && install -m 0755 /tmp/c2patool/c2patool /usr/local/bin/c2patool; \
       else \
         echo "c2patool is not bundled for ${TARGETARCH}; native parsers remain available"; \
       fi \
    && apt-get purge -y --auto-remove curl \
    && rm -rf /var/lib/apt/lists/* /tmp/c2patool /tmp/c2patool.tar.gz \
    && mkdir -p /app /home/aiwr \
    && chown 10001:10001 /app /home/aiwr

COPY --from=build /out/aiwr /usr/local/bin/aiwr
COPY config/clean_strategy.json /etc/aiwr/clean_strategy.json

ENV HOME=/home/aiwr \
    WATERMARKS_SERVER_VERSION=${VERSION} \
    WATERMARKS_CLEAN_STRATEGY_FILE=/etc/aiwr/clean_strategy.json

WORKDIR /app
USER 10001:10001
EXPOSE 8765
ENTRYPOINT ["aiwr"]
CMD ["serve", "--host", "0.0.0.0"]
