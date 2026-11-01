BIN_DIR ?= dist
APP := go-content-go
.DEFAULT_GOAL := build

.PHONY: build build-linux-amd64 build-linux-arm64 build-darwin-amd64 build-darwin-arm64 build-windows-amd64

$(BIN_DIR):
	mkdir -p $(BIN_DIR)

build: build-linux-amd64 build-linux-arm64 build-darwin-amd64 build-darwin-arm64 build-windows-amd64

build-linux-amd64: $(BIN_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o $(BIN_DIR)/$(APP)-linux-amd64 ./cmd/server

build-linux-arm64: $(BIN_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o $(BIN_DIR)/$(APP)-linux-arm64 ./cmd/server

build-darwin-amd64: $(BIN_DIR)
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o $(BIN_DIR)/$(APP)-darwin-amd64 ./cmd/server

build-darwin-arm64: $(BIN_DIR)
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o $(BIN_DIR)/$(APP)-darwin-arm64 ./cmd/server

build-windows-amd64: $(BIN_DIR)
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o $(BIN_DIR)/$(APP)-windows-amd64.exe ./cmd/server
