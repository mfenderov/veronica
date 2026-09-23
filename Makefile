.PHONY: build test test-e2e test-coverage crap lint fmt fix tidy clean install

BINARY=veronica
VERSION=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS=-ldflags "-X main.Version=$(VERSION)"

## Build

build:
	go build $(LDFLAGS) -o bin/$(BINARY) ./cmd/veronica

## Test

test:
	go test -v -race -shuffle=on ./...

test-e2e:
	go test -v -race -shuffle=on ./e2e/...

test-coverage:
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

# CRAP quality gate: complexity² × (1-coverage)³ + complexity. Max 10 — keep
# functions small and tested. Exclude only test files.
crap:
	go test -coverprofile=coverage.out ./...
	go tool gocrap -coverprofile coverage.out \
		-exclude '*_test.go' \
		-max 10 ./...

## Development

lint:
	go tool golangci-lint run

fmt:
	go fmt ./...

fix:
	go fix ./...

tidy:
	go mod tidy && go mod tidy --diff

## Clean

clean:
	rm -rf bin/ coverage.out coverage.html

## Install

# Destination directory for installed binaries. Defaults to ~/.local/bin
# when it exists and is on PATH (matching install.sh), otherwise ~/bin.
# Override with: make install BINDIR=/custom/bin
BINDIR ?= $(shell \
	if [ -d "$(HOME)/.local/bin" ] && case ":$(PATH):" in *":$(HOME)/.local/bin:"*) true;; *) false;; esac; then \
		echo "$(HOME)/.local/bin"; \
	else \
		echo "$(HOME)/bin"; \
	fi)

install: build
	mkdir -p $(BINDIR)
	cp bin/$(BINARY) $(BINDIR)/$(BINARY).tmp
	mv -f $(BINDIR)/$(BINARY).tmp $(BINDIR)/$(BINARY)
