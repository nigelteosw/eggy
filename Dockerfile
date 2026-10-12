# syntax=docker/dockerfile:1.7
FROM oven/bun:1 AS web-builder
WORKDIR /src/website
COPY website/package.json website/bun.lock* ./
RUN bun install
COPY website/ ./
RUN bun run build

FROM golang:1.26-bookworm AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web-builder /src/internal/panel/webui/dist ./internal/panel/webui/dist
RUN CGO_ENABLED=0 go test ./... \
    && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/eggyd ./cmd/eggyd

FROM debian:bookworm-slim
# Node LTS for the agent runner's sandboxed commands. The version and checksum
# are pinned together: a new Node is a reviewed change to these two lines.
ARG NODE_VERSION=22.23.3
ARG NODE_SHA256=df450af89261115ef9f9e3830c3eeb2cc9213b63c720b1af623cb5dcbe2e02de
RUN apt-get update \
    && apt-get install --yes --no-install-recommends ca-certificates curl git openssh-client tini xz-utils \
    && curl -fsSL "https://nodejs.org/dist/v${NODE_VERSION}/node-v${NODE_VERSION}-linux-x64.tar.xz" -o /tmp/node.tar.xz \
    && echo "${NODE_SHA256}  /tmp/node.tar.xz" | sha256sum -c - \
    && mkdir -p /usr/local/node \
    && tar -xJf /tmp/node.tar.xz -C /usr/local/node --strip-components=1 \
    && rm /tmp/node.tar.xz \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*
# The unprivileged account agent runner commands execute as. It has no login
# and owns only its run directories under /data/agent-runs.
RUN useradd --system --no-create-home --shell /usr/sbin/nologin coder
COPY --from=builder /usr/local/go /usr/local/go
COPY --from=builder /out/eggyd /usr/local/bin/eggyd
RUN mkdir -p /tmp/runs
ENV EGGY_HOME=/data \
    PATH="/usr/local/go/bin:/usr/local/node/bin:${PATH}"
EXPOSE 8080
ENTRYPOINT ["/usr/bin/tini", "--"]
CMD ["eggyd"]
