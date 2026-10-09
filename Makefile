VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/lukaskoebe/sandbox-studio/internal/version.Version=$(VERSION)
# Leaves out the databases of Caddy's PKI app, which Studio doesn't use.
TAGS := nobadger,nomysql,nopgx

.PHONY: all web build agent api test lint dev clean image

all: web build

web:
	cd web && pnpm install --frozen-lockfile && pnpm build

build: agent
	go build -tags "$(TAGS)" -ldflags "$(LDFLAGS)" -o bin/studio ./cmd/studio

# The guest agent always targets Linux; both architectures are embedded in the host binary.
agent:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o internal/agentbin/dist/studio-agent-linux-amd64 ./cmd/studio-agent
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o internal/agentbin/dist/studio-agent-linux-arm64 ./cmd/studio-agent

# Regenerates the web client's API types from the Go handlers.
api:
	go run ./cmd/studio openapi > web/openapi.json
	cd web && pnpm exec openapi-typescript openapi.json -o src/lib/api/schema.gen.ts

test:
	go vet ./...
	go test ./...

lint:
	test -z "$$(gofmt -l .)"
	cd web && pnpm typecheck && pnpm lint && pnpm check

dev:
	go run ./cmd/studio & cd web && pnpm dev

clean:
	rm -rf bin internal/webui/static/app internal/agentbin/dist/studio-agent-*

# Dev only: build the base image with the host Docker and load it into microsandbox.
# Releases pull ghcr.io/lukaskoebe/sandbox-studio-base by digest instead.
image:
	docker build -t sandbox-studio-base:dev images/base
	docker save sandbox-studio-base:dev | msb load -t sandbox-studio-base:dev
	docker rmi sandbox-studio-base:dev
