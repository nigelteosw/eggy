# syntax=docker/dockerfile:1.7
FROM golang:1.26-bookworm AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go test ./... \
    && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/eggyd ./cmd/eggyd \
    && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/eggy ./cmd/eggy

FROM node:24-bookworm-slim
ARG CODEX_VERSION=0.144.5
ARG CLAUDE_CODE_VERSION=2.1.215
RUN apt-get update \
    && apt-get install --yes --no-install-recommends ca-certificates curl git openssh-client tini \
    && npm install --global "@openai/codex@${CODEX_VERSION}" "@anthropic-ai/claude-code@${CLAUDE_CODE_VERSION}" \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*
COPY --from=builder /usr/local/go /usr/local/go
COPY --from=builder /out/eggyd /usr/local/bin/eggyd
COPY --from=builder /out/eggy /usr/local/bin/eggy
RUN mkdir -p /data/codex /data/claude /tmp/runs
ENV CODEX_HOME=/data/codex \
    CLAUDE_CONFIG_DIR=/data/claude \
    EGGY_CONFIG=/data/config.yaml \
    PATH="/usr/local/go/bin:${PATH}"
EXPOSE 8080
ENTRYPOINT ["/usr/bin/tini", "--"]
CMD ["eggyd"]
