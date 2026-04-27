.PHONY: fmt fmt-check vet test test-short test-race test-integration test-adapters coverage build check clean release-snapshot

PKG := ./...
BIN_DIR := bin
BINARY := $(BIN_DIR)/agent-ledger
CMD := ./cmd/agent-ledger

# CGO disabled for static-friendly builds. Later tasks may re-enable for
# SQLite via mattn/go-sqlite3, but Phase 1 stays pure-Go (modernc).
export CGO_ENABLED ?= 0

# Version metadata, injected via -ldflags. Falls back to "dev" / "unknown"
# when git is unavailable (for example, in an extracted source tarball).
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

VERSION_PKG := github.com/ruminaider/agent-clis/agent-ledger/internal/version
LDFLAGS := -s -w \
	-X $(VERSION_PKG).Version=$(VERSION) \
	-X $(VERSION_PKG).Commit=$(COMMIT) \
	-X $(VERSION_PKG).BuildDate=$(DATE)

fmt:
	gofmt -w .

fmt-check:
	@out=$$(gofmt -l .); \
	if [ -n "$$out" ]; then \
		echo "gofmt needs to be run on:"; echo "$$out"; exit 1; \
	fi

vet:
	go vet $(PKG)

test:
	go test $(PKG)

# test-short skips long subprocess and concurrency tests. Useful for
# editor save loops.
test-short:
	go test -short $(PKG)

# test-race runs every package with the race detector. SQLite under
# modernc is pure-Go, so the race detector observes the kernel's
# real concurrency.
test-race:
	go test -race $(PKG)

# test-integration runs only the top-level subprocess suite under
# tests/. The CLI binary is rebuilt inside each test process.
test-integration:
	go test ./tests/...

test-adapters:
	./adapters/tests/run.sh

# coverage emits an HTML report alongside a coverage.out file. The
# threshold is intentionally informational for Phase 1.
coverage:
	@mkdir -p $(BIN_DIR)
	go test -covermode=atomic -coverprofile=$(BIN_DIR)/coverage.out $(PKG)
	go tool cover -html=$(BIN_DIR)/coverage.out -o $(BIN_DIR)/coverage.html
	@echo "coverage report: $(BIN_DIR)/coverage.html"

build:
	@mkdir -p $(BIN_DIR)
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) $(CMD)

check: fmt-check vet test test-adapters build

# release-snapshot builds the four-target Phase 1 archive set under dist/
# without publishing. Requires goreleaser; see scripts/release-snapshot.sh
# for installation steps.
release-snapshot:
	./scripts/release-snapshot.sh

clean:
	rm -rf $(BIN_DIR) dist
