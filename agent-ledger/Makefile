.PHONY: fmt fmt-check vet test build check clean

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

build:
	@mkdir -p $(BIN_DIR)
	go build -o $(BINARY) $(CMD)

check: fmt-check vet test build

clean:
	rm -rf $(BIN_DIR) dist
