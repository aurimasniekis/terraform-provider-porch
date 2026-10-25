SHELL         := /usr/bin/env bash
GO            ?= go
BIN           := bin
VERSION       ?= dev
LDFLAGS       := -s -w -X main.version=$(VERSION)
TFPLUGINDOCS  ?= $(GO) run github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs@v0.25.0
GOLANGCI_LINT ?= golangci-lint

# Porch source the acceptance tests run against (exported read-only from a local checkout).
PORCH_REPO    ?= ../porch
PORCH_COMMIT  ?= e33dc75

.DEFAULT_GOAL := help
.PHONY: help build install test testacc lint fmt vet docs docs-check porch-src porch-bin families clean

help: ## List targets
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z_-]+:.*## / {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: ## Build bin/terraform-provider-porch
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN)/terraform-provider-porch .

install: ## go install the provider (for dev_overrides)
	$(GO) install -trimpath -ldflags '$(LDFLAGS)' .

test: ## Unit tests (client against httptest, schema and config validation)
	$(GO) test ./...

testacc: porch-bin ## Acceptance tests against a real `porch serve` started by TestMain
	TF_ACC=1 $(GO) test ./... -count=1 -timeout 30m $(TESTARGS)

vet: ## go vet
	$(GO) vet ./...

lint: ## golangci-lint
	$(GOLANGCI_LINT) run ./...

fmt: ## Format Go and Terraform sources
	$(GOLANGCI_LINT) fmt ./...
	terraform fmt -recursive examples

docs: ## Generate docs/ with tfplugindocs
	$(TFPLUGINDOCS) generate --provider-name porch

docs-check: docs ## Fail when generated docs are out of date (needs git)
	$(TFPLUGINDOCS) validate --provider-name porch
	git diff --exit-code -- docs

porch-src: ## Export Porch at PORCH_COMMIT into .porch-src (read-only use of PORCH_REPO)
	rm -rf .porch-src && mkdir -p .porch-src
	git -C $(PORCH_REPO) archive $(PORCH_COMMIT) | tar -x -C .porch-src

porch-bin: ## Build bin/porch and bin/fakegoogle from .porch-src
	@test -d .porch-src || $(MAKE) porch-src
	@mkdir -p .porch-src/internal/webui/dist && test -n "$$(ls -A .porch-src/internal/webui/dist)" || echo "<!doctype html>" > .porch-src/internal/webui/dist/index.html
	cd .porch-src && CGO_ENABLED=0 $(GO) build -o ../$(BIN)/porch ./cmd/porch
	cd .porch-src && CGO_ENABLED=0 $(GO) build -o ../$(BIN)/fakegoogle ./test/fakegoogle

families: build porch-bin ## Run examples/families (init/plan/apply/destroy) against a throwaway porch
	scripts/verify-families.sh

clean: ## Remove build output
	rm -rf $(BIN) dist
