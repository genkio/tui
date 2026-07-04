BINARY := folo-tui
PKG := ./cmd/folo-tui
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -ldflags "-X main.version=$(VERSION)"

.DEFAULT_GOAL := build
.PHONY: build run check count json mark-read auth test fmt vet tidy lint install clean help

build: ## Build the binary into ./folo-tui
	go build $(LDFLAGS) -o $(BINARY) $(PKG)

auth: ## Log in via a browser and save the session to ~/.config/tui/env
	go run $(PKG) --auth

run: ## Build and run the TUI (sources .env if present)
	@if [ -f .env ]; then . ./.env; fi; go run $(PKG)

check: ## Verify the connection to Folo and exit (sources .env if present)
	@if [ -f .env ]; then . ./.env; fi; go run $(PKG) --check

count: ## Print the unread count (for the launcher; sources .env if present)
	@if [ -f .env ]; then set -a; . ./.env; set +a; fi; go run $(PKG) --count

json: ## Print pending articles as JSON (for the 'all' timeline; sources .env if present)
	@if [ -f .env ]; then set -a; . ./.env; set +a; fi; go run $(PKG) --json

mark-read: ## Mark read the entry ids on stdin, one per line (sources .env if present)
	@if [ -f .env ]; then set -a; . ./.env; set +a; fi; go run $(PKG) --mark-read

test: ## Run tests
	go test ./...

fmt: ## Format all Go code
	gofmt -w .

vet: ## Run go vet
	go vet ./...

tidy: ## Tidy go.mod / go.sum
	go mod tidy

lint: fmt vet test ## fmt + vet + test

install: ## Install to $GOBIN (or $GOPATH/bin)
	go install $(LDFLAGS) $(PKG)

clean: ## Remove build artifacts
	rm -f $(BINARY)

help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
	  awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-9s\033[0m %s\n", $$1, $$2}'
