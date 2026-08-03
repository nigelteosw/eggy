GO ?= go

.PHONY: fmt vet test race build build-web smoke clean

fmt:
	gofmt -w cmd internal plugins

vet:
	$(GO) vet ./...

test:
	$(GO) test ./...

race:
	$(GO) test -race ./...

# vite no longer empties its output directory, because the tracked
# placeholder.html lives there. Clearing the bundle by hand keeps that from
# meaning every superseded hashed asset stays behind and gets embedded.
build-web:
	rm -rf plugins/webui/dist/assets plugins/webui/dist/index.html
	cd website && bun install && bun run build

build: build-web
	mkdir -p bin
	$(GO) build -trimpath -o bin/eggyd ./cmd/eggyd

smoke:
	./scripts/docker-smoke.sh

clean:
	rm -f bin/eggyd
