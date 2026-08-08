APPS := x inoreader slack folo reddit douban
CODESIGN_ID := tui-codesign

.DEFAULT_GOAL := build
.PHONY: build run launcher apps firewall signing-cert clean help $(APPS)

build: launcher apps ## Build the launcher and every TUI

# The firewall remembers "Allow" by code signature. Ad-hoc signing (-s -)
# yields a new identity every build, so each rebuild re-triggers the popup;
# signing with the stable self-signed cert (make signing-cert) survives
# rebuilds, so Allow is answered once.
launcher: ## Build the launcher binary into ./tui
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

firewall: launcher ## Allow ./tui through the macOS firewall so other devices reach --web (asks for sudo)
	sudo /usr/libexec/ApplicationFirewall/socketfilterfw --add "$(CURDIR)/tui"
	sudo /usr/libexec/ApplicationFirewall/socketfilterfw --unblockapp "$(CURDIR)/tui"

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
