.PHONY: build test test-race test-integration lint fmt vet

build:
	mkdir -p bin
	go build -ldflags "-X github.com/lucasfguimares/tui-db/internal/cli.version=$$(git describe --tags --always --dirty) -X github.com/lucasfguimares/tui-db/internal/cli.commit=$$(git rev-parse --short HEAD) -X github.com/lucasfguimares/tui-db/internal/cli.buildDate=$$(date -u +%Y-%m-%dT%H:%M:%SZ)" -o bin/tui-db ./cmd/tui-db

test:
	go test ./...

test-race:
	go test -race ./...

test-integration:
	go test -tags=integration ./...

lint:
	golangci-lint run ./...

fmt:
	gofmt -w .

vet:
	go vet ./...
