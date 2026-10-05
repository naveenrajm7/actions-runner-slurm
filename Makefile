GO ?= go

.PHONY: build test fmt

build:
	mkdir -p bin
	$(GO) build -trimpath -o bin/slurm-gha ./cmd/slurm-gha

test:
	$(GO) test ./...

fmt:
	$(GO) fmt ./...
