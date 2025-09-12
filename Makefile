# Makefile for garp Go application

BINARY_NAME=garp
BINARY_PATH=bin/$(BINARY_NAME)
GO_FILES=$(shell find . -name "*.go" -type f)

# Version embedding
VERSION=0.2
LDFLAGS=-X main.version=$(VERSION)

# Default target
all: build

# Build the binary
build: $(BINARY_PATH)

$(BINARY_PATH): $(GO_FILES)
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p bin
	go build -ldflags "$(LDFLAGS)" -o $(BINARY_PATH) .
	@echo "Build completed: $(BINARY_PATH)"

# Clean build artifacts
clean:
	@echo "Cleaning build artifacts..."
	rm -rf bin
	@echo "Clean completed"

# Test the application
test:
	@echo "Running tests..."
	go test ./...
	@echo "Tests completed"

# Format Go code
fmt:
	@echo "Formatting Go code..."
	go fmt ./...
	@echo "Formatting completed"

# Run go mod tidy
tidy:
	@echo "Running go mod tidy..."
	go mod tidy
	@echo "Go mod tidy completed"

# Install dependencies
deps:
	@echo "Installing dependencies..."
	go mod download
	@echo "Dependencies installed"

# Development build with race detection
dev:
	@echo "Building development version with race detection..."
	@mkdir -p bin
	go build -race -ldflags "$(LDFLAGS)" -o bin/$(BINARY_NAME)-dev .
	@echo "Development build completed: bin/$(BINARY_NAME)-dev"

# Run the application with sample arguments
run: build
	@echo "Running $(BINARY_NAME) with sample arguments..."
	./$(BINARY_PATH)

# Install to user's local bin directory
install: build
	@echo "Installing $(BINARY_NAME) to ~/.local/bin..."
	@mkdir -p ~/.local/bin
	cp $(BINARY_PATH) ~/.local/bin/
	@echo "Installation completed: ~/.local/bin/$(BINARY_NAME)"
	@echo "Make sure ~/.local/bin is in your PATH"

# Uninstall from user's local bin directory
uninstall:
	@echo "Removing $(BINARY_NAME) from ~/.local/bin..."
	rm -f ~/.local/bin/$(BINARY_NAME)
	@echo "Uninstallation completed"

# Show help
help:
	@echo "Available targets:"
	@echo "  build    - Build the binary"
	@echo "  clean    - Remove build artifacts"
	@echo "  test     - Run tests"
	@echo "  fmt      - Format Go code"
	@echo "  tidy     - Run go mod tidy"
	@echo "  deps     - Install dependencies"
	@echo "  dev      - Build development version with race detection"
	@echo "  run      - Build and run with help"
	@echo "  install  - Install to ~/.local/bin"
	@echo "  uninstall- Remove from ~/.local/bin"
	@echo "  help     - Show this help"

.PHONY: all build clean test fmt tidy deps dev run install uninstall help
