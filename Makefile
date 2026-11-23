VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
STATICCHECK := honnef.co/go/tools/cmd/staticcheck@2025.1.1

.PHONY: build test lint menubar install clean

build: ## Build bin/cockpit
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/cockpit ./cmd/cockpit

test: ## Run the tests with the race detector
	go test -race ./...

lint: ## gofmt, go vet and staticcheck
	@test -z "$$(gofmt -l .)" || { gofmt -l .; echo "run gofmt -w ."; exit 1; }
	go vet ./...
	go run $(STATICCHECK) ./...

menubar: ## Build the menu bar app binary into bin/
	mkdir -p bin
	swiftc -O -o bin/AgentCockpit menubar/main.swift

install: ## Install cockpit and the menu bar app for the current user
	./scripts/install.sh

clean:
	rm -rf bin
