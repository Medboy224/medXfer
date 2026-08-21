BINARY_NAME=xfer
BUILD_DIR=bin

.PHONY: all clean android-arm64 windows-amd64 linux-amd64

all: android-arm64 windows-amd64 linux-amd64

android-arm64:
	@echo "Building for Android (Termux ARM64)..."
	GOOS=android GOARCH=arm64 CGO_ENABLED=0 go build -ldflags="-s -w" -o $(BUILD_DIR)/$(BINARY_NAME)-android-arm64 ./cmd/medXfer

windows-amd64:
	@echo "Building for Windows (x86_64)..."
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" -o $(BUILD_DIR)/$(BINARY_NAME)-windows-amd64.exe ./cmd/medXfer

linux-amd64:
	@echo "Building for Linux (x86_64)..."
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" -o $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64 ./cmd/medXfer

clean:
	rm -rf $(BUILD_DIR)