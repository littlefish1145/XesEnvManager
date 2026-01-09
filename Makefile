# Makefile for Python Manager with WSL Support

# Detect OS
ifeq ($(OS),Windows_NT)
    DETECTED_OS := Windows
else
    DETECTED_OS := Unix
endif

# Build the application for Windows
build-windows:
ifeq ($(DETECTED_OS),Windows)
	@powershell -Command "$$env:GOOS='windows'; go build -o python-mgr.exe ./cmd/python-mgr"
else
	GOOS=windows go build -o python-mgr.exe ./cmd/python-mgr
endif

# Build the WSL server (cross-compile from Windows)
build-wsl-server:
ifeq ($(DETECTED_OS),Windows)
	@echo "Cross-compiling for Linux..."
	@powershell -Command "$$env:GOOS='linux'; $$env:CGO_ENABLED='0'; go build -o python-manager-wsl-server ./cmd/wsl-server"
else
	CGO_ENABLED=0 GOOS=linux go build -o python-manager-wsl-server ./cmd/wsl-server
endif

# Install dependencies
deps:
	go mod tidy

# Run tests
test:
	go test ./...

# Clean build artifacts
clean:
ifeq ($(DETECTED_OS),Windows)
	@if exist python-mgr.exe del /Q python-mgr.exe
	@if exist python-manager-wsl-server del /Q python-manager-wsl-server
else
	rm -f python-mgr.exe python-manager-wsl-server
endif

# Install the application
install: build-windows
	@echo "Copy python-mgr.exe to desired installation directory"

# Generate gRPC code (requires protoc and plugins installed)
gen-grpc:
	protoc --go_out=. --go-grpc_out=. proto/python_service.proto

# Build both Windows client and WSL server
build-all: build-windows build-wsl-server
	@echo ""
	@echo "Build complete!"
	@echo "  - Windows client: python-mgr.exe"
	@echo "  - WSL server: python-manager-wsl-server"
	@echo ""
	@echo "To deploy WSL server, run: make deploy-wsl"

# Run the WSL server (for testing)
run-wsl-server: build-wsl-server
ifeq ($(DETECTED_OS),Windows)
	@echo "WSL server built successfully!"
	@echo "Copy to WSL and run with:"
	@echo "  wsl cp python-manager-wsl-server ~/bin/"
	@echo "  wsl ./python-manager-wsl-server"
else
	./python-manager-wsl-server
endif

# Deploy WSL server to WSL
deploy-wsl: build-wsl-server
ifeq ($(DETECTED_OS),Windows)
	@echo "Deploying to WSL..."
	@powershell -Command "wsl test -d ~/bin && wsl cp python-manager-wsl-server ~/bin/ || wsl cp python-manager-wsl-server /usr/local/bin/"
	@powershell -Command "wsl test -f ~/bin/python-manager-wsl-server && wsl chmod +x ~/bin/python-manager-wsl-server || wsl chmod +x /usr/local/bin/python-manager-wsl-server 2>nul || true"
	@echo ""
	@echo "Deployed! Run in WSL with:"
	@echo "  wsl python-manager-wsl-server"
else
	cp python-manager-wsl-server /usr/local/bin/
	chmod +x /usr/local/bin/python-manager-wsl-server
endif

# Quick build for Windows only
build: build-windows

.PHONY: build-windows build-wsl-server deps test clean install gen-grpc build-all run-wsl-server deploy-wsl build
