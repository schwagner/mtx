.PHONY: build test lint run clean

BINARY=mtx
BUILD_DIR=bin
# See flake.nix for why these tags are required.
TAGS=sqlite_fts5,goolm

build:
	go build -tags $(TAGS) -ldflags="-s -w" -trimpath -o $(BUILD_DIR)/$(BINARY) ./cmd/mtx

test:
	go test -tags $(TAGS) ./... -race

lint:
	golangci-lint run --build-tags $(TAGS) ./...

run: build
	./$(BUILD_DIR)/$(BINARY)

clean:
	rm -rf $(BUILD_DIR)
