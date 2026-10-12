# syntax=docker/dockerfile:1.7

# Independent stages (web, Go, Node) build in parallel under BuildKit.
#
# Cache mounts: Railway requires the id form s/<service id>-<path>, and no
# variables in it. Plain docker accepts any id, so local builds still work.

FROM oven/bun:1 AS web-builder
WORKDIR /src/website
COPY website/package.json website/bun.lock* ./
RUN --mount=type=cache,id=s/cc7841e4-c1b2-4f12-b20b-4d45d6e948d7-/root/.bun/install/cache,target=/root/.bun/install/cache \
    bun install
COPY website/ ./
RUN bun run build

# Node LTS for the agent runner's sandboxed commands. The version and checksum
# are pinned together: a new Node is a reviewed change to these two lines.
FROM debian:bookworm-slim AS node
ARG NODE_VERSION=22.23.3
ARG NODE_SHA256=df450af89261115ef9f9e3830c3eeb2cc9213b63c720b1af623cb5dcbe2e02de
RUN apt-get update \
    && apt-get install --yes --no-install-recommends ca-certificates curl xz-utils \
    && curl -fsSL "https://nodejs.org/dist/v${NODE_VERSION}/node-v${NODE_VERSION}-linux-x64.tar.xz" -o /tmp/node.tar.xz \
    && echo "${NODE_SHA256}  /tmp/node.tar.xz" | sha256sum -c - \
    && mkdir -p /usr/local/node \
    && tar -xJf /tmp/node.tar.xz -C /usr/local/node --strip-components=1

FROM golang:1.26-bookworm AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,id=s/cc7841e4-c1b2-4f12-b20b-4d45d6e948d7-/go/pkg/mod,target=/go/pkg/mod \
    go mod download
# Only what the build reads: every go:embed lives under internal/, so a change to
# the docs, the website sources or the scripts does not invalidate this layer.
COPY cmd ./cmd
COPY internal ./internal
COPY --from=web-builder /src/internal/panel/webui/dist ./internal/panel/webui/dist
RUN --mount=type=cache,id=s/cc7841e4-c1b2-4f12-b20b-4d45d6e948d7-/go/pkg/mod,target=/go/pkg/mod \
    --mount=type=cache,id=s/cc7841e4-c1b2-4f12-b20b-4d45d6e948d7-/root/.cache/go-build,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/eggyd ./cmd/eggyd

# The tests are a stage the image does not depend on, so a deploy build skips
# them. Run them with: docker build --target test .   (make docker-test)
FROM builder AS test
COPY . .
COPY --from=web-builder /src/internal/panel/webui/dist ./internal/panel/webui/dist
RUN --mount=type=cache,id=s/cc7841e4-c1b2-4f12-b20b-4d45d6e948d7-/go/pkg/mod,target=/go/pkg/mod \
    --mount=type=cache,id=s/cc7841e4-c1b2-4f12-b20b-4d45d6e948d7-/root/.cache/go-build,target=/root/.cache/go-build \
    CGO_ENABLED=0 go test ./...

FROM debian:bookworm-slim
RUN apt-get update \
    && apt-get install --yes --no-install-recommends ca-certificates curl git openssh-client tini \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*
# Node LTS for the agent runner's sandboxed commands, from the node stage.
COPY --from=node /usr/local/node /usr/local/node
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
