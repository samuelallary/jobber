.PHONY: check test lint

check: lint test

test:
	@go test ./...

lint:
	@golangci-lint run
