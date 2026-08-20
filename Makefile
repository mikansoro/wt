BINARY := wt
VERSION := $(shell git describe --tags --always 2>/dev/null || echo dev)
LDFLAGS := -s -w -X wt/internal/version.Version=$(VERSION)

.PHONY: build test lint fmt

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/wt

# -count=1 defeats the test cache: the integration tests exec a binary they build at run
# time, so Go cannot see that they depend on the cli/repo/git packages.
test:
	go test -count=1 ./...

lint:
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run; \
	else \
		go vet ./...; \
	fi

fmt:
	gofmt -l -w .
