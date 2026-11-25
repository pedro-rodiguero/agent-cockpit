VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
STATICCHECK := honnef.co/go/tools/cmd/staticcheck@2026.2.2

.PHONY: build test lint menubar install clean

build: ## Build bin/cockpit and bin/cockpit-todo-server
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/cockpit ./cmd/cockpit
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/cockpit-todo-server ./cmd/cockpit-todo-server

test: ## Run the tests with the race detector
	go test -race ./...

lint: ## gofmt, go vet and staticcheck
	@test -z "$$(gofmt -l .)" || { gofmt -l .; echo "run gofmt -w ."; exit 1; }
	go vet ./...
	go run $(STATICCHECK) ./...

menubar: ## Build the menu bar app binary into bin/
	mkdir -p bin
	swift build -c release --package-path menubar
	cp menubar/.build/release/AgentCockpit bin/AgentCockpit

install: ## Install cockpit and the menu bar app for the current user
	./scripts/install.sh

clean:
	rm -rf bin
