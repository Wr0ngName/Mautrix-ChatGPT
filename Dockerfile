# mautrix-chatgpt: Matrix bridge for ChatGPT
#
# Simple API mode only (no sidecar needed for OpenAI)

# ============== Stage 1: Build Go binary ==============
FROM golang:1.24-bookworm AS builder

RUN apt-get update && apt-get install -y --no-install-recommends \
    git ca-certificates build-essential libsqlite3-dev \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG COMMIT_HASH
ARG BUILD_TIME
ARG VERSION=0.1.0

RUN CGO_ENABLED=1 go build -tags "goolm" -o /usr/bin/mautrix-chatgpt \
    -ldflags "-s -w \
        -X main.Tag=${VERSION} \
        -X main.Commit=${COMMIT_HASH:-$(git rev-parse HEAD 2>/dev/null || echo unknown)} \
        -X 'main.BuildTime=${BUILD_TIME:-$(date -Iseconds)}'" \
    ./cmd/mautrix-chatgpt

# ============== Stage 2: Final image ==============
FROM debian:bookworm-slim

ENV UID=1337 \
    GID=1337

# Install minimal runtime dependencies
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    bash \
    curl \
    gosu \
    libsqlite3-0 \
    && rm -rf /var/lib/apt/lists/*

# Install yq for YAML processing
RUN curl -sL https://github.com/mikefarah/yq/releases/latest/download/yq_linux_amd64 \
    -o /usr/bin/yq && chmod +x /usr/bin/yq

# Create bridge user
RUN useradd -m -u 1337 bridge && \
    mkdir -p /data && \
    chown -R bridge:bridge /data

WORKDIR /app

# Copy Go binary
COPY --from=builder /usr/bin/mautrix-chatgpt /usr/bin/mautrix-chatgpt

# Copy startup script
COPY docker-run.sh /docker-run.sh
RUN chmod +x /docker-run.sh

# Volume for data
VOLUME /data
WORKDIR /data

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=30s --retries=3 \
    CMD curl -sf http://localhost:29350/_matrix/mau/ready || exit 1

CMD ["/docker-run.sh"]
