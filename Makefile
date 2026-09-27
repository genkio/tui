APPS := x inoreader slack folo reddit douban bilibili
CODESIGN_ID := tui-codesign

.DEFAULT_GOAL := build
.PHONY: build run serve service restart logs service-uninstall launcher apps firewall signing-cert clean help $(APPS)

# Where this machine's server keeps the copy it syncs after every fetch. Another
# host with another path passes its own: make serve SYNC_DIR=...
SYNC_DIR := $(HOME)/box/tui

# launchd agent running the same `tui serve` as `make serve`, from this checkout.
SERVICE := com.genkio.tui
PLIST := $(HOME)/Library/LaunchAgents/$(SERVICE).plist
LOG := $(HOME)/Library/Logs/tui.log
DOMAIN = gui/$$(id -u)
# Prints the log from before the (re)start until the new server says it is up.
AWAIT_UP = n=$$(wc -l < $(LOG) 2>/dev/null || echo 0); \
	  launchctl kickstart -k $(DOMAIN)/$(SERVICE); \
	  for i in $$(seq 120); do tail -n +$$((n+1)) $(LOG) | grep -q 'listening on' && break; sleep 0.5; done; \
	  tail -n +$$((n+1)) $(LOG)

build: launcher apps ## Build tui and every standalone app binary

# The firewall remembers "Allow" by code signature. Ad-hoc signing (-s -)
# yields a new identity every build, so each rebuild re-triggers the popup;
# signing with the stable self-signed cert (make signing-cert) survives
# rebuilds, so Allow is answered once.
launcher: ## Build the main tui binary into ./tui
	go build -o ./tui ./cmd/tui
	@if [ "$$(uname)" = Darwin ]; then \
	  if security find-identity -v -p codesigning 2>/dev/null | grep -q "$(CODESIGN_ID)"; then \
	    codesign -f -s "$(CODESIGN_ID)" ./tui; \
	  else \
	    codesign -f -s - ./tui; \
	  fi; \
	fi

signing-cert: ## One-time: create a stable self-signed codesign cert so the firewall Allow sticks across rebuilds
	@if security find-identity -v -p codesigning 2>/dev/null | grep -q "$(CODESIGN_ID)"; then \
	  echo "$(CODESIGN_ID) already in the keychain; nothing to do"; \
	else \
	  tmp=$$(mktemp -d); \
	  printf '[req]\ndistinguished_name=dn\nx509_extensions=ext\nprompt=no\n[dn]\nCN=$(CODESIGN_ID)\n[ext]\nkeyUsage=critical,digitalSignature\nextendedKeyUsage=critical,codeSigning\nbasicConstraints=critical,CA:FALSE\n' > $$tmp/conf; \
	  openssl req -x509 -newkey rsa:2048 -sha256 -days 3650 -nodes -config $$tmp/conf -keyout $$tmp/key.pem -out $$tmp/cert.pem && \
	  openssl pkcs12 -export -certpbe PBE-SHA1-3DES -keypbe PBE-SHA1-3DES -macalg sha1 -out $$tmp/cert.p12 -inkey $$tmp/key.pem -in $$tmp/cert.pem -passout pass:$(CODESIGN_ID) && \
	  security import $$tmp/cert.p12 -k ~/Library/Keychains/login.keychain-db -P $(CODESIGN_ID) -T /usr/bin/codesign && \
	  security add-trusted-cert -r trustRoot -p codeSign -k ~/Library/Keychains/login.keychain-db $$tmp/cert.pem && \
	  echo "created $(CODESIGN_ID); now: make launcher && make firewall (once), Allow the popup one last time"; \
	  rm -rf $$tmp; \
	fi

firewall: launcher ## Allow other devices to reach tui serve (asks for sudo)
	sudo /usr/libexec/ApplicationFirewall/socketfilterfw --add "$(CURDIR)/tui"
	sudo /usr/libexec/ApplicationFirewall/socketfilterfw --unblockapp "$(CURDIR)/tui"

apps: $(APPS) ## Build each TUI binary

$(APPS): ## Build one TUI (e.g. make x)
	$(MAKE) -C plugins/$@ build

run: launcher ## Open the terminal All client
	./tui

# Everything, not just the launcher: serve shells out to the app binaries on
# every fetch, so a rebuild that left them behind would serve the old ones.
serve: build ## Rebuild and run the web server the way this machine runs it
	./tui serve --sync-dir $(SYNC_DIR)

# PATH is the installing shell's, since launchd's bare one lacks tailscale, pi
# and the rest the server shells out to. Re-run after changing it.
service: build ## Install tui serve as a login agent (starts at boot, restarts on crash) and start it
	@mkdir -p $(dir $(PLIST)) $(dir $(LOG))
	@printf '%s\n' \
	  '<?xml version="1.0" encoding="UTF-8"?>' \
	  '<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">' \
	  '<plist version="1.0"><dict>' \
	  '<key>Label</key><string>$(SERVICE)</string>' \
	  '<key>ProgramArguments</key><array>' \
	  '<string>$(CURDIR)/tui</string><string>serve</string><string>--sync-dir</string><string>$(SYNC_DIR)</string>' \
	  '</array>' \
	  '<key>WorkingDirectory</key><string>$(CURDIR)</string>' \
	  "<key>EnvironmentVariables</key><dict><key>PATH</key><string>$$PATH</string></dict>" \
	  '<key>RunAtLoad</key><true/>' \
	  '<key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>' \
	  '<key>ThrottleInterval</key><integer>5</integer>' \
	  '<key>StandardOutPath</key><string>$(LOG)</string>' \
	  '<key>StandardErrorPath</key><string>$(LOG)</string>' \
	  '</dict></plist>' > $(PLIST)
	@plutil -lint -s $(PLIST)
	@launchctl bootout $(DOMAIN)/$(SERVICE) 2>/dev/null; \
	  while launchctl print $(DOMAIN)/$(SERVICE) >/dev/null 2>&1; do sleep 0.2; done; \
	  launchctl bootstrap $(DOMAIN) $(PLIST)
	@$(AWAIT_UP)

restart: build ## Rebuild and restart the installed service
	@launchctl print $(DOMAIN)/$(SERVICE) >/dev/null 2>&1 || { echo "$(SERVICE) not loaded; run make service"; exit 1; }
	@$(AWAIT_UP)

logs: ## Follow the service log
	tail -n 50 -F $(LOG)

service-uninstall: ## Stop the service and remove it from login
	-launchctl bootout $(DOMAIN)/$(SERVICE)
	rm -f $(PLIST)

clean: ## Remove built binaries
	rm -f tui
	@for a in $(APPS); do $(MAKE) -C plugins/$$a clean || true; done

help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
	  awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-17s\033[0m %s\n", $$1, $$2}'
