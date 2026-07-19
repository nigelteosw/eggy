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
RUN apt-get update \
    && apt-get install --yes --no-install-recommends ca-certificates curl git openssh-client tini \
    && npm install --global "@openai/codex@${CODEX_VERSION}" \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*
COPY --from=builder /out/eggyd /usr/local/bin/eggyd
COPY --from=builder /out/eggy /usr/local/bin/eggy
RUN mkdir -p /data/codex /tmp/runs
ENV CODEX_HOME=/data/codex \
    EGGY_CONFIG=/data/config.yaml
VOLUME ["/data"]
EXPOSE 8080
ENTRYPOINT ["/usr/bin/tini", "--"]
CMD ["eggyd"]
