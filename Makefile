SHELL := /bin/bash
GO ?= go
NPM ?= npm
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
GO_PKGS := ./cmd/... ./internal/...

.PHONY: all build frontend test test-ui integration lint e2e clean

all: build

# The SPA is built first and copied into the Go embed tree.
frontend:
	cd webui && $(NPM) ci && $(NPM) run build
	rm -rf internal/web/dist
	cp -r webui/build internal/web/dist
	@test -f internal/web/dist/index.html || cp internal/web/dist/200.html internal/web/dist/index.html

build: frontend
	mkdir -p dist
	GOOS=linux GOARCH=amd64 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o dist/caddykit-linux-amd64 ./cmd/caddykit
	GOOS=linux GOARCH=arm64 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o dist/caddykit-linux-arm64 ./cmd/caddykit

test:
	$(GO) test $(GO_PKGS)

test-ui:
	cd webui && $(NPM) ci && $(NPM) run check && $(NPM) run lint

# Runs against two current Caddy minor releases. Requires Docker.
integration:
	CADDY_IMAGE=caddy:2.10 $(GO) test -tags=integration ./test/integration -count=1 -v
	CADDY_IMAGE=caddy:2.11 $(GO) test -tags=integration ./test/integration -count=1 -v

lint:
	$(GO) vet $(GO_PKGS)
	cd webui && $(NPM) run check && $(NPM) run lint
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run $(GO_PKGS); \
	else \
		echo "golangci-lint not installed; go vet and the frontend checks passed"; \
	fi

e2e:
	cd webui && $(NPM) run test:e2e

clean:
	rm -rf dist webui/build webui/.svelte-kit
