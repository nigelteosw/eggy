# syntax=docker/dockerfile:1.7
FROM oven/bun:1 AS web-builder
WORKDIR /src/web
COPY web/package.json web/bun.lock* ./
RUN bun install
COPY web/ ./
RUN bun run build

FROM golang:1.26-bookworm AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web-builder /src/internal/adapters/webui/dist ./internal/adapters/webui/dist
RUN CGO_ENABLED=0 go test ./... \
    && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/eggyd ./cmd/eggyd \
    && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/eggy ./cmd/eggy

FROM debian:bookworm-slim
RUN apt-get update \
    && apt-get install --yes --no-install-recommends ca-certificates curl git openssh-client tini \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*
COPY --from=builder /usr/local/go /usr/local/go
COPY --from=builder /out/eggyd /usr/local/bin/eggyd
COPY --from=builder /out/eggy /usr/local/bin/eggy
RUN mkdir -p /tmp/runs
ENV EGGY_CONFIG=/data/config.yaml \
    PATH="/usr/local/go/bin:${PATH}"
EXPOSE 8080
ENTRYPOINT ["/usr/bin/tini", "--"]
CMD ["eggyd"]
