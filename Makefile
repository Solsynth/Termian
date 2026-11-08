BIN_DIR := bin

.PHONY: all build build-client build-server test test-client test-hosted \
	tidy docker-client docker-server

all: build

build: build-client build-server

build-client:
	go build -o $(BIN_DIR)/termian .

# The server lives in its own module so client installs stay independent.
build-server:
	cd hosted && go build -o ../$(BIN_DIR)/termian-server ./cmd/termian-server

test: test-client test-hosted

test-client:
	go test ./...

test-hosted:
	cd hosted && go test ./...

tidy:
	go mod tidy

# Requires shared/v0.2.6 to be published; local builds use hosted/go.work.
tidy-hosted:
	cd hosted && GOWORK=off go mod tidy

docker-client:
	docker build --target client -t termian-client .

docker-server:
	docker build --target server -t termian-server .
