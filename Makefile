.PHONY: build test lint coverage clean release-local

VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

build:
	CGO_ENABLED=0 go build -v -ldflags "-X main.version=$(VERSION)" -o matrix-helper ./cmd/main.go

test:
	go test -v -race ./...

lint:
	golangci-lint run

coverage:
	go test -v -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

release-local:
	goreleaser release --snapshot --clean

clean:
	rm -f matrix-helper coverage.out coverage.html
	rm -rf dist/

.DEFAULT_GOAL := build
