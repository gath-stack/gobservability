.PHONY: help test lint doc check

# Default target
help:
	@echo "Available targets:"
	@echo "  make example    - Run example code"
	@echo "  make test       - Run all tests"
	@echo "  make test-fast  - Run all tests (not verbose)"
	@echo "  make bench      - Run all benchmarks"
	@echo "  make lint       - Run linters"
	@echo "  make doc        - Start pkgsite"
	@echo "  make check      - Check linting and testing"

example:
	@go run cmd/example/main.go

# Run tests
test:
	@echo "Running tests..."
	@go test -v -race -coverprofile=coverage.out ./...
	@go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

test-fast:
	@echo "Running tests..."
	@go test ./...

integration:
	@echo "Running integration tests..."
	@go test -v -tags=integration ./...

bench:
	@echo "Running benchmarks..."
	@go test -bench=. -benchmem ./...

bench-long:
	@echo "Running long benchmarks (10s each)..."
	@go test -bench=. -benchtime=10s -benchmem ./...

doc:
	@echo "Starting Pkgsite..."
	@pkgsite -open .

# Lint the code
lint:
	@echo "Running linters..."
	@golangci-lint run -v --timeout 5m ./...

format:
	@echo "Formatting code..."
	@golangci-lint run --fix -v --timeout 5m ./...
	@templ fmt .
	@go fmt ./...

# Install development dependencies
install-deps:
	@echo "Installing development dependencies..."
	@go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	@go install golang.org/x/pkgsite/cmd/pkgsite@latest
	@echo "Dependencies installed"

check: test lint