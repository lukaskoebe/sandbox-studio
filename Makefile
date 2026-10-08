VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/lukaskoebe/sandbox-studio/internal/version.Version=$(VERSION)

.PHONY: all web build agent test lint dev clean image

all: web build

web:
	cd web && pnpm install --frozen-lockfile && pnpm build

build: agent
	go build -ldflags "$(LDFLAGS)" -o bin/studio ./cmd/studio

# The guest agent always targets Linux; both architectures are embedded in the host binary.
agent:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o internal/agentbin/dist/studio-agent-linux-amd64 ./cmd/studio-agent
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o internal/agentbin/dist/studio-agent-linux-arm64 ./cmd/studio-agent

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
