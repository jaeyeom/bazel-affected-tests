# Makefile for bazel-affected-tests

# Variables
GO := go
GOLANGCI_LINT := golangci-lint
BINARY_NAME := bazel-affected-tests
BINARY_DIR := bin
BINARY_PATH := $(BINARY_DIR)/$(BINARY_NAME)
COVERAGE_FILE := coverage.out
COVERAGE_HTML := coverage.html
COVERAGE_THRESHOLD := 60

# Default target
.DEFAULT_GOAL := all

# Phony targets
.PHONY: all check format check-format lint fix vet test build coverage coverage-html coverage-report clean-coverage clean install help

# Main workflows
all: format fix test build

check: check-format lint test build

# Formatting targets
format:
	@echo "Formatting Go code..."
	@gofmt -w .

check-format:
	@echo "Checking code formatting..."
	@output=$$(gofmt -l .); \
	if [ -n "$$output" ]; then \
		echo "The following files are not formatted:"; \
		echo "$$output"; \
		exit 1; \
	fi
	@echo "All files are properly formatted"

# Linting targets
lint:
	@echo "Running golangci-lint..."
	@$(GOLANGCI_LINT) run ./...

fix: format
	@echo "Running golangci-lint with auto-fix..."
	@$(GOLANGCI_LINT) run --fix ./...

# Vetting target
vet:
	@echo "Running go vet..."
	@$(GO) vet ./...

# Testing targets
test:
	@echo "Running tests..."
	@$(GO) test -v ./...

coverage:
	@echo "Running tests with coverage..."
	@$(GO) test -coverprofile=$(COVERAGE_FILE) ./...
	@echo "Checking coverage threshold ($(COVERAGE_THRESHOLD)%)..."
	@total=$$($(GO) tool cover -func=$(COVERAGE_FILE) | grep total | awk '{print $$3}' | sed 's/%//'); \
	if [ "$$(echo "$$total < $(COVERAGE_THRESHOLD)" | bc)" -eq 1 ]; then \
		echo "Coverage $$total% is below threshold $(COVERAGE_THRESHOLD)%"; \
		exit 1; \
	else \
		echo "Coverage threshold met: $$total%"; \
	fi

coverage-html: coverage
	@echo "Generating HTML coverage report..."
	@$(GO) tool cover -html=$(COVERAGE_FILE) -o $(COVERAGE_HTML)
	@echo "Opening coverage report in browser..."
	@open $(COVERAGE_HTML) || xdg-open $(COVERAGE_HTML) 2>/dev/null || echo "Please open $(COVERAGE_HTML) manually"

coverage-report: coverage
	@echo "Coverage report by function:"
	@$(GO) tool cover -func=$(COVERAGE_FILE)

clean-coverage:
	@echo "Removing coverage files..."
	@rm -f $(COVERAGE_FILE) $(COVERAGE_HTML)

# Build targets
build:
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p $(BINARY_DIR)
	@$(GO) build -o $(BINARY_PATH) ./cmd/$(BINARY_NAME)
	@echo "Binary built at $(BINARY_PATH)"

# Install target
install:
	@echo "Installing $(BINARY_NAME)..."
	@$(GO) install ./cmd/$(BINARY_NAME)

# Clean target
clean: clean-coverage
	@echo "Cleaning build artifacts..."
	@rm -rf $(BINARY_DIR)
	@echo "Clean complete"

# Help target
help:
	@echo "Available targets:"
	@echo "  all              - Run format, fix, test, and build (default)"
	@echo "  check            - Run check-format, lint, test, and build (CI-friendly)"
	@echo "  format           - Format code with gofmt"
	@echo "  check-format     - Verify code formatting"
	@echo "  lint             - Run golangci-lint"
	@echo "  fix              - Auto-fix issues with golangci-lint"
	@echo "  vet              - Run go vet"
	@echo "  test             - Run tests"
	@echo "  build            - Build binary to $(BINARY_PATH)"
	@echo "  coverage         - Run tests with coverage and check threshold"
	@echo "  coverage-html    - Generate and open HTML coverage report"
	@echo "  coverage-report  - Print per-function coverage report"
	@echo "  clean-coverage   - Remove coverage files"
	@echo "  clean            - Remove build artifacts and coverage files"
	@echo "  install          - Install binary via go install"
	@echo "  help             - Show this help message"
