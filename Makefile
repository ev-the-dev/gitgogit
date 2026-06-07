BINARY   := gitgogit
PREFIX   ?= /usr/local
REGISTRY ?= $(DOCKER_REGISTRY)
IMAGE    := $(REGISTRY)/$(BINARY)
DIST     := dist

# Version derived from git tags; embedded in the binary and used for docker tags and GH releases.
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")

# Strip debug info (symbol table and DWARF) and embed version
GO_FLAGS += -ldflags="-s -w -X 'main.version=$(VERSION)'"

# Avoid embedding build path in executable
GO_FLAGS += -trimpath

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
	rm -rf $(DIST)

  ##################
 # Platform Build #
##################

platform-all:
	@echo "Building all platform binaries..."
	@mkdir -p $(DIST)
	@$(MAKE) --no-print-directory -j4 \
		platform-darwin-arm64 \
		platform-darwin-amd64 \
		platform-linux-amd64 \
		platform-linux-arm64

platform-unixlike:
	@test -n "$(TGOOS)"   || (echo "GOOS must be set"  && false)
	@test -n "$(TGOARCH)" || (echo "GOARCH must be set" && false)
	CGO_ENABLED=0 GOOS="$(TGOOS)" GOARCH="$(TGOARCH)" \
		go build $(GO_FLAGS) -o "$(DIST)/$(BINARY)-$(TGOOS)-$(TGOARCH)"

platform-darwin-arm64:
	@$(MAKE) --no-print-directory TGOOS=darwin TGOARCH=arm64 platform-unixlike

platform-darwin-amd64:
	@$(MAKE) --no-print-directory TGOOS=darwin TGOARCH=amd64 platform-unixlike

platform-linux-amd64:
	@$(MAKE) --no-print-directory TGOOS=linux TGOARCH=amd64 platform-unixlike

platform-linux-arm64:
	@$(MAKE) --no-print-directory TGOOS=linux TGOARCH=arm64 platform-unixlike

  ###########
 # Release #
###########

release: platform-all
	@echo "Checking for gh CLI..." && gh --version > /dev/null
	@echo "Checking for a version tag..." && \
		git describe --tags --exact-match HEAD > /dev/null 2>&1 || \
		(echo "HEAD is not tagged — run: git tag vX.Y.Z" && false)
	@echo "Checking for uncommitted changes..." && \
		test -z "`git status --porcelain`" || \
		(echo "Cannot release with uncommitted changes:" && git status --porcelain && false)
	@echo "Checking for main branch..." && \
		test main = "`git rev-parse --abbrev-ref HEAD`" || \
		(echo "Cannot release from non-main branch" && false)
	@echo "Checking for unpushed commits..." && git fetch && \
		test "" = "`git cherry`" || (echo "Cannot release with unpushed commits" && false)
	@echo "Checking that tag $(VERSION) exists on remote..." && \
		git ls-remote --tags origin "refs/tags/$(VERSION)" | grep -q "$(VERSION)" || \
		(echo "Tag $(VERSION) has not been pushed — run: git push origin $(VERSION)" && false)
	gh release create "$(VERSION)" $(DIST)/$(BINARY)-* \
		--title "$(VERSION)" \
		--generate-notes

.PHONY: help build install system-install system-uninstall test vet lint govulncheck docker-build docker-run docker-push clean platform-all platform-unixlike platform-darwin-arm64 platform-darwin-amd64 platform-linux-amd64 platform-linux-arm64 release
.DEFAULT_GOAL := help
