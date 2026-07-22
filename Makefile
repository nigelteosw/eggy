GO ?= go

.PHONY: fmt vet test race build build-web smoke clean

fmt:
	gofmt -w cmd internal

vet:
	$(GO) vet ./...

test:
	$(GO) test ./...

race:
	$(GO) test -race ./...

build-web:
	cd web && bun install && bun run build

build: build-web
	mkdir -p bin
	$(GO) build -trimpath -o bin/eggyd ./cmd/eggyd
	$(GO) build -trimpath -o bin/eggy ./cmd/eggy

smoke:
	./scripts/docker-smoke.sh

clean:
	rm -f bin/eggyd bin/eggy
