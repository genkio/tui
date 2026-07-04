APPS := x inoreader slack folo

.DEFAULT_GOAL := build
.PHONY: build run launcher apps clean help $(APPS)

build: launcher apps ## Build the launcher and every TUI

launcher: ## Build the launcher binary into ./tui
	go build -o ./tui ./cmd/tui

apps: $(APPS) ## Build each TUI binary

$(APPS): ## Build one TUI (e.g. make x)
	$(MAKE) -C plugins/$@ build

run: launcher ## Launch the picker (each TUI compiles on first open)
	./tui

clean: ## Remove built binaries
	rm -f tui
	@for a in $(APPS); do $(MAKE) -C plugins/$$a clean || true; done

help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
	  awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-9s\033[0m %s\n", $$1, $$2}'
