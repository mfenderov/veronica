.PHONY: build test test-coverage crap lint fmt tidy clean install

BINARY=veronica
VERSION=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS=-ldflags "-X main.Version=$(VERSION)"

## Build

build:
	go build $(LDFLAGS) -o bin/$(BINARY) ./cmd/veronica

## Test

test:
	go test -v -race ./...

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

tidy:
	go mod tidy

## Clean

clean:
	rm -rf bin/ coverage.out coverage.html

## Install

install: build
	mkdir -p ~/bin
	cp bin/$(BINARY) ~/bin/$(BINARY).tmp
	mv -f ~/bin/$(BINARY).tmp ~/bin/$(BINARY)
