# NexusValet Makefile

.PHONY: build run clean deps test install help

# Default target
all: build

# Build the application
build:
	@echo "Building NexusValet..."
	@go build -o bin/nexusvalet cmd/nexusvalet/main.go
	@echo "Build complete: bin/nexusvalet"

# Run the application
run:
	@echo "Running NexusValet..."
	@go run cmd/nexusvalet/main.go

# Clean build artifacts
clean:
	@echo "Cleaning build artifacts..."
	@rm -rf bin/
	@rm -f session.json
	@rm -f sessions.db
	@echo "Clean complete"

# Download and install dependencies
deps:
	@echo "Installing dependencies..."
	@go mod download
	@go mod tidy
	@echo "Dependencies installed"

# Run tests
test:
	@echo "Running tests..."
	@go test -v ./...

# Install the application to GOPATH/bin
install: build
	@echo "Installing NexusValet to GOPATH/bin..."
	@cp bin/nexusvalet $(GOPATH)/bin/nexusvalet
	@echo "Installation complete"

# Create necessary directories
dirs:
	@mkdir -p bin
	@mkdir -p plugins
	@mkdir -p logs

# Run with development settings
dev: dirs
	@echo "Running in development mode..."
	@go run cmd/nexusvalet/main.go

# Build for different platforms
build-linux:
	@echo "Building for Linux..."
	@GOOS=linux GOARCH=amd64 go build -o bin/nexusvalet-linux cmd/nexusvalet/main.go

build-windows:
	@echo "Building for Windows..."
	@GOOS=windows GOARCH=amd64 go build -o bin/nexusvalet-windows.exe cmd/nexusvalet/main.go

build-mac:
	@echo "Building for macOS..."
	@GOOS=darwin GOARCH=amd64 go build -o bin/nexusvalet-mac cmd/nexusvalet/main.go

# Build for all platforms
build-all: build-linux build-windows build-mac
	@echo "Cross-platform builds complete"

# Format code
fmt:
	@echo "Formatting code..."
	@go fmt ./...

# Run linter
lint:
	@echo "Running linter..."
	@golangci-lint run

# Generate documentation
docs:
	@echo "Generating documentation..."
	@godoc -http=:6060

# Show help
help:
	@echo "NexusValet Makefile Commands:"
	@echo ""
	@echo "  build        - Build the application"
	@echo "  run          - Run the application"
	@echo "  dev          - Run in development mode"
	@echo "  clean        - Clean build artifacts"
	@echo "  deps         - Install dependencies"
	@echo "  test         - Run tests"
	@echo "  install      - Install to GOPATH/bin"
	@echo "  fmt          - Format code"
	@echo "  lint         - Run linter"
	@echo "  docs         - Generate documentation"
	@echo ""
	@echo "  Cross-platform builds:"
	@echo "  build-linux  - Build for Linux"
	@echo "  build-windows- Build for Windows"
	@echo "  build-mac    - Build for macOS"
	@echo "  build-all    - Build for all platforms"
	@echo ""
	@echo "  help         - Show this help message"

