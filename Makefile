BINARY := wtx

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
	-X github.com/bkildow/wtx/cmd.version=$(VERSION) \
	-X github.com/bkildow/wtx/cmd.commit=$(COMMIT) \
	-X github.com/bkildow/wtx/cmd.date=$(DATE)

.DEFAULT_GOAL := build

.PHONY: build install test test-short e2e vet fmt clean dev

build:
	go build -ldflags '$(LDFLAGS)' -o $(BINARY) ./cmd/wtx
	go build -ldflags '$(LDFLAGS)' -o wt ./cmd/wt

install:
	go install -ldflags '$(LDFLAGS)' ./cmd/wtx ./cmd/wt

test:
	go test ./...

test-short:
	go test -short ./...

e2e:
	go test ./e2e/ -count=1

vet:
	golangci-lint run ./...

fmt:
	gofumpt -l -w .

clean:
	rm -f $(BINARY) wt

dev: fmt vet test build
