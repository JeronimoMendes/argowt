GO ?= go
GOFMT ?= gofmt
VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || printf unknown)
BUILD_DATE ?= unknown

LDFLAGS := -s -w \
	-X argowt/internal/version.Version=$(VERSION) \
	-X argowt/internal/version.Commit=$(COMMIT) \
	-X argowt/internal/version.Date=$(BUILD_DATE)

.PHONY: all build fmt fmt-check test vet cross-build release-check clean

all: build

build:
	@mkdir -p bin
	$(GO) build -trimpath -ldflags '$(LDFLAGS)' -o bin/argowt ./cmd/argowt

fmt:
	$(GO) fmt ./...

fmt-check:
	@test -z "$$($(GOFMT) -l .)" || { $(GOFMT) -l .; echo "Go files need formatting; run 'make fmt'"; exit 1; }

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

cross-build:
	@mkdir -p bin/release-check
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o bin/release-check/argowt-darwin-amd64 ./cmd/argowt
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o bin/release-check/argowt-darwin-arm64 ./cmd/argowt
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o bin/release-check/argowt-linux-amd64 ./cmd/argowt
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o bin/release-check/argowt-linux-arm64 ./cmd/argowt

release-check: fmt-check test vet cross-build

clean:
	rm -rf bin
