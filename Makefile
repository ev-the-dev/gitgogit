BINARY  := gitgogit
PREFIX  ?= /usr/local

help: ## Display available Make targets
	@echo ""
	@echo "Available targets:"
	@echo ""
	@grep -E '^[a-zA-Z0-9_-]+:.*?## ' Makefile | \
		awk 'BEGIN {FS = ":.*?## "} {printf "  %-20s %s\n", $$1, $$2}'
	@echo ""

build: ## Build the Go binary
	go build -o $(BINARY) .

install: ## Install via GOPATH/bin (no sudo required)
	go install .

system-install: build ## Copy binary to $(PREFIX)/bin (may require sudo)
	install -d $(PREFIX)/bin
	install -m 755 $(BINARY) $(PREFIX)/bin/$(BINARY)

system-uninstall: ## Remove binary from $(PREFIX)/bin
	rm -f $(PREFIX)/bin/$(BINARY)

test: ## Run Go tests
	go test ./...

clean: ## Remove build artifacts
	rm -f $(BINARY)

.PHONY: help build install system-install system-uninstall test clean
.DEFAULT_GOAL := help
