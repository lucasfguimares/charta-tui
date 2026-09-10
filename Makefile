COVERAGE_THRESHOLD ?= 50.0

.PHONY: build build-windows install test test-race test-integration coverage coverage-check lint lint-fmt fmt fmt-check vet security release-check ci

build:
	mkdir -p bin
	go build -ldflags "-X github.com/lucasfguimares/charta-tui/internal/cli.version=$$(git describe --tags --always --dirty) -X github.com/lucasfguimares/charta-tui/internal/cli.commit=$$(git rev-parse --short HEAD) -X github.com/lucasfguimares/charta-tui/internal/cli.buildDate=$$(date -u +%Y-%m-%dT%H:%M:%SZ)" -o bin/charta ./cmd/charta

build-windows:
	mkdir -p bin
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X github.com/lucasfguimares/charta-tui/internal/cli.version=$$(git describe --tags --always --dirty) -X github.com/lucasfguimares/charta-tui/internal/cli.commit=$$(git rev-parse --short HEAD) -X github.com/lucasfguimares/charta-tui/internal/cli.buildDate=$$(date -u +%Y-%m-%dT%H:%M:%SZ)" -o bin/charta-windows-amd64.exe ./cmd/charta

install: build
	./scripts/install.sh ./bin/charta

test:
	go test ./...

test-race:
	go test -race ./...

test-integration:
	go test -tags=integration ./...

coverage:
	go test -race -shuffle=on -covermode=atomic -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

coverage-check: coverage
	@total="$$(go tool cover -func=coverage.out | awk '/^total:/ {gsub(/%/, "", $$3); print $$3}')"; \
	printf 'Total coverage: %s%% (required: %s%%)\n' "$$total" "$(COVERAGE_THRESHOLD)"; \
	awk -v total="$$total" -v threshold="$(COVERAGE_THRESHOLD)" 'BEGIN { if (total + 0 < threshold + 0) exit 1 }'

lint:
	golangci-lint run ./...

lint-fmt:
	golangci-lint fmt --diff ./...

fmt:
	gofmt -w .

fmt-check:
	@files="$$(gofmt -l .)"; \
	if [ -n "$$files" ]; then printf 'Files need gofmt:\n%s\n' "$$files"; exit 1; fi

vet:
	go vet ./...

security:
	govulncheck ./...

release-check:
	goreleaser check
	goreleaser release --snapshot --clean

ci: fmt-check vet coverage-check lint-fmt lint security release-check
