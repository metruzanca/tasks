BIN ?= bin
VERSION ?= dev
TOOLS := $(CURDIR)/.tools

.PHONY: all build tasks plugin-github install test lint fmt generate clean

all: build

build: tasks plugin-github

tasks:
	@mkdir -p $(BIN)
	go build -ldflags "-X main.version=$(VERSION)" -o $(BIN)/tasks ./cmd/tasks

plugin-github:
	@mkdir -p $(BIN)
	go build -o $(BIN)/tasks-plugin-github ./cmd/tasks-plugin-github

install:
	go install -ldflags "-X main.version=$(VERSION)" ./cmd/tasks ./cmd/tasks-plugin-github

test:
	go test ./...

lint:
	go vet ./...

fmt:
	gofmt -w ./cmd ./internal ./pkg ./plugins

generate: $(TOOLS)/buf $(TOOLS)/protoc-gen-go $(TOOLS)/protoc-gen-go-grpc
	PATH=$(TOOLS):$$PATH $(TOOLS)/buf generate

$(TOOLS)/buf:
	@mkdir -p $(TOOLS)
	GOBIN=$(TOOLS) go install github.com/bufbuild/buf/cmd/buf@latest

$(TOOLS)/protoc-gen-go:
	@mkdir -p $(TOOLS)
	GOBIN=$(TOOLS) go install google.golang.org/protobuf/cmd/protoc-gen-go@latest

$(TOOLS)/protoc-gen-go-grpc:
	@mkdir -p $(TOOLS)
	GOBIN=$(TOOLS) go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

clean:
	rm -rf $(BIN)
