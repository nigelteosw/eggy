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
# The palette is written once, in internal/themes; Tailwind reads it from there.
COPY internal/themes/themes.json /src/internal/themes/themes.json
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

# herdr-core (hc) for the terminal extension. Version and checksum are pinned
# together, as Node's are, and come from releases.herdr.dev/core/<version>/
# manifest.json. It is installed at /usr/bin/hc, on the sandbox's PATH, and runs
# only when terminal.enabled is on.
FROM debian:bookworm-slim AS herdr-core
ARG HC_VERSION=0.1.21
ARG HC_SHA256=78424755c10a40dde38a0bdb55c20ac19706e9ae05ed68fc2acf1fcff0d941c1
RUN apt-get update \
    && apt-get install --yes --no-install-recommends ca-certificates curl \
    && curl -fsSL "https://releases.herdr.dev/core/${HC_VERSION}/herdr-core-linux-x86_64.tar.gz" -o /tmp/hc.tar.gz \
    && echo "${HC_SHA256}  /tmp/hc.tar.gz" | sha256sum -c - \
    && mkdir -p /out \
    && tar -xzf /tmp/hc.tar.gz -C /out

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
    && apt-get install --yes --no-install-recommends ca-certificates curl git openssh-client tini zsh \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*
# Every login shell (bash through /etc/profile, zsh through its zprofile)
# gets the image's tools and the account's ~/.local/bin back on PATH, and bash
# reads ~/.bashrc, so what an account installs into its home keeps working
# across redeploys: the home is on the volume, the image is not.
COPY deploy/eggy-profile.sh /etc/profile.d/eggy.sh
RUN echo 'emulate sh -c ". /etc/profile.d/eggy.sh"' >> /etc/zsh/zprofile
# Node LTS for the agent runner's sandboxed commands, from the node stage.
COPY --from=node /usr/local/node /usr/local/node
# herdr-core for the terminal extension, with its licence and notices.
COPY --from=herdr-core /out/herdr-core /usr/bin/hc
COPY --from=herdr-core /out/LICENSE /out/THIRD-PARTY-NOTICES /usr/share/doc/herdr-core/
# The unprivileged account agent runner commands execute as. It has no login
# and owns only its run directories under /data/agent-runs.
# A fixed, uncommon uid: RLIMIT_NPROC counts every process of a uid on the
# host, not per container, and on a shared host the default system uid (999)
# already had more than the sandbox's 512, so nothing sandboxed could fork.
RUN useradd --system --uid 199999 --no-create-home --shell /usr/sbin/nologin coder
COPY --from=builder /usr/local/go /usr/local/go
COPY --from=builder /out/eggyd /usr/local/bin/eggyd
RUN mkdir -p /tmp/runs
ENV EGGY_HOME=/data \
    PATH="/usr/local/go/bin:/usr/local/node/bin:${PATH}"
EXPOSE 8080
ENTRYPOINT ["/usr/bin/tini", "--"]
CMD ["eggyd"]
