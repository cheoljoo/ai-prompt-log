.PHONY: go py build-go test-go test-py fmt lint clean

# Fall back to the local Go toolchain under ~/go1.24.2 if `go` is not on PATH
# (e.g. it was installed manually rather than via apt/snap).
GO := $(shell command -v go 2>/dev/null || echo $(HOME)/go1.24.2/bin/go)
UV := uv

# Extra CLI args, e.g. `make go ARGS="--all"`.
ARGS ?=

## Run the Go implementation (cmd/apl).
go:
	$(GO) run ./cmd/apl $(ARGS)

## Run the Python (poc) implementation via uv.
py:
	cd poc && $(UV) run apl $(ARGS)

## Build the Go binary into ./bin/apl.
build-go:
	$(GO) build -o bin/apl ./cmd/apl

## Run Go tests.
test-go:
	$(GO) test ./...

## Run Python (poc) tests.
test-py:
	cd poc && $(UV) run python -m unittest discover -s tests

clean:
	rm -rf bin
