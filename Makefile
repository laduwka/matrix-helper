.PHONY: build test lint clean

build:
	CGO_ENABLED=0 go build -v -o matrix-helper ./cmd/main.go

test:
	go test -v -race ./...

lint:
	golangci-lint run

# TODO
coverage:
	go test -v -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

clean:
	rm -f matrix-helper coverage.out coverage.html

.DEFAULT_GOAL := build
