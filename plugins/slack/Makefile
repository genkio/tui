BINARY := slack-tui
PKG := ./cmd/slack-tui
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -ldflags "-X main.version=$(VERSION)"

.DEFAULT_GOAL := build
.PHONY: build run check count auth test fmt vet tidy lint install clean rename help

build: ## Build the binary into ./slack-tui
	go build $(LDFLAGS) -o $(BINARY) $(PKG)

auth: ## Capture Slack browser session (xoxc + xoxd + workspace domain) into .env (opens a browser to log in)
	./tools/auth.sh slack .env

run: ## Build and run the TUI (sources .env if present)
	@if [ -f .env ]; then . ./.env; fi; go run $(PKG)

check: ## Connect to the server, list its tools, and exit (sources .env if present)
	@if [ -f .env ]; then . ./.env; fi; go run $(PKG) --check

count: ## Print the unread count (for the launcher; sources .env if present)
	@if [ -f .env ]; then set -a; . ./.env; set +a; fi; go run $(PKG) --count

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

rename: ## Rewrite the module path. Usage: make rename MODULE=github.com/you/slack-tui
	@test -n "$(MODULE)" || { echo "usage: make rename MODULE=github.com/you/slack-tui"; exit 1; }
	@old=`go list -m`; \
	  grep -rl "$$old" --include='*.go' . | xargs perl -pi -e "s{\Q$$old\E}{$(MODULE)}g"; \
	  go mod edit -module "$(MODULE)" && go mod tidy && echo "renamed $$old -> $(MODULE)"

help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
	  awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-9s\033[0m %s\n", $$1, $$2}'
