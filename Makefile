GO ?= go
GOFMT ?= gofmt
VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || printf unknown)
BUILD_DATE ?= unknown
BINDIR ?= $(HOME)/.local/bin

LDFLAGS := -s -w \
	-X github.com/JeronimoMendes/argowt/internal/version.Version=$(VERSION) \
	-X github.com/JeronimoMendes/argowt/internal/version.Commit=$(COMMIT) \
	-X github.com/JeronimoMendes/argowt/internal/version.Date=$(BUILD_DATE)

.PHONY: all build install fmt fmt-check test vet cross-build release-check clean dev-up dev-down dev-run dev-seed dev-submit

all: build

build:
	@mkdir -p bin
	$(GO) build -trimpath -ldflags '$(LDFLAGS)' -o bin/argowt ./cmd/argowt

install: build
	@mkdir -p $(BINDIR)
	install -m 0755 bin/argowt $(BINDIR)/argowt

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

dev-up:
	dev/cluster.sh up

dev-down:
	dev/cluster.sh down

dev-run: build
	dev/cluster.sh run

dev-seed:
	dev/cluster.sh seed

# make dev-submit T=etl-pipeline P="dataset=orders seconds-per-step=10"
dev-submit:
	dev/cluster.sh submit $(T) $(P)
