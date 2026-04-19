.PHONY: build run clean docker

BINARY_NAME=livesource-manager
BUILD_DIR=bin

build:
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=1 go build -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/manager

run: build
	./$(BUILD_DIR)/$(BINARY_NAME) -once -config ./configs/config.ini

clean:
	rm -rf $(BUILD_DIR)

docker:
	docker build -t livesource-manager-go .

# 交叉编译
build-linux-amd64:
	CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -o $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64 ./cmd/manager

build-windows-amd64:
	CGO_ENABLED=1 GOOS=windows GOARCH=amd64 go build -o $(BUILD_DIR)/$(BINARY_NAME)-windows-amd64.exe ./cmd/manager

build-darwin-amd64:
	CGO_ENABLED=1 GOOS=darwin GOARCH=amd64 go build -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-amd64 ./cmd/manager

build-darwin-arm64:
	CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 go build -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-arm64 ./cmd/manager
