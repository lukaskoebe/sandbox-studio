VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/lukaskoebe/sandbox-studio/internal/version.Version=$(VERSION)
# Release builds pin the base image by digest; see docs/release.md.
LDFLAGS += $(if $(BASE_IMAGE),-X github.com/lukaskoebe/sandbox-studio/internal/version.BaseImage=$(BASE_IMAGE))
LDFLAGS += $(if $(BROWSER_IMAGE),-X github.com/lukaskoebe/sandbox-studio/internal/version.BrowserImage=$(BROWSER_IMAGE))
# Leaves out the databases of Caddy's PKI app, which Studio doesn't use.
TAGS := nobadger,nomysql,nopgx
# pipefail in the image recipes needs bash.
SHELL := bash

.PHONY: all web build app agent api test lint check e2e e2e-bin dev clean image image-browser

all: web build

web:
	cd web && pnpm install --frozen-lockfile && pnpm build

build: agent
	go build -tags "$(TAGS)" -ldflags "$(LDFLAGS)" -o bin/studio ./cmd/studio

# The app (cmd/studio-app, docs/app.md), a module of its own next to bin/studio. On Linux it
# needs GTK3 and WebKitGTK 4.1 (libgtk-3-dev libwebkit2gtk-4.1-dev).
APP_TAGS := $(if $(filter Linux,$(shell uname -s)),gtk3)
app:
	cd cmd/studio-app && go build -tags "$(APP_TAGS)" -ldflags "-s -w -X main.version=$(VERSION)" -o ../../bin/studio-app .

# The guest agent always targets Linux; both architectures are embedded in the host binary.
agent:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o internal/agentbin/dist/studio-agent-linux-amd64 ./cmd/studio-agent
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o internal/agentbin/dist/studio-agent-linux-arm64 ./cmd/studio-agent

# Regenerates the web client's API types from the Go handlers.
api:
	go run ./cmd/studio openapi > web/openapi.json
	cd web && pnpm exec openapi-typescript openapi.json -o src/lib/api/schema.gen.ts

test:
	go vet -tags "$(TAGS)" ./...
	go test -tags "$(TAGS)" ./...

lint:
	test -z "$$(gofmt -l .)"
	cd web && pnpm typecheck && pnpm lint && pnpm check

# What ci checks that this machine can, before a push and always before a release
# (docs/release.md): formatting, golangci-lint at ci's version, vet and the tests, the app's
# module, the API schema and the web client's types, and the web checks. ci's other jobs (the
# macOS and Windows builds, the app's start on macOS, e2e) stay there; run `make e2e` too when
# the UI changed.
GOLANGCI_LINT := go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0
check: lint test
	$(GOLANGCI_LINT) run ./...
	go vet -tags e2e,nobadger,nomysql,nopgx ./...
	cd cmd/studio-app && go vet -tags "$(APP_TAGS)" . && go test -tags "$(APP_TAGS)" .
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o /dev/null ./cmd/studio-agent
	go run ./cmd/studio openapi | diff -u web/openapi.json -
	t=$$(mktemp -d) && cd web && pnpm exec openapi-typescript openapi.json -o "$$t/schema.gen.ts" >/dev/null && diff -u src/lib/api/schema.gen.ts "$$t/schema.gen.ts"; s=$$?; rm -rf "$$t"; exit $$s
	cd web && node --test ../internal/harness/opencode-plugin.test.mjs
	sh scripts/test-install.sh
	if command -v shellcheck >/dev/null; then shellcheck install.sh scripts/test-install.sh scripts/macos-app.sh; else echo "shellcheck isn't installed: ci runs it"; fi

# End-to-end tests: the web UI against Studio built with in-process fakes instead of VMs
# (internal/e2e). Needs Chromium; see CONTRIBUTING.md.
e2e: web e2e-bin
	cd web && pnpm e2e

e2e-bin:
	go build -tags "e2e,$(TAGS)" -ldflags "$(LDFLAGS)" -o bin/studio-e2e ./cmd/studio

dev:
	go run ./cmd/studio & cd web && pnpm dev

clean:
	rm -rf bin internal/webui/static/app internal/agentbin/dist/studio-agent-*

# Dev only: build the base image with the host Docker and load it into microsandbox.
# Releases pull ghcr.io/lukaskoebe/sandbox-studio-base by digest instead.
image:
	docker build -t sandbox-studio-base:dev images/base
	set -o pipefail; docker save sandbox-studio-base:dev | msb load -t sandbox-studio-base:dev
	docker rmi sandbox-studio-base:dev >/dev/null 2>&1 || true

# Dev only: the browser image for teammates' browser VMs (docs/browser.md).
image-browser:
	docker build -t sandbox-studio-browser:dev images/browser
	set -o pipefail; docker save sandbox-studio-browser:dev | msb load -t sandbox-studio-browser:dev
	docker rmi sandbox-studio-browser:dev >/dev/null 2>&1 || true
