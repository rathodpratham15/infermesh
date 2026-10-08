.PHONY: build test lint fmt run

build:
	go build -o bin/ ./cmd/...

test:
	go test -race -count=1 ./...

lint:
	golangci-lint run ./...

fmt:
	gofmt -w .

run:
	go run ./cmd/gateway
