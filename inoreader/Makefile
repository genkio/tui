BINARY := inoreader-tui
PKG := ./cmd/inoreader-tui
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -ldflags "-X main.version=$(VERSION)"

.DEFAULT_GOAL := build
.PHONY: build run check count json mark-read auth test fmt vet tidy lint install clean help

build: ## Build the binary into ./inoreader-tui
	go build $(LDFLAGS) -o $(BINARY) $(PKG)

auth: ## Capture the Inoreader session cookie into .env (opens a browser to log in)
	./tools/auth.sh inoreader https://www.inoreader.com/all_articles inoreader.com INOREADER_COOKIE .env

run: ## Build and run the TUI (sources .env if present)
	@if [ -f .env ]; then . ./.env; fi; go run $(PKG)

check: ## Verify the connection to Inoreader and exit (sources .env if present)
	@if [ -f .env ]; then . ./.env; fi; go run $(PKG) --check

count: ## Print the unread count (for the launcher; sources .env if present)
	@if [ -f .env ]; then set -a; . ./.env; set +a; fi; go run $(PKG) --count

json: ## Print unread articles as JSON (for the 'all' timeline; sources .env if present)
	@if [ -f .env ]; then set -a; . ./.env; set +a; fi; go run $(PKG) --json

mark-read: ## Mark read the article ids on stdin, one per line (sources .env if present)
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
