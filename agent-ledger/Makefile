.PHONY: fmt fmt-check vet test test-short test-race test-integration coverage build check clean

PKG := ./...
BIN_DIR := bin
BINARY := $(BIN_DIR)/agent-ledger
CMD := ./cmd/agent-ledger

# CGO disabled for static-friendly builds. Later tasks may re-enable for
# SQLite via mattn/go-sqlite3, but Phase 1 Task 001 stays pure-Go.
export CGO_ENABLED ?= 0

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

# coverage emits an HTML report alongside a coverage.out file. The
# threshold is intentionally informational for Phase 1.
coverage:
	@mkdir -p $(BIN_DIR)
	go test -covermode=atomic -coverprofile=$(BIN_DIR)/coverage.out $(PKG)
	go tool cover -html=$(BIN_DIR)/coverage.out -o $(BIN_DIR)/coverage.html
	@echo "coverage report: $(BIN_DIR)/coverage.html"

build:
	@mkdir -p $(BIN_DIR)
	go build -o $(BINARY) $(CMD)

check: fmt-check vet test build

clean:
	rm -rf $(BIN_DIR) dist
