BINARY := x-tui
PKG := ./cmd/x-tui
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -ldflags "-X main.version=$(VERSION)"

.DEFAULT_GOAL := build
.PHONY: build run check count json mark-read auth test fmt vet tidy lint install clean help

build: ## Build the binary into ./x-tui
	go build $(LDFLAGS) -o $(BINARY) $(PKG)

run: ## Build and run the TUI (sources .env if present)
	@if [ -f .env ]; then . ./.env; fi; go run $(PKG)

check: ## Fetch one page from each timeline and exit (sources .env if present)
	@if [ -f .env ]; then . ./.env; fi; go run $(PKG) --check

count: ## Print the unread count (for the launcher; sources .env if present)
	@if [ -f .env ]; then set -a; . ./.env; set +a; fi; go run $(PKG) --count

json: ## Print unread posts as JSON (for the 'all' timeline; sources .env if present)
	@if [ -f .env ]; then set -a; . ./.env; set +a; fi; go run $(PKG) --json

mark-read: ## Mark read the post ids on stdin, one per line (sources .env if present)
	@if [ -f .env ]; then set -a; . ./.env; set +a; fi; go run $(PKG) --mark-read

auth: ## Capture x.com session (auth_token + ct0) into .env (opens a browser to log in)
	./tools/auth.sh x .env

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
