BINARY   := gitgogit
PREFIX   ?= /usr/local
REGISTRY ?= $(DOCKER_REGISTRY)
VERSION  ?= $(shell cat .version)
IMAGE    := $(REGISTRY)/$(BINARY)

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

vet: ## Run Go vet static analysis
	go vet ./...

lint: ## Run Go linter
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.10.1 run ./...

govulncheck: ## Scan Go dependencies for known vulnerabilities
	govulncheck ./...

docker-build: ## Build Docker image
	docker build -t $(BINARY):latest .
	@if [ -n "$(REGISTRY)" ]; then docker tag $(BINARY):latest $(IMAGE):$(VERSION); fi

docker-run: docker-build ## Run container with mounted config
	env | grep -iE 'TOKEN|KEY' > /tmp/gitgogit-env 2>/dev/null || true
	docker run --rm --network host \
		-v $$(realpath $${CONFIG:-config.yaml}):/home/gitgogit/.config/gitgogit/config.yaml:ro \
		-v gitgogit-cache:/home/gitgogit/.local/share/gitgogit \
		--env-file /tmp/gitgogit-env \
		$(BINARY):latest
	rm -f /tmp/gitgogit-env

docker-push: docker-build ## Push Docker image to registry (requires DOCKER_REGISTRY)
	@if [ -z "$(REGISTRY)" ]; then echo "error: set DOCKER_REGISTRY env var"; exit 1; fi
	docker push $(IMAGE):$(VERSION)

clean: ## Remove build artifacts
	rm -f $(BINARY)

.PHONY: help build install system-install system-uninstall test vet lint govulncheck docker-build docker-run docker-push clean
.DEFAULT_GOAL := help
